//go:build windows

// Package control provides the private control channel used by the tray
// companion.  It deliberately uses a named pipe instead of an HTTP endpoint:
// the player-capture HTTP server may listen on 0.0.0.0, while start/stop is a
// local process-management operation.
package control

import (
	"errors"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const PipeName = `\\.\pipe\Metabox-Nexus-PlayerCap-control`

const (
	pipeAccessDuplex   = 0x00000003
	pipeTypeMessage    = 0x00000004
	pipeReadModeMsg    = 0x00000002
	pipeWait           = 0x00000000
	pipeUnlimited      = 255
	bufferSize         = 4096
	maxClientAttempts  = 30
	clientAttemptDelay = 100 * time.Millisecond
)

var (
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procCreateNamedPipeW   = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe   = kernel32.NewProc("ConnectNamedPipe")
	procDisconnectNamedPipe = kernel32.NewProc("DisconnectNamedPipe")
	procWaitNamedPipeW     = kernel32.NewProc("WaitNamedPipeW")
)

// RunServer blocks while serving commands. The callback must be non-blocking;
// the main process uses it only to enqueue a shutdown request.
func RunServer(handle func(command string) string) error {
	if handle == nil {
		return errors.New("control: nil command handler")
	}

	name := windows.StringToUTF16Ptr(PipeName)
	for {
		h, err := createNamedPipe(name)
		if err != nil {
			return err
		}

		if err := connectNamedPipe(h); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			_ = windows.CloseHandle(h)
			return err
		}

		var request [bufferSize]byte
		var read uint32
		if err := windows.ReadFile(h, request[:], &read, nil); err == nil && read > 0 {
			response := handle(strings.TrimSpace(string(request[:read])))
			if response != "" {
				payload := []byte(response + "\n")
				var written uint32
				_ = windows.WriteFile(h, payload, &written, nil)
			}
		}

		_ = disconnectNamedPipe(h)
		_ = windows.CloseHandle(h)
	}
}

// Send sends one command to the service and returns its one-line response.
func Send(command string) (string, error) {
	name := windows.StringToUTF16Ptr(PipeName)
	var h windows.Handle
	var err error
	for i := 0; i < maxClientAttempts; i++ {
		h, err = windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			break
		}
		if waitErr := waitNamedPipe(name, uint32(clientAttemptDelay/time.Millisecond)); waitErr != nil && i == maxClientAttempts-1 {
			return "", err
		}
		time.Sleep(clientAttemptDelay)
	}
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	payload := []byte(strings.TrimSpace(command) + "\n")
	var written uint32
	if err := windows.WriteFile(h, payload, &written, nil); err != nil {
		return "", err
	}

	var response [bufferSize]byte
	var read uint32
	if err := windows.ReadFile(h, response[:], &read, nil); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(response[:read])), nil
}

func createNamedPipe(name *uint16) (windows.Handle, error) {
	r, _, callErr := procCreateNamedPipeW.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(pipeAccessDuplex),
		uintptr(pipeTypeMessage|pipeReadModeMsg|pipeWait),
		uintptr(pipeUnlimited),
		bufferSize,
		bufferSize,
		0,
		0,
	)
	if r == uintptr(^windows.Handle(0)) {
		return 0, callErr
	}
	return windows.Handle(r), nil
}

func connectNamedPipe(h windows.Handle) error {
	r, _, callErr := procConnectNamedPipe.Call(uintptr(h), 0)
	if r != 0 {
		return nil
	}
	if last := windows.GetLastError(); last != windows.ERROR_SUCCESS {
		return last
	}
	return callErr
}

func disconnectNamedPipe(h windows.Handle) error {
	r, _, callErr := procDisconnectNamedPipe.Call(uintptr(h))
	if r != 0 {
		return nil
	}
	return callErr
}

func waitNamedPipe(name *uint16, timeoutMS uint32) error {
	r, _, callErr := procWaitNamedPipeW.Call(uintptr(unsafe.Pointer(name)), uintptr(timeoutMS))
	if r != 0 {
		return nil
	}
	return callErr
}
