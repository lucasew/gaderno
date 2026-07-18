package kernel

import (
	"strings"
	"testing"
)

func TestParseInspectReply(t *testing.T) {
	res := parseInspectReply(map[string]any{
		"status": "ok",
		"found":  true,
		"data": map[string]any{
			"text/plain": "Signature: print(value, ..., sep=' ')\nDocstring:\nPrint objects",
		},
	}, 0)
	if !res.Found || res.Text == "" {
		t.Fatalf("%#v", res)
	}
	// ANSI color labels from ipykernel → plain text + colored HTML.
	ansi := parseInspectReply(map[string]any{
		"status": "ok",
		"found":  true,
		"data": map[string]any{
			"text/plain": "\x1b[31mSignature:\x1b[39m os.chdir(path)\n\x1b[31mDocstring:\x1b[39m\nChange cwd",
		},
	}, 1)
	if strings.Contains(ansi.Text, "\x1b") || strings.Contains(ansi.Text, "[31m") {
		t.Fatalf("ANSI not stripped from text: %q", ansi.Text)
	}
	if !strings.Contains(ansi.Text, "Signature:") || !strings.Contains(ansi.Text, "os.chdir") {
		t.Fatalf("lost content: %q", ansi.Text)
	}
	if ansi.HTML == "" || !strings.Contains(ansi.HTML, "ansi-fg-red") {
		t.Fatalf("expected colored HTML: %q", ansi.HTML)
	}
	empty := parseInspectReply(nil, 1)
	if empty.Found || empty.DetailLevel != 1 {
		t.Fatalf("%#v", empty)
	}
}
