//go:build !windows

package control

import "errors"

const PipeName = ""

func RunServer(func(string) string) error { return errors.New("control: Windows only") }

func Send(string) (string, error) { return "", errors.New("control: Windows only") }
