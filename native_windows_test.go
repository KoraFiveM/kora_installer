package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The helper is a temporary test executable, never FXServer or a database.
func TestMain(m *testing.M) {
	if os.Getenv("KORA_NATIVE_TEST_HELPER") == "1" {
		if err := os.WriteFile(os.Getenv("KORA_NATIVE_TEST_READY"), []byte("ready"), 0600); err != nil {
			os.Exit(2)
		}
		for {
			time.Sleep(time.Second)
		}
	}
	os.Exit(m.Run())
}

func TestNativeProcessJobLifecycle(t *testing.T) {
	root := t.TempDir()
	c := nativeTestConfig(t)
	if err := generate(root, Plan{Config: c}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "artifacts"), 0700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = copyNewFile(self, filepath.Join(root, "artifacts", "FXServer.exe")); err != nil {
		t.Fatal(err)
	}
	if err = writeNew(root, "native.json", jsonBytes(nativeInstallation{Version: 1}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = writeNew(root, "install.complete", nil, 0600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(root, "helper.ready")
	t.Setenv("KORA_NATIVE_TEST_HELPER", "1")
	t.Setenv("KORA_NATIVE_TEST_READY", ready)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- nativeRun(ctx, root) }()
	deadline := time.After(15 * time.Second)
	for !regularFile(ready) {
		select {
		case err := <-done:
			t.Fatalf("worker exited before readiness: %v", err)
		case <-deadline:
			t.Fatal("worker did not start")
		case <-time.After(50 * time.Millisecond):
		}
	}
	if err := nativeRun(ctx, root); err == nil {
		t.Fatal("duplicate worker accepted")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("job did not stop")
	}
	if got := nativeStatus(root); got != "Native process stopped" {
		t.Fatal(got)
	}
}
