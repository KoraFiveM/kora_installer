//go:build windows && gui

package main

import (
	"fmt"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var richEditLibrary = windows.NewLazySystemDLL("Msftedit.dll")

type richLog struct{ walk.WidgetBase }

func newRichLog(parent walk.Container) (*richLog, error) {
	if err := richEditLibrary.Load(); err != nil {
		return nil, err
	}
	r := new(richLog)
	if err := walk.InitWidget(r, parent, "RICHEDIT50W", win.WS_VISIBLE|win.WS_TABSTOP|win.WS_VSCROLL|win.ES_MULTILINE|win.ES_READONLY, 0); err != nil {
		return nil, err
	}
	r.SendMessage(win.EM_SETBKGNDCOLOR, 0, uintptr(walk.RGB(24, 28, 25)))

	if font, err := walk.NewFont("Consolas", 10, 0); err == nil {
		r.SetFont(font)
		r.Disposing().Attach(font.Dispose)
	}
	r.SendMessage(win.EM_EXLIMITTEXT, 0, 1024*1024)
	return r, nil
}
func (r *richLog) CreateLayoutItem(*walk.LayoutContext) walk.LayoutItem {
	return walk.NewGreedyLayoutItem()
}
func (r *richLog) SetText(text string) error { r.Render(text, ""); return nil }
func (r *richLog) Render(text, filter string) {
	// Preserve selection/scroll when inspecting older lines; follow only at bottom.
	var scroll win.SCROLLINFO
	scroll.CbSize = uint32(unsafe.Sizeof(scroll))
	scroll.FMask = win.SIF_ALL
	win.GetScrollInfo(r.Handle(), win.SB_VERT, &scroll)
	follow := scroll.NPos+int32(scroll.NPage) >= scroll.NMax
	var selection win.CHARRANGE
	r.SendMessage(win.EM_EXGETSEL, 0, uintptr(unsafe.Pointer(&selection)))
	var point win.POINT
	r.SendMessage(win.EM_GETSCROLLPOS, 0, uintptr(unsafe.Pointer(&point)))
	spans := coloredLogSpans(text, filter)
	for i := range spans {
		spans[i].Text = strings.ReplaceAll(spans[i].Text, "\r\n", "\r")
	}
	plain := ""
	for _, span := range spans {
		plain += span.Text
	}
	r.SendMessage(win.WM_SETREDRAW, 0, 0)
	p, err := syscall.UTF16PtrFromString(plain)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString(fmt.Sprintf("Unable to display log: %v", err))
	}
	r.SendMessage(win.WM_SETTEXT, 0, uintptr(unsafe.Pointer(p)))
	offset := 0
	for _, span := range spans {
		length := len(utf16.Encode([]rune(span.Text)))
		selection := win.CHARRANGE{CpMin: int32(offset), CpMax: int32(offset + length)}
		r.SendMessage(win.EM_EXSETSEL, 0, uintptr(unsafe.Pointer(&selection)))
		format := win.CHARFORMAT{DwMask: win.CFM_COLOR, CrTextColor: win.COLORREF(walk.RGB(byte(span.Color>>16), byte(span.Color>>8), byte(span.Color)))}
		format.CbSize = uint32(unsafe.Sizeof(format))
		r.SendMessage(win.EM_SETCHARFORMAT, win.SCF_SELECTION, uintptr(unsafe.Pointer(&format)))
		offset += length
	}
	if follow {
		selection = win.CHARRANGE{CpMin: int32(offset), CpMax: int32(offset)}
	}
	r.SendMessage(win.EM_EXSETSEL, 0, uintptr(unsafe.Pointer(&selection)))
	if follow {
		r.SendMessage(win.EM_SCROLLCARET, 0, 0)
	} else {
		r.SendMessage(win.EM_SETSCROLLPOS, 0, uintptr(unsafe.Pointer(&point)))
	}
	r.SendMessage(win.WM_SETREDRAW, 1, 0)
	r.Invalidate()
}

// Native Windows themes retain keyboard, focus and accessibility behavior.
func themeControl(hwnd win.HWND) {
	theme, _ := syscall.UTF16PtrFromString("DarkMode_Explorer")
	win.SetWindowTheme(hwnd, theme, nil)
}

var controlThemeCallback = syscall.NewCallback(func(hwnd win.HWND, _ uintptr) uintptr {
	var name [64]uint16
	if _, err := win.GetClassName(hwnd, &name[0], len(name)); err == nil {
		class := syscall.UTF16ToString(name[:])
		if class == "Button" || class == "ComboBox" {
			themeControl(hwnd)
		}
	}
	return 1
})
