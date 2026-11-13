package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestLicenseInputFramesStayAligned(t *testing.T) {
	m := newModel(validConfig(t), map[string]bool{})
	for i, f := range fields {
		if f.Key == "license-key" {
			m.step = i
		}
	}
	m.loadField()
	for _, secret := range []string{"", strings.Repeat("a", 90), strings.Repeat("密", 50)} {
		m.input.SetValue(secret)
		m.input.CursorEnd()
		for _, width := range []int{120, 80, 60, 40, 80} {
			next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 50})
			m = next.(model)
			view := ansi.Strip(m.View().Content)
			if secret != "" && strings.Contains(view, secret) {
				t.Fatal("license leaked")
			}
			var left, right int
			inside := false
			rows := 0
			boxes := 0
			for _, line := range strings.Split(view, "\n") {
				runes := []rune(line)
				var corners []int
				for x, r := range runes {
					if r == '╭' || r == '╮' {
						corners = append(corners, x)
					}
				}
				if len(corners) == 2 {
					if !inside && boxes == 0 {
						boxes++
						continue
					} // outer frame
					left, right = corners[0], corners[1]
					inside = true
					rows = 0
					continue
				}
				if !inside {
					continue
				}
				if len(runes) <= right {
					t.Fatalf("clipped input frame at width %d", width)
				}
				if runes[left] == '╰' {
					if runes[right] != '╯' {
						t.Fatalf("misaligned bottom at width %d", width)
					}
					if rows != 3 {
						t.Fatalf("input wrapped into %d rows at width %d\n%s", rows, width, view)
					}
					inside = false
					boxes++
					continue
				}
				if runes[left] != '│' || runes[right] != '│' {
					t.Fatalf("misaligned input sides at width %d: %q", width, line)
				}
				rows++
			}
			if boxes != 2 || inside {
				t.Fatalf("missing frame at width %d", width)
			}
		}
	}
}
