package main

import (
	"golang.org/x/sys/windows"
	"net"
	"syscall"
)

func portListenConfig() net.ListenConfig {
	return net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var socketErr error
		err := raw.Control(func(fd uintptr) {
			// Winsock permits overlapping wildcard/interface binds unless exclusive.
			const soExclusiveAddrUse = ^windows.SO_REUSEADDR
			socketErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, soExclusiveAddrUse, 1)
		})
		if err != nil {
			return err
		}
		return socketErr
	}}
}
