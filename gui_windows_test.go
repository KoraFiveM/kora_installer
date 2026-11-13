//go:build windows && gui

package main

import (
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lxn/walk"
)

// Real native widgets, synthetic data only: no prerequisites, services or networks.
func TestControlWindow(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	c := guiTestConfig(t)
	g, err := newSetupGUI(c)
	if err != nil {
		t.Fatal(err)
	}
	defer g.window.Dispose()
	if !strings.Contains(g.window.Title(), "Kora Control") {
		t.Fatal("wrong app identity")
	}
	g.edit("license").SetText("PASTED_DEMO_LICENSE")
	if g.edit("license").Text() != "PASTED_DEMO_LICENSE" || !g.edit("license").PasswordMode() {
		t.Fatal("masked edit round trip")
	}
	for page := 0; page < 6; page++ {
		g.show(page)
		if !g.pages[page].Visible() {
			t.Fatalf("page %d hidden", page)
		}
	}
	g.show(1)
	g.edit("hostname").SetText("My first FiveM server")
	g.edit("cfx-user").SetText("your-cfx-username")
	g.status.SetText("Preview with simulated values — no installation started")
	go func() {
		time.Sleep(500 * time.Millisecond)
		g.window.Synchronize(func() {
			if target := os.Getenv("KORA_GUI_SCREENSHOT"); target != "" {
				bitmap, err := walk.NewBitmapFromWindow(g.window)
				if err != nil {
					t.Error(err)
				} else {
					defer bitmap.Dispose()
					img, err := bitmap.ToImage()
					if err != nil {
						t.Error(err)
					} else {
						if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
							t.Error(err)
						}
						f, err := os.Create(target)
						if err != nil {
							t.Error(err)
						} else {
							if err := png.Encode(f, img); err != nil {
								t.Error(err)
							}
							f.Close()
						}
					}
				}
			}
			g.window.Close()
		})
	}()
	g.window.Run()
}
