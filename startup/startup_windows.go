//go:build windows

package startup

import (
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

const valueName = "MetaboxNexusPlayerCapTray"

func Enabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return false, nil
		}
		return false, err
	}
	defer key.Close()
	_, _, err = key.GetStringValue(valueName)
	if err == registry.ErrNotExist {
		return false, nil
	}
	return err == nil, err
}

func SetEnabled(exePath string, enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if !enabled {
		if err := key.DeleteValue(valueName); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}

	// Quote the path because an installation directory may contain spaces.
	return key.SetStringValue(valueName, `"`+filepath.Clean(exePath)+`"`)
}
