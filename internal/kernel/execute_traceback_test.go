package kernel

import (
	"strings"
	"testing"
)

func TestTracebackFromContentStripsANSI(t *testing.T) {
	// Typical ipykernel error frame with SGR color sequences.
	raw := "\x1b[0;31mValueError\x1b[0m: boom"
	tb := tracebackFromContent(map[string]any{
		"traceback": []any{
			"\x1b[1;31m---------------------------------------------------------------------------\x1b[0m",
			raw,
			"    \x1b[0;32m1\x1b[0m raise ValueError(\"boom\")",
		},
	})
	if len(tb) != 3 {
		t.Fatalf("len=%d %#v", len(tb), tb)
	}
	for i, line := range tb {
		if strings.Contains(line, "\x1b") {
			t.Fatalf("line %d still has ESC: %q", i, line)
		}
	}
	if !strings.Contains(tb[1], "ValueError") || !strings.Contains(tb[1], "boom") {
		t.Fatalf("cleaned frame: %q", tb[1])
	}
}

func TestTracebackFromContentStringSlice(t *testing.T) {
	tb := tracebackFromContent(map[string]any{
		"traceback": []string{"a", "b"},
	})
	if len(tb) != 2 || tb[0] != "a" || tb[1] != "b" {
		t.Fatalf("%#v", tb)
	}
}

func TestTracebackFromContentMissing(t *testing.T) {
	if tb := tracebackFromContent(nil); tb != nil {
		t.Fatalf("%#v", tb)
	}
	if tb := tracebackFromContent(map[string]any{}); tb != nil {
		t.Fatalf("%#v", tb)
	}
}

func TestApplyErrorContent(t *testing.T) {
	var res ExecuteResult
	applyErrorContent(&res, map[string]any{
		"ename":  "\x1b[31mTypeError\x1b[0m",
		"evalue": "bad",
		"traceback": []any{
			"Traceback (most recent call last):",
			"TypeError: bad",
		},
	})
	if res.Ename != "TypeError" {
		t.Fatalf("ename %q", res.Ename)
	}
	if res.Evalue != "bad" {
		t.Fatalf("evalue %q", res.Evalue)
	}
	if len(res.Traceback) != 2 {
		t.Fatalf("traceback %#v", res.Traceback)
	}
}
