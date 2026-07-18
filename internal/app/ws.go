package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/lucasew/gaderno/internal/kernel"
	"github.com/lucasew/gaderno/internal/session"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024 * 64,
	WriteBufferSize: 1024 * 64,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type wsControl struct {
	Type      string `json:"type"`
	CellID    string `json:"cell_id,omitempty"`
	Text      string `json:"text,omitempty"`
	Source    string `json:"source,omitempty"`
	Name      string `json:"name,omitempty"`
	Update    string `json:"update,omitempty"` // base64 awareness payload
	Index     *int   `json:"index,omitempty"`
	Code        string `json:"code,omitempty"`
	CursorPos   *int   `json:"cursor_pos,omitempty"`
	ReqID       string `json:"req_id,omitempty"`
	DetailLevel *int   `json:"detail_level,omitempty"`
}

func registerWS(mux *http.ServeMux, reg *session.Registry, logger *slog.Logger) {
	mux.HandleFunc("GET /ws/notebooks/{path...}", func(w http.ResponseWriter, r *http.Request) {
		path := r.PathValue("path")
		hub, err := reg.GetOrOpen(r.Context(), path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Error("ws upgrade", "err", err)
			return
		}
		clientID := uuid.NewString()
		client := hub.AddClient(clientID)
		defer hub.RemoveClient(clientID)
		defer conn.Close()

		// Single writer goroutine — gorilla/websocket is not concurrent-safe.
		done := make(chan struct{})
		go func() {
			defer close(done)
			for out := range client.Out {
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				mt := websocket.TextMessage
				if out.Binary {
					mt = websocket.BinaryMessage
				}
				if err := conn.WriteMessage(mt, out.Data); err != nil {
					return
				}
			}
		}()

		// hello only until client acks — do not push CRDT state or accept
		// client updates before the session fence passes (prevents a tab with
		// a previous Y.Doc from poisoning a recreated hub on reconnect).
		hello, _ := json.Marshal(map[string]string{
			"type":       "hello",
			"session_id": hub.SessionID,
			"client_id":  clientID,
		})
		client.Out <- session.Outbound{Data: hello}

		for {
			_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
			mt, data, err := conn.ReadMessage()
			if err != nil {
				break
			}
			switch mt {
			case websocket.BinaryMessage:
				reply, err := hub.HandleSyncMessage(clientID, data)
				if err != nil {
					logger.Debug("sync apply", "err", err)
					continue
				}
				if reply != nil {
					select {
					case client.Out <- session.Outbound{Binary: true, Data: reply}:
					default:
					}
				}
			case websocket.TextMessage:
				var ctrl wsControl
				if err := json.Unmarshal(data, &ctrl); err != nil {
					continue
				}
				if ctrl.Type == "hello.ack" {
					var ack struct {
						SessionID string `json:"session_id"`
					}
					_ = json.Unmarshal(data, &ack)
					if ack.SessionID != "" && ack.SessionID != hub.SessionID {
						sendErr(client, "session_id mismatch")
						continue
					}
					if !hub.MarkClientReady(clientID) {
						continue
					}
					hub.SendKernelStatus(client)
					select {
					case client.Out <- session.Outbound{Binary: true, Data: hub.EncodeSyncStep1()}:
					default:
					}
					continue
				}
				if !hub.ClientReady(clientID) {
					// Drop awareness/control until session is confirmed.
					continue
				}
				// Awareness: pass through raw JSON so "update" is preserved.
				if ctrl.Type == "awareness" {
					hub.BroadcastJSON(data, clientID)
					continue
				}
				handleControl(hub, client, clientID, ctrl, logger)
			}
		}
		<-done
	})
}

