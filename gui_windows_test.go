//go:build windows && gui

package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/win"
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
	g.edit("db-port").SetText("not-a-port")
	if _, err := g.readConfig(); err != nil {
		t.Fatalf("hidden database field blocked earlier page: %v", err)
	}
	g.show(2)
	if _, err := g.readConfig(); err == nil {
		t.Fatal("invalid visible database port accepted")
	}
	g.edit("db-port").SetText("3306")
	g.show(1)
	g.edit("hostname").SetText("My first FiveM server")
	g.edit("cfx-user").SetText("your-cfx-username")
	g.status.SetText("Preview with simulated values — no installation started")
	go func() {
		time.Sleep(500 * time.Millisecond)
		g.window.Synchronize(func() {
			b := g.edit("license").Bounds()
			if !win.IsWindowVisible(g.edit("license").Handle()) {
				t.Error("license input is not visible in Windows")
			}
			if b.Width < 100 || b.Height < 10 {
				t.Errorf("license input has invalid layout: %+v", b)
			}
			if target := os.Getenv("KORA_GUI_SCREENSHOT"); target != "" {
				// Capture the visible desktop pixels, not WM_PRINT (native tabs paint
				// their backing control over their child pages in that printing path).
				bitmap, err := controlScreenshot(g.window)
				if err != nil {
					t.Error(err)
				} else {
					defer bitmap.Dispose()
					img, err := bitmap.ToImage()
					if err != nil {
						t.Error(err)
					} else {
						// GDI screenshots are opaque; GetDIBits leaves alpha undefined.
						for i := 3; i < len(img.Pix); i += 4 {
							img.Pix[i] = 255
						}
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

func controlScreenshot(window *walk.MainWindow) (*walk.Bitmap, error) {
	bounds := window.BoundsPixels()
	bitmap, err := walk.NewBitmapForDPI(walk.Size{Width: bounds.Width, Height: bounds.Height}, window.DPI())
	if err != nil {
		return nil, err
	}
	canvas, err := walk.NewCanvasFromImage(bitmap)
	if err != nil {
		bitmap.Dispose()
		return nil, err
	}
	screen := win.GetDC(0)
	ok := win.BitBlt(canvas.HDC(), 0, 0, int32(bounds.Width), int32(bounds.Height), screen, int32(bounds.X), int32(bounds.Y), win.SRCCOPY)
	win.ReleaseDC(0, screen)
	canvas.Dispose()
	if !ok {
		bitmap.Dispose()
		return nil, fmt.Errorf("cannot capture native window")
	}
	return bitmap, nil
}
