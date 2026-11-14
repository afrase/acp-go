//go:build windows

package acp_test

import "syscall"

func alive(pid int) bool {
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid)) //nolint:gosec // a pid fits
	if err != nil {
		return false
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	ev, _ := syscall.WaitForSingleObject(h, 0)
	return ev == syscall.WAIT_TIMEOUT
}
