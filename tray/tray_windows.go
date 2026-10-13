//go:build windows

package tray

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"Metabox-Nexus-PlayerCap/control"
	"Metabox-Nexus-PlayerCap/startup"
	"golang.org/x/sys/windows"
)

const (
	className = "MetaboxNexusPlayerCapTrayWindow"
	trayMsg   = 0x0401

	menuStart uintptr = 1001 + iota
	menuStop
	menuRestart
	menuOpen
	menuOpenLyricDisplay
	menuOpenLyricPage
	menuOpenEffectDisplay
	menuOpenEffectPage
	menuLogs
	menuAutostart
	menuExit

	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmCommand       = 0x0111
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	csHRedraw       = 0x0002
	csVRedraw       = 0x0001
	menuString      = 0x0000
	tpmRightButton  = 0x0002
	nifMessage      = 0x0001
	nifIcon         = 0x0002
	nifTip          = 0x0004
	nimAdd          = 0x0000
	nimModify       = 0x0001
	nimDelete       = 0x0002
	nifGuid         = 0x0020
	idiApplication  = 32512
	idcArrow        = 32512
	mbOK            = 0x0000
	mbIconError     = 0x0010
	createNewGroup  = 0x00000200
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClass = user32.NewProc("RegisterClassExW")
	procCreateWindow  = user32.NewProc("CreateWindowExW")
	procDefWindow     = user32.NewProc("DefWindowProcW")
	procDestroyWindow = user32.NewProc("DestroyWindow")
	procGetMessage    = user32.NewProc("GetMessageW")
	procTranslate     = user32.NewProc("TranslateMessage")
	procDispatch      = user32.NewProc("DispatchMessageW")
	procPostQuit      = user32.NewProc("PostQuitMessage")
	procCreateMenu    = user32.NewProc("CreatePopupMenu")
	procAppendMenu    = user32.NewProc("AppendMenuW")
	procDestroyMenu   = user32.NewProc("DestroyMenu")
	procTrackMenu     = user32.NewProc("TrackPopupMenu")
	procCursorPos     = user32.NewProc("GetCursorPos")
	procSetForeground = user32.NewProc("SetForegroundWindow")
	procPostMessage   = user32.NewProc("PostMessageW")
	procRegisterMsg   = user32.NewProc("RegisterWindowMessageW")
	procLoadIcon      = user32.NewProc("LoadIconW")
	procLoadCursor    = user32.NewProc("LoadCursorW")
	procModuleHandle  = kernel32.NewProc("GetModuleHandleW")
	procDestroyIcon   = user32.NewProc("DestroyIcon")
	procMessageBox    = user32.NewProc("MessageBoxW")
	procFreeConsole   = kernel32.NewProc("FreeConsole")
	procCreateMutex   = kernel32.NewProc("CreateMutexW")
	procExtractIcon   = shell32.NewProc("ExtractIconExW")
	procNotify        = shell32.NewProc("Shell_NotifyIconW")

	// Callback trampolines are package globals by design.
	windowCallback = windows.NewCallback(windowProc)
	active         *controller
	taskbarCreated uint32
	// A stable identity lets a new tray process remove an icon left behind
	// when the previous process was terminated by Task Manager.
	trayIdentity = [16]byte{0x4d, 0x4e, 0x50, 0x43, 0x01, 0x00, 0x01, 0x00, 0x8a, 0x39, 0x4e, 0x70, 0xa1, 0x5c, 0x29, 0x61}
)

type point struct{ x, y int32 }

type message struct {
	hwnd    uintptr
	message uint32
	_       uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	_       uint32
}

type wndClassEx struct {
	cbSize      uint32
	style       uint32
	callback    uintptr
	classExtra  int32
	windowExtra int32
	instance    windows.Handle
	icon        windows.Handle
	cursor      windows.Handle
	background  windows.Handle
	menu        *uint16
	className   *uint16
	smallIcon   windows.Handle
}

type notifyIconData struct {
	cbSize          uint32
	hWnd            windows.Handle
	uID             uint32
	uFlags          uint32
	callbackMessage uint32
	hIcon           windows.Handle
	tip             [128]uint16
	state           uint32
	stateMask       uint32
	info            [256]uint16
	version         uint32
	infoTitle       [64]uint16
	infoFlags       uint32
	guid            [16]byte
	balloonIcon     windows.Handle
}

type nativeTray struct {
	mu         sync.Mutex
	hwnd       windows.Handle
	icon       notifyIconData
	iconHandle windows.Handle
	ownsIcon   bool
}

type controller struct {
	mu       sync.Mutex
	corePath string
	cmd      *exec.Cmd
	logs     []string
	native   *nativeTray
	exitOnce sync.Once
}

