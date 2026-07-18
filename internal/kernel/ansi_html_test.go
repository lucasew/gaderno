package kernel

import (
	"strings"
	"testing"
)

func TestANSIToHTML_stripsAndColors(t *testing.T) {
	in := "\x1b[31mSignature:\x1b[39m os.chdir(path)\n\x1b[31mDocstring:\x1b[39m\nChange cwd"
	out := ANSIToHTML(in)
	if strings.Contains(out, "\x1b") || strings.Contains(out, "[31m") {
		t.Fatalf("raw ANSI leaked: %q", out)
	}
	if !strings.Contains(out, `class="ansi-fg-red"`) {
		t.Fatalf("expected red span: %s", out)
	}
	if !strings.Contains(out, "Signature:") || !strings.Contains(out, "os.chdir") {
		t.Fatalf("lost text: %s", out)
	}
	if !strings.Contains(out, "os.chdir(path)") {
		t.Fatalf("body missing: %s", out)
	}
	// XSS: escape
	evil := ANSIToHTML(`<script>alert(1)</script>`)
	if strings.Contains(evil, "<script>") {
		t.Fatalf("not escaped: %s", evil)
	}
	if !strings.Contains(evil, "&lt;script&gt;") {
		t.Fatalf("expected escaped: %s", evil)
	}
}
