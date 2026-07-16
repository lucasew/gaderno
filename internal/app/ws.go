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

		// Single writer: all outbound frames go through client.Out (gorilla/websocket is not concurrent-safe).
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

		hub.SendKernelStatus(client)
		select {
		case client.Out <- session.Outbound{Binary: true, Data: hub.EncodeSyncStep1()}:
		default:
		}

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
			// Always prefer client buffer when provided (including empty string after clear).
			// Use presence of cell_id + explicit source field via separate flag — if Source is sent, apply.
			// Clients always send source on run.
			if ctrl.CellID != "" {
				_ = hub.SetCellSource(ctrl.CellID, ctrl.Source, clientID)
			}
			// Long-lived exec context (not tied to a short HTTP request)
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
	}
}

func sendErr(client *session.Client, msg string) {
	b, _ := json.Marshal(map[string]string{"type": "error", "text": msg})
	select {
	case client.Out <- session.Outbound{Data: b}:
	default:
	}
}