func Run(corePath string, install bool) error {
	// A Win32 window and its message queue belong to the OS thread that
	// created them. Keep that thread fixed through window creation, dispatch,
	// and destruction; otherwise the Go scheduler can move this goroutine to a
	// different thread after a blocking service-control call, leaving tray
	// notifications queued on a thread whose message loop is no longer running.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if install {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		if err := startup.SetEnabled(self, true); err != nil {
			return fmt.Errorf("enable login startup: %w", err)
		}
	}
	mutex, alreadyRunning, err := acquireTrayMutex()
	if err != nil {
		return err
	}
	if alreadyRunning {
		return nil
	}
	defer windows.CloseHandle(mutex)

	procFreeConsole.Call()
	taskbarCreated = registerTaskbarCreated()
	c := &controller{corePath: filepath.Clean(corePath)}
	active = c
	var trayErr error
	c.native, trayErr = newNativeTray(c.corePath)
	if trayErr != nil {
		return trayErr
	}
	defer c.native.close()

	// Do not block the tray window thread while the core service is starting.
	// When both processes are stopped, control.Send may retry for several
	// seconds before c.start can launch the core. The tray icon is already
	// visible by this point, so the message loop must begin immediately or
	// clicks appear to be ignored during that startup window.
	c.async(func() {
		if _, err := control.Send("status"); err != nil {
			c.start()
		}
	})
	go c.refreshLoop()
	return c.messageLoop()
}

func acquireTrayMutex() (windows.Handle, bool, error) {
	name := windows.StringToUTF16Ptr(`Local\Metabox-Nexus-PlayerCap-Tray`)
	r, _, callErr := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if r == 0 {
		if callErr == nil {
			callErr = fmt.Errorf("CreateMutexW failed")
		}
		return 0, false, callErr
	}
	if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
		_ = windows.CloseHandle(windows.Handle(r))
		return 0, true, nil
	}
	return windows.Handle(r), false, nil
}

// Uninstall disables login startup and asks an already running core to stop.
// A missing service is not an uninstall failure.
func Uninstall() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := startup.SetEnabled(self, false); err != nil {
		return err
	}
	_, _ = control.Send("stop")
	return nil
}

func (c *controller) start() {
	c.mu.Lock()
	if c.cmd != nil {
		c.mu.Unlock()
		return
	}
	cmd := exec.Command(c.corePath)
	cmd.Stdin = strings.NewReader("")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNewGroup}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.addLogLocked("stdout pipe: " + err.Error())
		c.mu.Unlock()
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		c.addLogLocked("stderr pipe: " + err.Error())
		c.mu.Unlock()
		return
	}
	if err := cmd.Start(); err != nil {
		c.addLogLocked("start service: " + err.Error())
		c.mu.Unlock()
		return
	}
	c.cmd = cmd
	c.addLogLocked("service started")
	c.mu.Unlock()
	go c.consume(stdout)
	go c.consume(stderr)
	go c.wait(cmd)
	c.refreshTip()
}

func (c *controller) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	c.mu.Lock()
	if c.cmd == cmd {
		c.cmd = nil
	}
	if err != nil {
		c.addLogLocked("service exited: " + err.Error())
	} else {
		c.addLogLocked("service exited")
	}
	c.mu.Unlock()
	c.refreshTip()
}

func (c *controller) stop() {
	_, _ = control.Send("stop")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := control.Send("status"); err != nil {
			c.refreshTip()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	c.mu.Lock()
	cmd := c.cmd
	c.addLogLocked("graceful stop timed out")
	c.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	c.refreshTip()
}

func (c *controller) restart() {
	c.stop()
	time.Sleep(500 * time.Millisecond)
	c.start()
}

func (c *controller) consume(r io.Reader) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		c.mu.Lock()
		c.addLogLocked(s.Text())
		c.mu.Unlock()
	}
}

func (c *controller) addLogLocked(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	c.logs = append(c.logs, line)
	if len(c.logs) > 40 {
		c.logs = c.logs[len(c.logs)-40:]
	}
}

func (c *controller) diagnostics() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.logs) == 0 {
		return "No service output."
	}
	return strings.Join(c.logs, "\n")
}

func (c *controller) refreshLoop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for range t.C {
		c.refreshTip()
	}
}

func (c *controller) refreshTip() {
	if c.native == nil {
		return
	}
	if _, err := control.Send("status"); err == nil {
		c.native.setTip("Metabox PlayerCap - Running")
	} else {
		c.native.setTip("Metabox PlayerCap - Stopped")
	}
}

func (c *controller) async(fn func()) {
	go func() {
		defer func() { _ = recover() }()
		fn()
		c.refreshTip()
	}()
}

func (c *controller) openPage() {
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", "http://127.0.0.1:8765").Start()
}

