package kernel

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ExecuteResult is a simplified view of one execute call.
type ExecuteResult struct {
	MsgID          string `json:"msg_id"`
	Status         string `json:"status"` // ok | error | abort
	ExecutionCount int    `json:"execution_count"`
	Stdout         string `json:"stdout"`
	Stderr         string `json:"stderr"`
	Ename          string `json:"ename"`
	Evalue         string `json:"evalue"`
}

// StreamChunk is a stdout/stderr update during execute.
// Text is the full terminal-filtered stream so far (replace, do not append).
type StreamChunk struct {
	Name string // stdout | stderr
	Text string
}

// DisplayData is one display_data / execute_result mime bundle for the client.
// Data keys are mime types; values are JSON-friendly (strings, usually).
type DisplayData struct {
	OutputType string         `json:"output_type"` // display_data | execute_result
	Data       map[string]any `json:"data"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	// Transient is Jupyter transient metadata (e.g. display_id); optional.
	Transient map[string]any `json:"transient,omitempty"`
}

// MaxDisplayBytes is a soft per-mime payload cap (decoded character length).
// Oversized mimes are dropped and noted in text/plain.
const MaxDisplayBytes = 12 << 20 // 12 MiB

// ExecuteOpts configures execute behavior.
type ExecuteOpts struct {
	// OnStream is called for each IOPub stream chunk (may be nil).
	OnStream func(StreamChunk)
	// OnDisplay is called for each display_data / execute_result (may be nil).
	// The client decides rendering; server does not turn mimes into HTML here.
	OnDisplay func(DisplayData)
}

// Execute runs code on the kernel and collects IOPub until idle for this msg.
func (m *Manager) Execute(ctx context.Context, code string) (ExecuteResult, error) {
	return m.ExecuteOpts(ctx, code, ExecuteOpts{})
}

// ExecuteOpts runs code with optional stream callbacks.
func (m *Manager) ExecuteOpts(ctx context.Context, code string, opts ExecuteOpts) (ExecuteResult, error) {
	if m.Conn == nil {
		return ExecuteResult{}, fmt.Errorf("no connection")
	}
	// Hold for the whole execute so complete_request cannot interleave on shell.
	m.shellMu.Lock()
	defer m.shellMu.Unlock()

	req := Message{
		Header: NewHeader(m.Session, "execute_request"),
		Content: map[string]any{
			"code":             code,
			"silent":           false,
			"store_history":    true,
			"user_expressions": map[string]any{},
			"allow_stdin":      false,
			"stop_on_error":    true,
		},
	}
	msgID := req.Header.MsgID
	if err := m.Conn.SendShell(req); err != nil {
		return ExecuteResult{}, err
	}

	var res ExecuteResult
	res.MsgID = msgID
	deadline := time.Now().Add(120 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}

	// Stateful VT filters so progress spinners / CR rewrites collapse across chunks.
	var outTerm, errTerm TermFilter

	// When the caller's context cancels or we hit the execute deadline, send
	// interrupt_request on the control channel and drain until this execute
	// goes idle (or a short grace period). Leaving the kernel running after a
	// timeout holds shellMu free for the next Complete/Execute while the old
	// code still owns the kernel — interleaved shell replies and wasted CPU.
	const interruptGrace = 5 * time.Second
	var stopCause error
	interrupted := false

	for {
		if stopCause == nil {
			if err := ctx.Err(); err != nil {
				stopCause = err
			} else if time.Now().After(deadline) {
				stopCause = fmt.Errorf("execute timeout")
			}
		}
		if stopCause != nil && !interrupted {
			_ = m.Interrupt(context.Background())
			interrupted = true
			deadline = time.Now().Add(interruptGrace)
		}
		if stopCause != nil && interrupted && time.Now().After(deadline) {
			if res.Status == "" {
				res.Status = "abort"
			}
			res.Stdout = outTerm.String()
			res.Stderr = errTerm.String()
			return res, stopCause
		}

		remain := time.Until(deadline)
		if remain <= 0 {
			continue
		}
		// After cancel, parent ctx is done — drain with Background so we can
		// still observe idle / execute_reply from the interrupted run.
		parentCtx := ctx
		if stopCause != nil {
			parentCtx = context.Background()
		}
		rctx, cancel := context.WithTimeout(parentCtx, remain)
		msg, ch, err := m.Conn.recvEither(rctx)
		cancel()
		if err != nil {
			if stopCause != nil {
				continue
			}
			if ctx.Err() != nil {
				// loop will set stopCause and interrupt on next iteration
				continue
			}
			if time.Now().After(deadline) {
				continue
			}
			continue
		}
		parent := msg.ParentHeader.MsgID
		switch ch {
		case "shell":
			if msg.Header.MsgType == "execute_reply" && parent == msgID {
				if s, ok := msg.Content["status"].(string); ok {
					res.Status = s
				}
				if n, ok := asInt(msg.Content["execution_count"]); ok {
					res.ExecutionCount = n
				}
				if res.Status == "error" {
					res.Ename, _ = msg.Content["ename"].(string)
					res.Evalue, _ = msg.Content["evalue"].(string)
				}
				// Interrupted kernels often reply with status "abort" / "error".
				if stopCause != nil && res.Status == "" {
					res.Status = "abort"
				}
			}
		case "iopub":
			if parent != "" && parent != msgID {
				continue
			}
			switch msg.Header.MsgType {
			case "stream":
				name, _ := msg.Content["name"].(string)
				text := multilineContent(msg.Content["text"])
				if text == "" {
					continue
				}
				if name == "stderr" {
					errTerm.Write(text)
					res.Stderr = errTerm.String()
					if opts.OnStream != nil {
						// Text is the full filtered stream so far (replace, not append).
						opts.OnStream(StreamChunk{Name: "stderr", Text: res.Stderr})
					}
				} else {
					outTerm.Write(text)
					res.Stdout = outTerm.String()
					if opts.OnStream != nil {
						opts.OnStream(StreamChunk{Name: "stdout", Text: res.Stdout})
					}
				}
			case "error":
				res.Ename, _ = msg.Content["ename"].(string)
				res.Evalue, _ = msg.Content["evalue"].(string)
				// Tracebacks are often ANSI-colored.
				res.Ename = FilterTerminal(res.Ename)
				res.Evalue = FilterTerminal(res.Evalue)
				res.Status = "error"
			case "status":
				state, _ := msg.Content["execution_state"].(string)
				if state == "idle" && (parent == msgID || parent == "") {
					// Only finish when this execute is idle; empty parent is rare
					if parent == msgID {
						res.Stdout = outTerm.String()
						res.Stderr = errTerm.String()
						if stopCause != nil {
							if res.Status == "" {
								res.Status = "abort"
							}
							return res, stopCause
						}
						if res.Status == "" {
							res.Status = "ok"
						}
						return res, nil
					}
				}
			case "execute_result", "display_data":
				dd := DisplayData{
					OutputType: msg.Header.MsgType,
					Data:       normalizeMimeBundle(msg.Content["data"]),
				}
				if md, ok := msg.Content["metadata"].(map[string]any); ok {
					dd.Metadata = md
				}
				if tr, ok := msg.Content["transient"].(map[string]any); ok {
					dd.Transient = tr
				}
				if len(dd.Data) == 0 {
					continue
				}
				// Keep a plain-text breadcrumb in stdout for logs / untrusted clients.
				if tp, ok := dd.Data["text/plain"].(string); ok && tp != "" {
					// Don't also dump figure placeholders into the stream UI if we
					// already ship the full bundle — only for result summary.
					_ = tp
				}
				if opts.OnDisplay != nil {
					opts.OnDisplay(dd)
				}
			case "clear_output":
				// Client handles rebuild; we don't push a dedicated event yet.
				// Wait for next display_data.
			}
		}
	}
}

func (c *Conn) recvEither(ctx context.Context) (Message, string, error) {
	select {
	case <-ctx.Done():
		return Message{}, "", ctx.Err()
	case msg := <-c.shellCh:
		return msg, "shell", nil
	case msg := <-c.iopubCh:
		return msg, "iopub", nil
	}
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

func multilineContent(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var s string
		for _, x := range t {
			s += fmt.Sprint(x)
		}
		return s
	case []string:
		return strings.Join(t, "")
	default:
		if v == nil {
			return ""
		}
		return fmt.Sprint(v)
	}
}

// normalizeMimeBundle flattens Jupyter mime values (string | []string) and
// drops oversized payloads so a single PNG can't blow up the WebSocket.
func normalizeMimeBundle(raw any) map[string]any {
	src, ok := raw.(map[string]any)
	if !ok || len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	var dropped []string
	for mime, v := range src {
		s := multilineContent(v)
		if s == "" {
			// Keep non-string JSON mimes (e.g. application/json objects).
			switch v.(type) {
			case map[string]any, []any, float64, bool:
				out[mime] = v
			}
			continue
		}
		if len(s) > MaxDisplayBytes {
			dropped = append(dropped, mime)
			continue
		}
		out[mime] = s
	}
	if len(dropped) > 0 {
		note := "[gaderno: omitted oversized mime: " + strings.Join(dropped, ", ") + "]"
		if existing, ok := out["text/plain"].(string); ok && existing != "" {
			out["text/plain"] = existing + "\n" + note
		} else {
			out["text/plain"] = note
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
