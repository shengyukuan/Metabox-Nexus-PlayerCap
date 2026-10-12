//go:build !windows

package startup

import "errors"

func Enabled() (bool, error) { return false, errors.New("startup: Windows only") }

func SetEnabled(string, bool) error { return errors.New("startup: Windows only") }
