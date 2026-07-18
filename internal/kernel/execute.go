package kernel

import (
	"context"
	"fmt"
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

// ExecuteOpts configures execute behavior.
type ExecuteOpts struct {
	// OnStream is called for each IOPub stream chunk (may be nil).
	OnStream func(StreamChunk)
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

	for {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			return res, fmt.Errorf("execute timeout")
		}
		rctx, cancel := context.WithTimeout(ctx, remain)
		msg, ch, err := m.Conn.recvEither(rctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			if time.Now().After(deadline) {
				return res, fmt.Errorf("execute timeout")
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
						if res.Status == "" {
							res.Status = "ok"
						}
						res.Stdout = outTerm.String()
						res.Stderr = errTerm.String()
						return res, nil
					}
				}
			case "execute_result", "display_data":
				if data, ok := msg.Content["data"].(map[string]any); ok {
					if tp, ok := data["text/plain"]; ok {
						text := multilineContent(tp)
						if text == "" {
							continue
						}
						outTerm.Write(text)
						res.Stdout = outTerm.String()
						if opts.OnStream != nil {
							opts.OnStream(StreamChunk{Name: "stdout", Text: res.Stdout})
						}
					}
				}
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
	default:
		if v == nil {
			return ""
		}
		return fmt.Sprint(v)
	}
}
