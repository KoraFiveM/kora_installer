package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func guiTestConfig(t *testing.T) Config {
	c := nativeTestConfig(t)
	c.DirectoryName = "my-server"
	c.Output = t.TempDir()
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.CFXPort = tcp.Addr().(*net.TCPAddr).Port
	tcp.Close()
	return c
}
func TestGUIRejectsExistingFolderAndMisplacedLicense(t *testing.T) {
	c := guiTestConfig(t)
	if err := guiValidate(c, 1); err != nil {
		t.Fatal(err)
	}
	c.CFXUser = "cfxk_NOT_A_USERNAME"
	if err := guiValidate(c, 1); err == nil || !strings.Contains(err.Error(), "license") {
		t.Fatal(err)
	}
	c.CFXUser = "fixture"
	folder := filepath.Join(c.Output, c.DirectoryName)
	os.Mkdir(folder, 0700)
	os.WriteFile(filepath.Join(folder, "keep"), []byte("retained"), 0600)
	if err := guiValidate(c, 1); err == nil {
		t.Fatal("existing destination accepted")
	}
	if data, _ := os.ReadFile(filepath.Join(folder, "keep")); string(data) != "retained" {
		t.Fatal("changed existing data")
	}
}
func TestGUIReviewAndLogsHideSecrets(t *testing.T) {
	c := guiTestConfig(t)
	c.License = "license-fixture"
	c.DBPassword = "database-fixture"
	c.DBAdminPassword = "admin-fixture"
	p := Plan{Config: c, Resources: []Resource{{Path: "[kora]/fixture", Ref: "v1.2.3", SelectionNote: c.License}}}
	text := guiReview(p) + guiRedact("\x1b[31m"+c.License+" "+c.DBPassword+" "+c.DBAdminPassword+"\x00", c)
	for _, secret := range []string{c.License, c.DBPassword, c.DBAdminPassword, "\x1b", "\x00"} {
		if strings.Contains(text, secret) {
			t.Fatalf("unsafe display: %q", secret)
		}
	}
	if !strings.Contains(text, "v1.2.3") || !strings.Contains(text, c.DirectoryName) {
		t.Fatal("review missing concrete plan")
	}
}
func TestGUIBlocksPortConflictBeforeReview(t *testing.T) {
	c := guiTestConfig(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	c.CFXPort = listener.Addr().(*net.TCPAddr).Port
	if err := guiValidate(c, 1); err == nil {
		t.Fatal("occupied port accepted")
	}
	c.CFXPort = 30120
	c.TXAdmin = true
	c.TXPort = c.CFXPort
	if err := guiValidate(c, 2); err == nil {
		t.Fatal("selected port collision accepted")
	}
}
