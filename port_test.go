package main

import (
	"net"
	"strconv"
	"testing"
)

func TestPortEntryRejectsClashesWithoutChangingConfiguration(t *testing.T) {
	c := validConfig(t)
	m := newModel(c, map[string]bool{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := m.set("cfx-port", strconv.Itoa(port)); err == nil {
		t.Fatal("accepted occupied TCP port")
	}
	if m.config.CFXPort != c.CFXPort {
		t.Fatal("changed configuration after failed validation")
	}
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	port = packet.LocalAddr().(*net.UDPAddr).Port
	if err := m.set("cfx-port", strconv.Itoa(port)); err == nil {
		t.Fatal("accepted occupied UDP port")
	}
	m.config.Adminer = true
	if err := m.set("cfx-port", strconv.Itoa(m.config.AdminerPort)); err == nil {
		t.Fatal("accepted selected Adminer port")
	}
	if err := m.set("tx-port", "30120"); err == nil {
		t.Fatal("accepted reserved txAdmin port")
	}
}