func (c *controller) openHTML(name string) {
	self, err := os.Executable()
	if err != nil {
		showError(err)
		return
	}
	path := filepath.Join(filepath.Dir(self), name)
	if _, err := os.Stat(path); err != nil {
		showError(fmt.Errorf("HTML file not found: %s", path))
		return
	}
	if err := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", path).Start(); err != nil {
		showError(err)
	}
}

func (c *controller) toggleAutostart() {
	self, err := os.Executable()
	if err != nil {
		showError(err)
		return
	}
	enabled, err := startup.Enabled()
	if err != nil {
		showError(err)
		return
	}
	if err := startup.SetEnabled(self, !enabled); err != nil {
		showError(err)
	}
}

func (c *controller) menu() {
	menu, _, _ := procCreateMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	addMenu(menu, menuStart, "启动服务")
	addMenu(menu, menuStop, "停止服务")
	addMenu(menu, menuRestart, "重启服务")
	addMenu(menu, menuOpen, "打开网页")
	addMenu(menu, menuOpenLyricDisplay, "打开歌词显示页")
	addMenu(menu, menuOpenLyricPage, "打开歌词配置页面")
	addMenu(menu, menuOpenEffectDisplay, "打开特效显示页")
	addMenu(menu, menuOpenEffectPage, "打开特效页面")
	addMenu(menu, menuLogs, "查看诊断信息")
	if enabled, err := startup.Enabled(); err == nil && enabled {
		addMenu(menu, menuAutostart, "禁用开机自启")
	} else {
		addMenu(menu, menuAutostart, "启用开机自启")
	}
	addMenu(menu, menuExit, "退出托盘程序")
	var p point
	if r, _, _ := procCursorPos.Call(uintptr(unsafe.Pointer(&p))); r == 0 {
		return
	}
	procSetForeground.Call(uintptr(c.native.hwnd))
	procTrackMenu.Call(menu, tpmRightButton, uintptr(p.x), uintptr(p.y), 0, uintptr(c.native.hwnd), 0)
	procPostMessage.Call(uintptr(c.native.hwnd), 0, 0, 0)
}

func (c *controller) command(id uintptr) {
	switch id {
	case menuStart:
		c.async(c.start)
	case menuStop:
		c.async(c.stop)
	case menuRestart:
		c.async(c.restart)
	case menuOpen:
		c.openPage()
	case menuOpenLyricDisplay:
		c.openHTML("lyric_display.html")
	case menuOpenLyricPage:
		c.openHTML("lyric_page.html")
	case menuOpenEffectDisplay:
		c.openHTML("effect_display.html")
	case menuOpenEffectPage:
		c.openHTML("effect_page.html")
	case menuLogs:
		messageBox("Metabox PlayerCap 诊断信息", c.diagnostics(), mbOK)
	case menuAutostart:
		c.toggleAutostart()
	case menuExit:
		c.async(func() {
			c.stop()
			// DestroyWindow must run on the thread that owns the tray window.
			// Post WM_CLOSE so the message loop performs the destroy and reaches
			// the deferred Shell_NotifyIcon(NIM_DELETE) cleanup.
			c.exitOnce.Do(func() {
				procPostMessage.Call(uintptr(c.native.hwnd), wmClose, 0, 0)
			})
		})
	}
}

func newNativeTray(corePath string) (*nativeTray, error) {
	classPtr := windows.StringToUTF16Ptr(className)
	instance := moduleHandle()
	icon, ownsIcon := loadCoreIcon(corePath)
	if icon == 0 {
		icon = loadIcon(idiApplication)
	}
	class := wndClassEx{
		cbSize:    uint32(unsafe.Sizeof(wndClassEx{})),
		style:     csHRedraw | csVRedraw,
		callback:  windowCallback,
		instance:  instance,
		icon:      icon,
		cursor:    loadCursor(idcArrow),
		className: classPtr,
		smallIcon: icon,
	}
	if r, _, err := procRegisterClass.Call(uintptr(unsafe.Pointer(&class))); r == 0 && err != windows.ERROR_CLASS_ALREADY_EXISTS {
		if ownsIcon && icon != 0 {
			procDestroyIcon.Call(uintptr(icon))
		}
		if err == nil {
			err = fmt.Errorf("register tray window class failed")
		}
		return nil, err
	}
	hwnd, _, err := procCreateWindow.Call(0, uintptr(unsafe.Pointer(classPtr)), uintptr(unsafe.Pointer(classPtr)), 0, 0, 0, 0, 0, 0, 0, uintptr(instance), 0)
	if hwnd == 0 {
		if ownsIcon && icon != 0 {
			procDestroyIcon.Call(uintptr(icon))
		}
		if err == nil {
			err = fmt.Errorf("create tray window failed")
		}
		return nil, err
	}
	t := &nativeTray{
		hwnd:       windows.Handle(hwnd),
		iconHandle: windows.Handle(icon),
		ownsIcon:   ownsIcon,
	}
	t.icon = notifyIconData{
		cbSize:          uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:            t.hwnd,
		uID:             1,
		uFlags:          nifMessage | nifIcon | nifTip | nifGuid,
		callbackMessage: trayMsg,
		hIcon:           icon,
		guid:            trayIdentity,
	}
	copy(t.icon.tip[:], windows.StringToUTF16("Metabox PlayerCap"))
	if !t.add() {
		procDestroyWindow.Call(hwnd)
		if t.ownsIcon && t.iconHandle != 0 {
			procDestroyIcon.Call(uintptr(t.iconHandle))
		}
		return nil, fmt.Errorf("add tray icon failed")
	}
	return t, nil
}

