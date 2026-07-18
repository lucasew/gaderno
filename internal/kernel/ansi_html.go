package kernel

import (
	"html"
	"strconv"
	"strings"
)

// ANSIToHTML converts terminal text with SGR color/style sequences into safe
// HTML. All literal text is html-escaped; only <span class="…"> wrappers are
// emitted. Unknown/control sequences are dropped (same idea as FilterTerminal).
func ANSIToHTML(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s) + 64)
	b.WriteString(`<pre class="ansi">`)

	// active classes (order stable for nicer markup)
	bold := false
	underline := false
	fg := "" // class suffix e.g. "red", "bright-cyan", "fg-38-5-196" simplified

	open := false
	writeOpen := func() {
		if open {
			return
		}
		var classes []string
		if bold {
			classes = append(classes, "ansi-bold")
		}
		if underline {
			classes = append(classes, "ansi-underline")
		}
		if fg != "" {
			classes = append(classes, "ansi-fg-"+fg)
		}
		if len(classes) == 0 {
			return
		}
		b.WriteString(`<span class="`)
		b.WriteString(strings.Join(classes, " "))
		b.WriteString(`">`)
		open = true
	}
	writeClose := func() {
		if open {
			b.WriteString("</span>")
			open = false
		}
	}
	setStyle := func() {
		writeClose()
		writeOpen()
	}

	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// CSI … final
			j := i + 2
			for j < len(s) {
				c := s[j]
				if c >= 0x40 && c <= 0x7e {
					params := s[i+2 : j]
					final := c
					j++
					if final == 'm' {
						applySGR(params, &bold, &underline, &fg)
						setStyle()
					}
					// drop other CSI (cursor etc.) for HTML path
					i = j
					break
				}
				j++
			}
			if j >= len(s) {
				break
			}
			continue
		}
		if s[i] == 0x1b {
			// other ESC — skip 1–2 bytes
			if i+1 < len(s) {
				i += 2
			} else {
				i++
			}
			continue
		}
		if s[i] == 0x9b {
			// C1 CSI
			j := i + 1
			for j < len(s) {
				c := s[j]
				if c >= 0x40 && c <= 0x7e {
					params := s[i+1 : j]
					final := c
					j++
					if final == 'm' {
						applySGR(params, &bold, &underline, &fg)
						setStyle()
					}
					i = j
					break
				}
				j++
			}
			continue
		}
		// gather a run of plain text
		start := i
		for i < len(s) && s[i] != 0x1b && s[i] != 0x9b {
			i++
		}
		chunk := s[start:i]
		if chunk != "" {
			writeOpen()
			b.WriteString(html.EscapeString(chunk))
		}
	}
	writeClose()
	b.WriteString("</pre>")
	return b.String()
}

func applySGR(params string, bold, underline *bool, fg *string) {
	if params == "" {
		params = "0"
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			continue
		}
		switch {
		case n == 0:
			*bold = false
			*underline = false
			*fg = ""
		case n == 1:
			*bold = true
		case n == 4:
			*underline = true
		case n == 22:
			*bold = false
		case n == 24:
			*underline = false
		case n == 39:
			*fg = ""
		case n >= 30 && n <= 37:
			*fg = basicFG(n - 30)
		case n >= 90 && n <= 97:
			*fg = "bright-" + basicFG(n-90)
		case n == 38:
			// 38;5;n or 38;2;r;g;b — map 256-color roughly; truecolor ignored → default
			if i+1 < len(parts) {
				mode, _ := strconv.Atoi(strings.TrimSpace(parts[i+1]))
				if mode == 5 && i+2 < len(parts) {
					idx, _ := strconv.Atoi(strings.TrimSpace(parts[i+2]))
					*fg = xterm256(idx)
					i += 2
				} else if mode == 2 && i+4 < len(parts) {
					// skip truecolor; leave as default for safety
					i += 4
					*fg = ""
				} else {
					i++
				}
			}
		case n == 48:
			// background — ignore for inspect readability
			if i+1 < len(parts) {
				mode, _ := strconv.Atoi(strings.TrimSpace(parts[i+1]))
				if mode == 5 && i+2 < len(parts) {
					i += 2
				} else if mode == 2 && i+4 < len(parts) {
					i += 4
				} else {
					i++
				}
			}
		}
	}
}

func basicFG(n int) string {
	switch n {
	case 0:
		return "black"
	case 1:
		return "red"
	case 2:
		return "green"
	case 3:
		return "yellow"
	case 4:
		return "blue"
	case 5:
		return "magenta"
	case 6:
		return "cyan"
	case 7:
		return "white"
	default:
		return ""
	}
}

// Map common xterm-256 indices used by IPython to named classes.
func xterm256(idx int) string {
	if idx < 0 {
		return ""
	}
	if idx < 8 {
		return basicFG(idx)
	}
	if idx < 16 {
		return "bright-" + basicFG(idx-8)
	}
	// cube / grayscale — approximate with nearest basic
	if idx >= 232 {
		if idx >= 244 {
			return "white"
		}
		return "black"
	}
	// 16..231 color cube
	c := idx - 16
	r := c / 36
	g := (c % 36) / 6
	b := c % 6
	// pick dominant channel
	if r >= g && r >= b && r > 0 {
		if r >= 4 {
			return "bright-red"
		}
		return "red"
	}
	if g >= r && g >= b && g > 0 {
		if g >= 4 {
			return "bright-green"
		}
		return "green"
	}
	if b > 0 {
		if b >= 4 {
			return "bright-blue"
		}
		return "blue"
	}
	return "white"
}
