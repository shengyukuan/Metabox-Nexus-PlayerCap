//go:build !windows

package tray

import "errors"

func Run(string, bool) error { return errors.New("tray: Windows only") }