// add registers the icon with the current Explorer taskbar. Explorer removes
// all notification icons when it restarts, while the tray process and its
// hidden window keep running. TaskbarCreated is broadcast after the new shell
// is ready, so the same icon data can be registered again safely.
func (t *nativeTray) add() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Remove a previous registration with this stable identity first. This is
	// important after Task Manager termination, where the old process cannot
	// run NIM_DELETE and Explorer may retain a dead icon entry.
	t.icon.uFlags = nifGuid
	procNotify.Call(nimDelete, uintptr(unsafe.Pointer(&t.icon)))
	t.icon.uFlags = nifMessage | nifIcon | nifTip | nifGuid
	r, _, _ := procNotify.Call(nimAdd, uintptr(unsafe.Pointer(&t.icon)))
	return r != 0
}

func (t *nativeTray) setTip(value string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	copy(t.icon.tip[:], windows.StringToUTF16(value))
	t.icon.uFlags = nifGuid | nifTip
	procNotify.Call(nimModify, uintptr(unsafe.Pointer(&t.icon)))
	t.icon.uFlags = nifMessage | nifIcon | nifTip | nifGuid
}

func (t *nativeTray) close() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.icon.uFlags = nifGuid
	procNotify.Call(nimDelete, uintptr(unsafe.Pointer(&t.icon)))
	procDestroyWindow.Call(uintptr(t.hwnd))
	if t.ownsIcon && t.iconHandle != 0 {
		procDestroyIcon.Call(uintptr(t.iconHandle))
	}
}

func (c *controller) messageLoop() error {
	var msg message
	for {
		r, _, err := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) == -1 {
			return err
		}
		if r == 0 {
			return nil
		}
		procTranslate.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatch.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func windowProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if taskbarCreated != 0 && msg == taskbarCreated {
		if active != nil && active.native != nil {
			active.native.add()
		}
		return 0
	}

	switch msg {
	case trayMsg:
		if active != nil {
			switch uint32(lParam) {
			case wmRButtonUp, wmLButtonUp:
				active.menu()
			case wmLButtonDblClk:
				active.openPage()
			}
		}
	case wmCommand:
		if active != nil {
			active.command(wParam & 0xffff)
		}
	case wmDestroy:
		procPostQuit.Call(0)
	default:
		r, _, _ := procDefWindow.Call(hwnd, uintptr(msg), wParam, lParam)
		return r
	}
	return 0
}

func registerTaskbarCreated() uint32 {
	name := windows.StringToUTF16Ptr("TaskbarCreated")
	r, _, _ := procRegisterMsg.Call(uintptr(unsafe.Pointer(name)))
	return uint32(r)
}

func addMenu(menu, id uintptr, text string) {
	procAppendMenu.Call(menu, menuString, id, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
}

func showError(err error) {
	messageBox("Metabox PlayerCap", err.Error(), mbOK|mbIconError)
}

func messageBox(title, text string, flags uintptr) {
	procMessageBox.Call(0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(title))), flags)
}

func moduleHandle() windows.Handle {
	r, _, _ := procModuleHandle.Call(0)
	return windows.Handle(r)
}

func loadIcon(id uintptr) windows.Handle {
	r, _, _ := procLoadIcon.Call(0, id)
	return windows.Handle(r)
}

func loadCoreIcon(path string) (windows.Handle, bool) {
	name := windows.StringToUTF16Ptr(path)
	var large, small windows.Handle
	r, _, _ := procExtractIcon.Call(
		uintptr(unsafe.Pointer(name)),
		0,
		uintptr(unsafe.Pointer(&large)),
		uintptr(unsafe.Pointer(&small)),
		1,
	)
	if r == 0 {
		return 0, false
	}
	if small != 0 {
		if large != 0 {
			procDestroyIcon.Call(uintptr(large))
		}
		return small, true
	}
	return large, large != 0
}

func loadCursor(id uintptr) windows.Handle {
	r, _, _ := procLoadCursor.Call(0, id)
	return windows.Handle(r)
}
