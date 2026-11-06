//go:build !linux

package config

import (
	"errors"
	"syscall"
)

// SignalSingBox stub for non-linux systems
func SignalSingBox(sig syscall.Signal) error {
	return errors.New("SignalSingBox only supported on linux")
}

// WriteSysctl stub for non-linux systems
func WriteSysctl(key, val string) error {
	return errors.New("WriteSysctl only supported on linux")
}