func handleControl(hub *session.Hub, client *session.Client, clientID string, ctrl wsControl, logger *slog.Logger) {
	switch ctrl.Type {
	case "ping":
		b, _ := json.Marshal(map[string]string{"type": "pong"})
		select {
		case client.Out <- session.Outbound{Data: b}:
		default:
		}
	case "chat.send":
		b, _ := json.Marshal(map[string]string{
			"type": "chat.message",
			"text": ctrl.Text,
			"from": client.ID[:8],
		})
		hub.BroadcastJSON(b, "")
	case "cell.set_source":
		// Legacy full-cell replace (still used as Run flush safety).
		if ctrl.CellID == "" {
			sendErr(client, "cell_id required")
			return
		}
		if err := hub.SetCellSource(ctrl.CellID, ctrl.Source, clientID); err != nil {
			sendErr(client, err.Error())
			return
		}
		b, _ := json.Marshal(map[string]any{
			"type":    "cell.source_ack",
			"cell_id": ctrl.CellID,
		})
		select {
		case client.Out <- session.Outbound{Data: b}:
		default:
		}
	case "cell.insert":
		idx := 0
		if ctrl.Index != nil {
			idx = *ctrl.Index
		} else {
			// append by default
			idx = len(hub.Doc.SnapshotCells())
		}
		ct := ctrl.Text // "code" | "markdown"
		if ct == "" {
			ct = "code"
		}
		id, err := hub.InsertCell(idx, ct)
		if err != nil {
			sendErr(client, err.Error())
			return
		}
		// structure broadcast already sent; include focus hint to originator
		b, _ := json.Marshal(map[string]any{"type": "cell.inserted", "cell_id": id, "index": idx})
		select {
		case client.Out <- session.Outbound{Data: b}:
		default:
		}
	case "cell.delete":
		if ctrl.CellID == "" {
			sendErr(client, "cell_id required")
			return
		}
		if err := hub.DeleteCell(ctrl.CellID); err != nil {
			sendErr(client, err.Error())
		}
	case "cell.set_type":
		if ctrl.CellID == "" {
			sendErr(client, "cell_id required")
			return
		}
		ct := ctrl.Text
		if ct == "" {
			ct = ctrl.Name
		}
		if err := hub.SetCellType(ctrl.CellID, ct); err != nil {
			sendErr(client, err.Error())
		}
	case "cell.move":
		if ctrl.CellID == "" || ctrl.Index == nil {
			sendErr(client, "cell_id and index required")
			return
		}
		if err := hub.MoveCell(ctrl.CellID, *ctrl.Index); err != nil {
			sendErr(client, err.Error())
		}
	case "kernel.bind":
		name := ctrl.Name
		if name == "" {
			name = ctrl.Text
		}
		if err := hub.BindKernel(name); err != nil {
			sendErr(client, err.Error())
		}
	case "exec.run":
		go func() {
			// Prefer live CRDT text; client source is a flush backup.
			if ctrl.CellID != "" && ctrl.Source != "" {
				_ = hub.SetCellSource(ctrl.CellID, ctrl.Source, clientID)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			if err := hub.EnsureKernel(ctx, ""); err != nil {
				if err.Error() == "no kernel selected" {
					b, _ := json.Marshal(map[string]any{"type": "kernel.needs_pick"})
					select {
					case client.Out <- session.Outbound{Data: b}:
					default:
					}
				}
				sendErr(client, err.Error())
				return
			}
			res, err := hub.ExecuteCell(ctx, ctrl.CellID, func(ch kernel.StreamChunk) {
				b, _ := json.Marshal(map[string]any{
					"type":    "exec.stream",
					"cell_id": ctrl.CellID,
					"name":    ch.Name,
					"text":    ch.Text,
				})
				hub.BroadcastJSON(b, "")
			})
			if err != nil {
				sendErr(client, err.Error())
				return
			}
			b, _ := json.Marshal(map[string]any{
				"type":            "exec.result",
				"cell_id":         ctrl.CellID,
				"status":          res.Status,
				"stdout":          res.Stdout,
				"stderr":          res.Stderr,
				"ename":           res.Ename,
				"evalue":          res.Evalue,
				"execution_count": res.ExecutionCount,
			})
			hub.BroadcastJSON(b, "")
		}()
	case "complete.request":
		// Async; reply only to requesting client (not broadcast).
		go func() {
			code := ctrl.Code
			if code == "" {
				code = ctrl.Source
			}
			pos := 0
			if ctrl.CursorPos != nil {
				pos = *ctrl.CursorPos
			} else if len(code) > 0 {
				pos = len(code)
			}
			reqID := ctrl.ReqID
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			res, err := hub.Complete(ctx, code, pos)
			if err != nil {
				b, _ := json.Marshal(map[string]any{
					"type":         "complete.reply",
					"req_id":       reqID,
					"status":       "error",
					"matches":      []string{},
					"cursor_start": pos,
					"cursor_end":   pos,
					"text":         err.Error(),
				})
				select {
				case client.Out <- session.Outbound{Data: b}:
				default:
				}
				return
			}
			b, _ := json.Marshal(map[string]any{
				"type":         "complete.reply",
				"req_id":       reqID,
				"status":       res.Status,
				"matches":      res.Matches,
				"cursor_start": res.CursorStart,
				"cursor_end":   res.CursorEnd,
			})
			select {
			case client.Out <- session.Outbound{Data: b}:
			default:
			}
		}()
	case "inspect.request":
		// Hover / signature help — reply only to originator.
		go func() {
			code := ctrl.Code
			if code == "" {
				code = ctrl.Source
			}
			pos := 0
			if ctrl.CursorPos != nil {
				pos = *ctrl.CursorPos
			} else if len(code) > 0 {
				pos = len(code)
			}
			detail := 0
			if ctrl.DetailLevel != nil {
				detail = *ctrl.DetailLevel
			}
			reqID := ctrl.ReqID
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			res, err := hub.Inspect(ctx, code, pos, detail)
			if err != nil {
				b, _ := json.Marshal(map[string]any{
					"type":         "inspect.reply",
					"req_id":       reqID,
					"status":       "error",
					"found":        false,
					"text":         err.Error(),
					"detail_level": detail,
				})
				select {
				case client.Out <- session.Outbound{Data: b}:
				default:
				}
				return
			}
			b, _ := json.Marshal(map[string]any{
				"type":         "inspect.reply",
				"req_id":       reqID,
				"status":       res.Status,
				"found":        res.Found,
				"text":         res.Text,
				"html":         res.HTML,
				"detail_level": res.DetailLevel,
			})
			select {
			case client.Out <- session.Outbound{Data: b}:
			default:
			}
		}()
	}
}

func sendErr(client *session.Client, msg string) {
	b, _ := json.Marshal(map[string]string{"type": "error", "text": msg})
	select {
	case client.Out <- session.Outbound{Data: b}:
	default:
	}
}
