//go:build !windows

package main

import "net"

func portListenConfig() net.ListenConfig { return net.ListenConfig{} }
