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
	"unsafe"

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

	g.serverInfo.Render("\x1b[31mERROR first\x1b[0m\n\x1b[32mready", "error")
	var selection = win.CHARRANGE{CpMin: 0, CpMax: 1}
	g.serverInfo.SendMessage(win.EM_EXSETSEL, 0, uintptr(unsafe.Pointer(&selection)))
	var format win.CHARFORMAT
	format.CbSize = uint32(unsafe.Sizeof(format))
	g.serverInfo.SendMessage(win.EM_GETCHARFORMAT, win.SCF_SELECTION, uintptr(unsafe.Pointer(&format)))
	if format.CrTextColor != win.COLORREF(walk.RGB(255, 112, 112)) {
		t.Fatalf("log colour not applied: %x", format.CrTextColor)
	}
	if got := g.serverInfo.SendMessage(win.WM_GETTEXTLENGTH, 0, 0); got != 13 {
		t.Fatalf("filter or CRLF text export failed: %d", got)
	}
	// RichEdit selection offsets count its internal CR, whereas WM_GETTEXTLENGTH
	// includes CRLF for compatibility with the standard Windows edit control.
	g.serverInfo.Render("\x1b[31mERROR first\x1b[0m\n\x1b[32mready", "")
	selection = win.CHARRANGE{CpMin: 12, CpMax: 13}
	g.serverInfo.SendMessage(win.EM_EXSETSEL, 0, uintptr(unsafe.Pointer(&selection)))
	g.serverInfo.SendMessage(win.EM_GETCHARFORMAT, win.SCF_SELECTION, uintptr(unsafe.Pointer(&format)))
	if format.CrTextColor != win.COLORREF(walk.RGB(149, 255, 0)) {
		t.Fatalf("second-line colour offset incorrect: %x", format.CrTextColor)
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
			area, ok := controlWorkArea(g.window)
			if ok {
				bounds := g.window.BoundsPixels()
				if bounds.Y+bounds.Height > area.Y+area.Height {
					t.Errorf("window exceeds work area: %+v vs %+v", bounds, area)
				}
			}
			if !win.IsWindowVisible(g.edit("license").Handle()) {
				t.Error("license input is not visible in Windows")
			}
			if b.Width < 100 || b.Height < 10 {
				t.Errorf("license input has invalid layout: %+v", b)
			}
			if target := os.Getenv("KORA_GUI_SCREENSHOT"); target != "" {
				saveControlScreenshot(t, g.window, target)
			}
		})
		if target := os.Getenv("KORA_GUI_SCREENSHOT"); target != "" {
			scenes := []struct {
				name  string
				setup func()
			}{
				{"database", func() { g.show(2); g.combo("db-setup").SetCurrentIndex(0); g.service.SetChecked(true) }},
				{"review", func() {
					g.show(4)
					preview := c
					preview.Output = `C:\KoraServers`
					preview.DirectoryName = "kora-demo"
					preview.Hostname = "Kora demo server"
					preview.GameBuild = "3095 (example)"
					preview.CFXPort = 30120
					preview.DBPort = 3306
					preview.TXAdmin = true
					preview.TXPort = 40120
					preview.WindowsService = true
					preview.DBSetup = "new-service"
					g.review.SetText(guiReview(Plan{Config: preview, ArtifactURL: "Recommended Windows artifact (simulated)", Resources: []Resource{
						{Path: "[kora]/kora-core", Ref: "example-tag"},
						{Path: "[kora]/kora-crypto", Ref: "example-tag"},
					}}))
				}},
				{"servers", func() {
					g.tabs.SetCurrentIndex(1)
					// Populate only the display model: no real server selection,
					// filesystem discovery, log watcher or process is started.
					g.serversBox.SetModel([]string{"kora-demo — Native service running (simulated)", "kora-dev — Native process stopped (simulated)"})
					g.serversBox.SetCurrentIndex(0)
					g.watchedText = "[Kora Control] 14:30:00 start requested\n\x1b[36m[FXServer] Preparing server session\x1b[0m\n\x1b[32m[database] Connected to MariaDB\x1b[0m\n\x1b[32m[kora-core] Resource started\x1b[0m\n\x1b[32m[kora-crypto] Resource started\x1b[0m\n\x1b[33m[demo] Example warning for colour preview\x1b[0m\n\x1b[31m[demo] Example error for colour preview\x1b[0m\n\x1b[36m[FXServer] Listening on port 30120 TCP + UDP\x1b[0m\n[Kora Control] 14:30:05 start completed"
					g.renderWatchedLogs()
				}},
				{"filtered-logs", func() { g.logFilter.SetText("kora") }},
			}
			for _, scene := range scenes {
				g.window.Synchronize(scene.setup)
				time.Sleep(500 * time.Millisecond)
				name := scene.name
				g.window.Synchronize(func() {
					saveControlScreenshot(t, g.window, filepath.Join(filepath.Dir(target), "kora-control-"+name+".png"))
				})
			}
		}
		g.window.Synchronize(func() { g.window.Close() })
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

// Screenshot the real desktop surface after the UI thread has painted the scene.
// Synthetic fixture values are labelled on every screen.
func saveControlScreenshot(t *testing.T, window *walk.MainWindow, target string) {
	t.Helper()
	// Page changes invalidate nested native controls independently. Flush the
	// entire window tree before reading desktop pixels.
	win.RedrawWindow(window.Handle(), nil, 0, win.RDW_INVALIDATE|win.RDW_ERASE|win.RDW_ALLCHILDREN|win.RDW_UPDATENOW|win.RDW_FRAME)
	bitmap, err := controlScreenshot(window)
	if err != nil {
		t.Error(err)
		return
	}
	defer bitmap.Dispose()
	img, err := bitmap.ToImage()
	if err != nil {
		t.Error(err)
		return
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Error(err)
		return
	}
	f, err := os.Create(target)
	if err != nil {
		t.Error(err)
		return
	}
	if err := png.Encode(f, img); err != nil {
		t.Error(err)
	}
	if err := f.Close(); err != nil {
		t.Error(err)
	}
}
