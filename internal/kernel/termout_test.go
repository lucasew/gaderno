package kernel

import (
	"strings"
	"testing"
)

func TestFilterTerminal_plain(t *testing.T) {
	got := FilterTerminal("hello\nworld")
	if got != "hello\nworld" {
		t.Fatalf("got %q", got)
	}
}

func TestFilterTerminal_stripSGR(t *testing.T) {
	raw := "\x1b[31merror\x1b[0m: boom"
	got := FilterTerminal(raw)
	if got != "error: boom" {
		t.Fatalf("got %q", got)
	}
}

func TestFilterTerminal_carriageReturnProgress(t *testing.T) {
	// classic single-line progress: rewrite with CR
	raw := "downloading 10%\rdownloading 50%\rdownloading 100%\ndone\n"
	got := FilterTerminal(raw)
	if !strings.Contains(got, "downloading 100%") {
		t.Fatalf("missing final progress: %q", got)
	}
	if strings.Contains(got, "10%") || strings.Contains(got, "50%") {
		t.Fatalf("stale progress frames kept: %q", got)
	}
	if !strings.Contains(got, "done") {
		t.Fatalf("missing done: %q", got)
	}
}

func TestFilterTerminal_eraseLineAndSpinner(t *testing.T) {
	// uv-like: erase line then redraw spinner status
	raw := "\x1b[2K\x1b[37m⠋\x1b[0m \x1b[2mResolving...\x1b[0m" +
		"\r\x1b[2K\x1b[37m⠙\x1b[0m \x1b[2mResolving...\x1b[0m" +
		"\r\x1b[2K\x1b[37m⠹\x1b[0m \x1b[2mResolved 1 package\x1b[0m\n"
	got := FilterTerminal(raw)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("escape left behind: %q", got)
	}
	if !strings.Contains(got, "Resolved 1 package") {
		t.Fatalf("missing final status: %q", got)
	}
	// only one status line expected
	if strings.Count(got, "Resolving") > 0 && strings.Contains(got, "Resolved") {
		// resolving frames should have been overwritten
		if strings.Contains(got, "Resolving") {
			t.Fatalf("spinner frames not collapsed: %q", got)
		}
	}
}

func TestFilterTerminal_cursorUpMultiline(t *testing.T) {
	// two-line progress rewritten with cursor-up
	raw := "   Building numpy==2.5.1\n" +
		"\x1b[37m⠋\x1b[0m Preparing packages... (0/1)\n" +
		"\x1b[1A\x1b[2K\x1b[1A\x1b[2K" +
		"   Building numpy==2.5.1\n" +
		"\x1b[37m⠙\x1b[0m Preparing packages... (0/1)\n" +
		"\x1b[1A\x1b[2K\x1b[1A\x1b[2K" +
		"   Building numpy==2.5.1\n" +
		"\x1b[37m⠹\x1b[0m Preparing packages... (1/1)\n"
	got := FilterTerminal(raw)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("escape left behind: %q", got)
	}
	if strings.Count(got, "Building numpy") != 1 {
		t.Fatalf("expected single Building line, got %q", got)
	}
	if !strings.Contains(got, "(1/1)") {
		t.Fatalf("missing final prepare line: %q", got)
	}
	if strings.Contains(got, "(0/1)") {
		t.Fatalf("stale prepare frame kept: %q", got)
	}
}

func TestFilterTerminal_privateModesAndQueries(t *testing.T) {
	raw := "\x1b[?2026$p\x1b[?2027$p\x1b[?25l\x1b[?2004hhello\x1b[?25h\x1b[?2004l"
	got := FilterTerminal(raw)
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestFilterTerminal_streamingChunks(t *testing.T) {
	var f TermFilter
	f.Write("\x1b[2K\x1b[37m⠋\x1b[0m Resolving...\r")
	f.Write("\x1b[2K\x1b[37m⠙\x1b[0m Resolving...\r")
	f.Write("\x1b[2KResolved 1 package\n")
	got := f.String()
	if got != "Resolved 1 package\n" && got != "Resolved 1 package" {
		// allow trailing newline from our newline handling
		if !strings.HasPrefix(strings.TrimRight(got, "\n"), "Resolved 1 package") || strings.Contains(got, "Resolving") {
			t.Fatalf("got %q", got)
		}
	}
}

func TestFilterTerminal_oscHyperlink(t *testing.T) {
	raw := "\x1b]8;;https://example.com\x07click\x1b]8;;\x07 me"
	got := FilterTerminal(raw)
	if got != "click me" {
		t.Fatalf("got %q", got)
	}
}
