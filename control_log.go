package main

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type logSpan struct {
	Text  string
	Color uint32
}

var sgrPattern = regexp.MustCompile(`\x1b\[([0-9;]*)m`)
var logPalette = []uint32{0x8b9388, 0xff7070, 0x95ff00, 0xffd166, 0x7db5ff, 0xd89bff, 0x71e6d1, 0xebf0e6, 0xa6afa3, 0xff9696, 0xb7ff65, 0xffe39b, 0xa3ccff, 0xe6bbff, 0xa1f2e4, 0xffffff}

// Only SGR foreground colours reach RichEdit. Strip all other terminal controls.
func coloredLogSpans(text, filter string) []logSpan {
	var result []logSpan
	color := uint32(0xebf0e6)
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		include := strings.Contains(strings.ToLower(ansi.Strip(line)), strings.ToLower(filter))
		add := func(s string) {
			if include {
				if s = ansi.Strip(s); s != "" {
					result = append(result, logSpan{s, color})
				}
			}
		}
		last := 0
		for _, m := range sgrPattern.FindAllStringSubmatchIndex(line, -1) {
			add(line[last:m[0]])
			params := strings.Split(line[m[2]:m[3]], ";")
			for i := 0; i < len(params); i++ {
				n, _ := strconv.Atoi(params[i])
				switch {
				case n == 0 || n == 39:
					color = 0xebf0e6
				case n >= 30 && n <= 37:
					color = logPalette[n-30]
				case n >= 90 && n <= 97:
					color = logPalette[n-90+8]
				case n == 38 && i+4 < len(params) && params[i+1] == "2":
					r, _ := strconv.Atoi(params[i+2])
					g, _ := strconv.Atoi(params[i+3])
					b, _ := strconv.Atoi(params[i+4])
					if r >= 0 && r < 256 && g >= 0 && g < 256 && b >= 0 && b < 256 {
						color = uint32(r<<16 | g<<8 | b)
					}
					i += 4
				case n == 48 && i+4 < len(params) && params[i+1] == "2":
					i += 4
				case (n == 38 || n == 48) && i+2 < len(params) && params[i+1] == "5":
					v, _ := strconv.Atoi(params[i+2])
					if n == 38 && v >= 0 && v < 256 {
						if v < 16 {
							color = logPalette[v]
						} else if v >= 232 {
							c := uint32(8 + (v-232)*10)
							color = c<<16 | c<<8 | c
						} else {
							v -= 16
							levels := []uint32{0, 95, 135, 175, 215, 255}
							color = levels[v/36]<<16 | levels[v/6%6]<<8 | levels[v%6]
						}
					}
					i += 2
				}
			}
			last = m[1]
		}
		add(line[last:])
		add("\r\n")
	}
	return result
}
