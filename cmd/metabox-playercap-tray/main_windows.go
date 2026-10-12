//go:build windows

package main

import (
	"os"
	"path/filepath"
	"sort"

	"Metabox-Nexus-PlayerCap/tray"
)

func main() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	install := len(os.Args) > 1 && os.Args[1] == "--install"
	if len(os.Args) > 1 && os.Args[1] == "--uninstall" {
		_ = tray.Uninstall()
		return
	}
	core := filepath.Join(filepath.Dir(self), "Metabox-Nexus-PlayerCap.exe")
	if _, err := os.Stat(core); err != nil {
		matches, _ := filepath.Glob(filepath.Join(filepath.Dir(self), "Metabox-Nexus-PlayerCap-*.exe"))
		matches = filterCoreExecutables(matches)
		sort.Strings(matches)
		if len(matches) > 0 {
			core = matches[len(matches)-1]
		}
	}
	_ = tray.Run(core, install)
}

func filterCoreExecutables(paths []string) []string {
	filtered := paths[:0]
	for _, path := range paths {
		if filepath.Base(path) != "Metabox-Nexus-PlayerCap-Tray.exe" {
			filtered = append(filtered, path)
		}
	}
	return filtered
}
