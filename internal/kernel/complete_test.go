package kernel

import "testing"

func TestParseCompleteReply(t *testing.T) {
	res := parseCompleteReply(map[string]any{
		"status":       "ok",
		"matches":      []any{"os.path", "os.environ"},
		"cursor_start": 3.0,
		"cursor_end":   5.0,
	}, 5)
	if res.Status != "ok" {
		t.Fatalf("status %q", res.Status)
	}
	if res.CursorStart != 3 || res.CursorEnd != 5 {
		t.Fatalf("cursors %d %d", res.CursorStart, res.CursorEnd)
	}
	if len(res.Matches) != 2 || res.Matches[0] != "os.path" {
		t.Fatalf("matches %#v", res.Matches)
	}
	empty := parseCompleteReply(nil, 1)
	if empty.CursorStart != 1 || empty.Matches == nil {
		t.Fatalf("empty %#v", empty)
	}
}
