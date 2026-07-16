package app

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/lucasew/gaderno/internal/session"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024 * 64,
	WriteBufferSize: 1024 * 64,
	CheckOrigin: func(r *http.Request) bool {
		return true // local-first; tighten with token later
	},
}

// JSON control messages (text frames).
type wsControl struct {
	Type   string `json:"type"`
	CellID string `json:"cell_id,omitempty"`
	Text   string `json:"text,omitempty"`
	Source string `json:"source,omitempty"`
	Name   string `json:"name,omitempty"`
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
		hub.SendKernelStatus(client)

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

		step1 := hub.EncodeSyncStep1()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_ = conn.WriteMessage(websocket.BinaryMessage, step1)

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
				handleControl(r, hub, client, ctrl, logger)
			}
		}
		<-done
	})
}

func handleControl(r *http.Request, hub *session.Hub, client *session.Client, ctrl wsControl, logger *slog.Logger) {
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
		if ctrl.CellID == "" {
			sendErr(client, "cell_id required")
			return
		}
		if err := hub.SetCellSource(ctrl.CellID, ctrl.Source); err != nil {
			sendErr(client, err.Error())
			return
		}
		// ack originator (synced to server memory)
		b, _ := json.Marshal(map[string]any{
			"type":    "cell.source_ack",
			"cell_id": ctrl.CellID,
		})
		select {
		case client.Out <- session.Outbound{Data: b}:
		default:
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
			// Optional: apply source from client if provided (flush-before-run)
			if ctrl.Source != "" && ctrl.CellID != "" {
				_ = hub.SetCellSource(ctrl.CellID, ctrl.Source)
			}
			ctx := r.Context()
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
			res, err := hub.ExecuteCell(ctx, ctrl.CellID)
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
	}
}

func sendErr(client *session.Client, msg string) {
	b, _ := json.Marshal(map[string]string{"type": "error", "text": msg})
	select {
	case client.Out <- session.Outbound{Data: b}:
	default:
	}
}
