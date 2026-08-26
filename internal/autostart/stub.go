//go:build !linux

package autostart

import "errors"

var errUnsupported = errors.New("autostart is only supported on Linux")

func AutostartPath(home string) string { return "" }

func BuildDesktopEntry(executable string) (string, error) { return "", errUnsupported }

func WriteAutostart(home, executable string) error { return errUnsupported }

func RemoveAutostart(home string) error { return nil }

func IsAutostartEnabled(home string) (bool, error) { return false, nil }
