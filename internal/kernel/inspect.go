package kernel

import (
	"context"
	"fmt"
	"time"
)

// InspectResult is the Jupyter inspect_reply payload we surface to the UI.
type InspectResult struct {
	Status string `json:"status"`
	Found  bool   `json:"found"`
	// Text is plain text (ANSI stripped) from text/plain.
	Text string `json:"text"`
	// HTML is safe colored markup (ANSI → classed spans, content escaped).
	HTML string `json:"html"`
	// DetailLevel echoes the request (0 ≈ signature, 1 ≈ full docs).
	DetailLevel int `json:"detail_level"`
}

// Inspect asks the kernel for docs/signature at cursorPos.
// detailLevel: 0 abbreviated, 1 full (Jupyter protocol).
// Best-effort: returns Found=false when shell is busy with execute.
func (m *Manager) Inspect(ctx context.Context, code string, cursorPos, detailLevel int) (InspectResult, error) {
	if m.Conn == nil {
		return InspectResult{}, fmt.Errorf("no connection")
	}
	if cursorPos < 0 {
		cursorPos = 0
	}
	if cursorPos > len(code) {
		cursorPos = len(code)
	}
	if detailLevel < 0 {
		detailLevel = 0
	}
	if detailLevel > 1 {
		detailLevel = 1
	}

	if !m.shellMu.TryLock() {
		return InspectResult{Status: "busy", Found: false, DetailLevel: detailLevel}, nil
	}
	defer m.shellMu.Unlock()

	req := Message{
		Header: NewHeader(m.Session, "inspect_request"),
		Content: map[string]any{
			"code":         code,
			"cursor_pos":   cursorPos,
			"detail_level": detailLevel,
		},
	}
	msgID := req.Header.MsgID
	if err := m.Conn.SendShell(req); err != nil {
		return InspectResult{}, err
	}

	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	for {
		if err := ctx.Err(); err != nil {
			return InspectResult{}, err
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			return InspectResult{}, fmt.Errorf("inspect timeout")
		}
		rctx, cancel := context.WithTimeout(ctx, remain)
		msg, ch, err := m.Conn.recvEither(rctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return InspectResult{}, ctx.Err()
			}
			if time.Now().After(deadline) {
				return InspectResult{}, fmt.Errorf("inspect timeout")
			}
			continue
		}
		if ch != "shell" {
			continue
		}
		if msg.Header.MsgType != "inspect_reply" {
			continue
		}
		if msg.ParentHeader.MsgID != msgID {
			continue
		}
		return parseInspectReply(msg.Content, detailLevel), nil
	}
}

func parseInspectReply(content map[string]any, detailLevel int) InspectResult {
	res := InspectResult{
		Status:      "ok",
		Found:       false,
		DetailLevel: detailLevel,
	}
	if content == nil {
		return res
	}
	if s, ok := content["status"].(string); ok && s != "" {
		res.Status = s
	}
	switch f := content["found"].(type) {
	case bool:
		res.Found = f
	case float64:
		res.Found = f != 0
	}
	var rawPlain string
	if data, ok := content["data"].(map[string]any); ok {
		// Prefer text/plain (often ANSI-colored by ipykernel).
		if tp, ok := data["text/plain"]; ok {
			rawPlain = multilineContent(tp)
		} else if th, ok := data["text/html"]; ok {
			// No safe HTML sanitizer for arbitrary kernel HTML — plain-strip only.
			rawPlain = multilineContent(th)
		}
	}
	if rawPlain != "" {
		// Colored tooltip markup (escaped text + classed spans).
		res.HTML = ANSIToHTML(rawPlain)
		// Plain fallback for clients / signature-line extraction.
		res.Text = FilterTerminal(rawPlain)
	}
	if res.Text != "" {
		// Some kernels omit found=true but still send a body.
		res.Found = true
	}
	return res
}
