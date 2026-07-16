package session

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/lucasew/gaderno/internal/crdt"
	"github.com/lucasew/gaderno/internal/document"
	"github.com/lucasew/gaderno/internal/kernel"
	"github.com/lucasew/gaderno/internal/store"
	ycrdt "github.com/reearth/ygo/crdt"
	ysync "github.com/reearth/ygo/sync"
)

// Client is a connected browser peer.
type Client struct {
	ID   string
	Send chan []byte // outbound binary yjs/sync frames or JSON text as bytes with flag — use Out
	Out  chan Outbound
}

// Outbound is a message to one client.
type Outbound struct {
	Binary bool
	Data   []byte
}

// Hub is the per-notebook actor: document CRDT + optional kernel + clients.
type Hub struct {
	Path       string
	Root       string
	Doc        *crdt.NotebookDoc
	store      *store.Store
	mu         sync.Mutex
	kernel     *kernel.Manager
	kernelName string
	clients    map[string]*Client
	saveTimer  *time.Timer
	unsub      func()
}

// Open loads a notebook from store into a new Hub.
func Open(ctx context.Context, st *store.Store, root, rel string) (*Hub, error) {
	nb, err := st.Load(ctx, rel)
	if err != nil {
		return nil, err
	}
	doc := crdt.New()
	if err := doc.LoadFromNotebook(nb); err != nil {
		return nil, err
	}
	h := &Hub{
		Path:       rel,
		Root:       root,
		Doc:        doc,
		store:      st,
		kernelName: kernelspecName(nb),
		clients:    make(map[string]*Client),
	}
	h.unsub = doc.Doc.OnUpdate(func(update []byte, origin any) {
		// Don't echo back to originator if origin is client id string
		originID, _ := origin.(string)
		frame := ysync.EncodeUpdate(update)
		h.broadcast(frame, true, originID)
		h.scheduleSave()
	})
	return h, nil
}

func kernelspecName(nb *document.Notebook) string {
	if nb.Metadata == nil {
		return "python3"
	}
	if ks, ok := nb.Metadata["kernelspec"].(map[string]any); ok {
		if n, ok := ks["name"].(string); ok && n != "" {
			return n
		}
	}
	return "python3"
}

func (h *Hub) scheduleSave() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.saveTimer != nil {
		h.saveTimer.Stop()
	}
	h.saveTimer = time.AfterFunc(500*time.Millisecond, func() {
		_ = h.Save(context.Background())
	})
}

// Save projects CRDT to ipynb and writes store.
func (h *Hub) Save(ctx context.Context) error {
	h.mu.Lock()
	nb := h.Doc.ProjectNotebook()
	st := h.store
	path := h.Path
	h.mu.Unlock()
	return st.Save(ctx, path, nb)
}

// EnsureKernel starts the kernel if needed.
func (h *Hub) EnsureKernel(ctx context.Context, name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.kernel != nil {
		return nil
	}
	if name == "" {
		name = h.kernelName
	}
	cwd := h.Root
	if d := filepath.Dir(h.Path); d != "." {
		cwd = filepath.Join(h.Root, d)
	}
	m, err := kernel.Start(ctx, name, cwd)
	if err != nil {
		return err
	}
	h.kernel = m
	return nil
}

// SetCellSource updates cell source in the CRDT (debounced save via OnUpdate).
func (h *Hub) SetCellSource(cellID, source string) error {
	return h.Doc.SetSourceServer(cellID, source)
}

// ExecuteCell runs a cell by id.
func (h *Hub) ExecuteCell(ctx context.Context, cellID string) (kernel.ExecuteResult, error) {
	h.mu.Lock()
	k := h.kernel
	src := h.Doc.Source(cellID)
	h.mu.Unlock()
	if k == nil {
		return kernel.ExecuteResult{}, fmt.Errorf("kernel not started")
	}
	return k.Execute(ctx, src)
}

// AddClient registers a peer and returns it.
func (h *Hub) AddClient(id string) *Client {
	c := &Client{
		ID:  id,
		Out: make(chan Outbound, 64),
	}
	h.mu.Lock()
	h.clients[id] = c
	h.mu.Unlock()
	return c
}

// RemoveClient unregisters a peer.
func (h *Hub) RemoveClient(id string) {
	h.mu.Lock()
	if c, ok := h.clients[id]; ok {
		close(c.Out)
		delete(h.clients, id)
	}
	h.mu.Unlock()
}

// HandleSyncMessage applies a y-protocols sync frame and returns an optional reply.
func (h *Hub) HandleSyncMessage(clientID string, msg []byte) ([]byte, error) {
	return ysync.ApplySyncMessage(h.Doc.Doc, msg, clientID)
}

// EncodeSyncStep1 for server-initiated handshake.
func (h *Hub) EncodeSyncStep1() []byte {
	return ysync.EncodeSyncStep1(h.Doc.Doc)
}

func (h *Hub) broadcast(data []byte, binary bool, skipClientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, c := range h.clients {
		if id == skipClientID {
			continue
		}
		select {
		case c.Out <- Outbound{Binary: binary, Data: data}:
		default:
			// drop if slow
		}
	}
}

// Close shuts down the kernel and clients.
func (h *Hub) Close(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.unsub != nil {
		h.unsub()
		h.unsub = nil
	}
	if h.saveTimer != nil {
		h.saveTimer.Stop()
	}
	for id, c := range h.clients {
		close(c.Out)
		delete(h.clients, id)
	}
	if h.kernel != nil {
		err := h.kernel.Shutdown(ctx)
		h.kernel = nil
		return err
	}
	return nil
}

// DocRaw exposes underlying ygo doc for advanced ops.
func (h *Hub) DocRaw() *ycrdt.Doc { return h.Doc.Doc }

// BroadcastJSON sends a text frame to all clients (or all but skip).
func (h *Hub) BroadcastJSON(data []byte, skipClientID string) {
	h.broadcast(data, false, skipClientID)
}
