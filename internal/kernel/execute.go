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

// Execute runs code on the kernel and collects IOPub until idle for this msg.
func (m *Manager) Execute(ctx context.Context, code string) (ExecuteResult, error) {
	if m.Conn == nil {
		return ExecuteResult{}, fmt.Errorf("no connection")
	}
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
	deadline := time.Now().Add(60 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		// Prefer non-blocking-ish short reads by alternating shell/iopub with timeout via context on socket — Recv blocks.
		// Use a helper with goroutine select.
		msg, ch, err := m.Conn.recvAny(deadline)
		if err != nil {
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
			if parent != msgID && parent != "" {
				continue
			}
			switch msg.Header.MsgType {
			case "stream":
				name, _ := msg.Content["name"].(string)
				text := multilineContent(msg.Content["text"])
				if name == "stderr" {
					res.Stderr += text
				} else {
					res.Stdout += text
				}
			case "error":
				res.Ename, _ = msg.Content["ename"].(string)
				res.Evalue, _ = msg.Content["evalue"].(string)
				res.Status = "error"
			case "status":
				state, _ := msg.Content["execution_state"].(string)
				if state == "idle" && parent == msgID {
					if res.Status == "" {
						res.Status = "ok"
					}
					return res, nil
				}
			case "execute_result":
				if data, ok := msg.Content["data"].(map[string]any); ok {
					if tp, ok := data["text/plain"]; ok {
						res.Stdout += multilineContent(tp)
					}
				}
			}
		}
	}
	return res, fmt.Errorf("execute timeout")
}

func (c *Conn) recvAny(deadline time.Time) (Message, string, error) {
	type result struct {
		msg Message
		ch  string
		err error
	}
	ch := make(chan result, 2)
	go func() {
		msg, err := c.RecvShell(context.Background())
		ch <- result{msg, "shell", err}
	}()
	go func() {
		msg, err := c.RecvIOPub(context.Background())
		ch <- result{msg, "iopub", err}
	}()
	timeout := time.Until(deadline)
	if timeout < 0 {
		timeout = 0
	}
	select {
	case r := <-ch:
		return r.msg, r.ch, r.err
	case <-time.After(timeout):
		return Message{}, "", fmt.Errorf("timeout")
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
