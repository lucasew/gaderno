package kernel

import (
	"context"
	"fmt"
	"time"
)

// CompleteResult is the Jupyter complete_reply payload we care about.
type CompleteResult struct {
	Matches     []string `json:"matches"`
	CursorStart int      `json:"cursor_start"`
	CursorEnd   int      `json:"cursor_end"`
	Status      string   `json:"status"`
}

// Complete asks the kernel for completions at cursorPos (byte/UTF-8 offset in code).
// Returns empty matches (not error) when the shell is busy with execute.
func (m *Manager) Complete(ctx context.Context, code string, cursorPos int) (CompleteResult, error) {
	if m.Conn == nil {
		return CompleteResult{}, fmt.Errorf("no connection")
	}
	if cursorPos < 0 {
		cursorPos = 0
	}
	if cursorPos > len(code) {
		cursorPos = len(code)
	}

	// Do not block execute; autocomplete is best-effort.
	if !m.shellMu.TryLock() {
		return CompleteResult{Status: "busy", CursorStart: cursorPos, CursorEnd: cursorPos}, nil
	}
	defer m.shellMu.Unlock()

	req := Message{
		Header: NewHeader(m.Session, "complete_request"),
		Content: map[string]any{
			"code":       code,
			"cursor_pos": cursorPos,
		},
	}
	msgID := req.Header.MsgID
	if err := m.Conn.SendShell(req); err != nil {
		return CompleteResult{}, err
	}

	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	for {
		if err := ctx.Err(); err != nil {
			return CompleteResult{}, err
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			return CompleteResult{}, fmt.Errorf("complete timeout")
		}
		rctx, cancel := context.WithTimeout(ctx, remain)
		msg, ch, err := m.Conn.recvEither(rctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return CompleteResult{}, ctx.Err()
			}
			if time.Now().After(deadline) {
				return CompleteResult{}, fmt.Errorf("complete timeout")
			}
			continue
		}
		// Ignore IOPub noise; only shell complete_reply for our parent id.
		if ch != "shell" {
			continue
		}
		if msg.Header.MsgType != "complete_reply" {
			continue
		}
		if msg.ParentHeader.MsgID != msgID {
			continue
		}
		return parseCompleteReply(msg.Content, cursorPos), nil
	}
}

func parseCompleteReply(content map[string]any, fallbackPos int) CompleteResult {
	res := CompleteResult{
		Status:      "ok",
		CursorStart: fallbackPos,
		CursorEnd:   fallbackPos,
		Matches:     nil,
	}
	if content == nil {
		res.Matches = []string{}
		return res
	}
	if s, ok := content["status"].(string); ok && s != "" {
		res.Status = s
	}
	if n, ok := asInt(content["cursor_start"]); ok {
		res.CursorStart = n
	}
	if n, ok := asInt(content["cursor_end"]); ok {
		res.CursorEnd = n
	}
	switch m := content["matches"].(type) {
	case []any:
		for _, x := range m {
			if s, ok := x.(string); ok && s != "" {
				res.Matches = append(res.Matches, s)
			}
		}
	case []string:
		res.Matches = append(res.Matches, m...)
	}
	if res.Matches == nil {
		res.Matches = []string{}
	}
	return res
}
