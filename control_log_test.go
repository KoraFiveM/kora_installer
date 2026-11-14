package main

import (
	"strings"
	"testing"
)

func TestColoredLogFilter(t *testing.T) {
	spans := coloredLogSpans("\x1b[31mERROR red\x1b[0m\nnormal\n\x1b[38;2;1;2;3mERROR rgb\x1b[2J", "error")
	var text string
	var red, rgb bool
	for _, s := range spans {
		text += s.Text
		red = red || s.Color == logPalette[1]
		rgb = rgb || s.Color == 0x010203
	}
	if strings.Contains(text, "normal") || strings.Contains(text, "\x1b") || !red || !rgb {
		t.Fatalf("bad log spans: %#v", spans)
	}
}
