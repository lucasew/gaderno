package session

import (
	"context"
	"encoding/json"
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
	ID  string
	Out chan Outbound
}

// Outbound is a message to one client.
type Outbound struct {
	Binary bool
	Data   []byte
}

// KernelPhase is session-level kernel lifecycle (not process-internal).
type KernelPhase string

const (
	PhaseNeedsKernel KernelPhase = "needs_kernel"
	PhaseBound       KernelPhase = "bound"   // name selected, process not started
	PhaseStarting    KernelPhase = "starting"
	PhaseReady       KernelPhase = "ready"
	PhaseBusy        KernelPhase = "busy"
	PhaseDead        KernelPhase = "dead"
)

// Hub is the per-notebook actor: document CRDT + optional kernel + clients.
type Hub struct {
	Path  string
	Root  string
	Doc   *crdt.NotebookDoc
	store *store.Store

	mu         sync.Mutex
	kernel     *kernel.Manager
	boundName  string // empty = NeedsKernel
	phase      KernelPhase
	clients    map[string]*Client
	saveTimer  *time.Timer
	unsub      func()
	spawning   bool
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

	metaName := readKernelspecName(nb)
	bound := ""
	phase := PhaseNeedsKernel
	if metaName != "" {
		if cat, err := kernel.LoadCatalog(); err == nil && cat.Has(metaName) {
			bound = metaName
			phase = PhaseBound
		}
		// unresolvable metadata → NeedsKernel (no silent fallback)
	}

	h := &Hub{
		Path:      rel,
		Root:      root,
		Doc:       doc,
		store:     st,
		boundName: bound,
		phase:     phase,
		clients:   make(map[string]*Client),
	}
	h.unsub = doc.Doc.OnUpdate(func(update []byte, origin any) {
		originID, _ := origin.(string)
		frame := ysync.EncodeUpdate(update)
		h.broadcast(frame, true, originID)
		h.scheduleSave()
	})
	return h, nil
}

func readKernelspecName(nb *document.Notebook) string {
	if nb == nil || nb.Metadata == nil {
		return ""
	}
	if ks, ok := nb.Metadata["kernelspec"].(map[string]any); ok {
		if n, ok := ks["name"].(string); ok {
			return n
		}
	}
	return ""
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
	// keep kernelspec metadata if we already persisted it on the CRDT meta
	st := h.store
	path := h.Path
	h.mu.Unlock()
	return st.Save(ctx, path, nb)
}

// KernelStatus is broadcast / sent on join.
type KernelStatus struct {
	Phase     KernelPhase `json:"phase"`
	BoundName string      `json:"bound_name,omitempty"`
	Display   string      `json:"display_name,omitempty"`
	Running   bool        `json:"running"`
	NeedsPick bool        `json:"needs_kernel"`
}

// Status returns current kernel selection/spawn state.
func (h *Hub) Status() KernelStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.statusLocked()
}

func (h *Hub) statusLocked() KernelStatus {
	display := ""
	if h.boundName != "" {
		if cat, err := kernel.LoadCatalog(); err == nil {
			if s, err := cat.Spec(h.boundName); err == nil {
				display = s.Spec.DisplayName
				if display == "" {
					display = s.Name
				}
			}
		}
	}
	return KernelStatus{
		Phase:     h.phase,
		BoundName: h.boundName,
		Display:   display,
		Running:   h.kernel != nil,
		NeedsPick: h.boundName == "" || h.phase == PhaseNeedsKernel,
	}
}

// BindKernel selects a kernelspec without starting the process.
// If a different kernel was running, it is killed first.
func (h *Hub) BindKernel(name string) error {
	if name == "" {
		return fmt.Errorf("kernel name required")
	}
	cat, err := kernel.LoadCatalog()
	if err != nil {
		return err
	}
	if !cat.Has(name) {
		return fmt.Errorf("kernelspec %q not available", name)
	}

	h.mu.Lock()
	var old *kernel.Manager
	if h.kernel != nil && h.boundName != name {
		old = h.kernel
		h.kernel = nil
	}
	h.boundName = name
	h.phase = PhaseBound
	st := h.statusLocked()
	h.mu.Unlock()

	if old != nil {
		_ = old.Shutdown(context.Background())
	}
	h.broadcastKernelStatus(st)
	return nil
}

// EnsureKernel lazily starts the bound kernel on first Run.
func (h *Hub) EnsureKernel(ctx context.Context, name string) error {
	// Optional name: bind first if provided
	if name != "" {
		if err := h.BindKernel(name); err != nil {
			return err
		}
	}

	h.mu.Lock()
	if h.kernel != nil {
		h.mu.Unlock()
		return nil
	}
	if h.boundName == "" {
		h.phase = PhaseNeedsKernel
		h.mu.Unlock()
		return fmt.Errorf("no kernel selected")
	}
	if h.spawning {
		// wait for in-flight spawn
		h.mu.Unlock()
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			h.mu.Lock()
			if h.kernel != nil {
				h.mu.Unlock()
				return nil
			}
			if !h.spawning && h.phase == PhaseDead {
				h.mu.Unlock()
				return fmt.Errorf("kernel spawn failed")
			}
			h.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		return fmt.Errorf("kernel spawn timeout")
	}
	h.spawning = true
	h.phase = PhaseStarting
	bound := h.boundName
	cwd := h.Root
	if d := filepath.Dir(h.Path); d != "." {
		cwd = filepath.Join(h.Root, d)
	}
	st := h.statusLocked()
	h.mu.Unlock()
	h.broadcastKernelStatus(st)

	m, err := kernel.Start(ctx, bound, cwd)

	h.mu.Lock()
	h.spawning = false
	if err != nil {
		h.phase = PhaseDead
		st := h.statusLocked()
		h.mu.Unlock()
		h.broadcastKernelStatus(st)
		return err
	}
	h.kernel = m
	h.phase = PhaseReady
	// persist kernelspec into notebook only after successful spawn
	_ = h.persistKernelspecLocked(bound)
	st = h.statusLocked()
	h.mu.Unlock()
	h.broadcastKernelStatus(st)
	return nil
}

func (h *Hub) persistKernelspecLocked(name string) error {
	cat, err := kernel.LoadCatalog()
	if err != nil {
		return err
	}
	spec, err := cat.Spec(name)
	if err != nil {
		return err
	}
	// Project notebook, set metadata, save via store (hold lock — caller holds it)
	nb := h.Doc.ProjectNotebook()
	if nb.Metadata == nil {
		nb.Metadata = map[string]any{}
	}
	nb.Metadata["kernelspec"] = map[string]any{
		"name":         spec.Name,
		"display_name": spec.Spec.DisplayName,
		"language":     spec.Spec.Language,
	}
	if spec.Spec.Language != "" {
		nb.Metadata["language_info"] = map[string]any{"name": spec.Spec.Language}
	}
	// Write through store so next open binds correctly.
	// Also re-load into CRDT meta is best-effort; file is source of truth on reopen.
	return h.store.Save(context.Background(), h.Path, nb)
}

// InsertCell adds a cell and notifies clients to rebuild structure.
func (h *Hub) InsertCell(index int, cellType string) (string, error) {
	ct := document.CellType(cellType)
	if ct == "" {
		ct = document.CellCode
	}
	id, err := h.Doc.InsertCell(index, ct, "")
	if err != nil {
		return "", err
	}
	h.broadcastStructure()
	return id, nil
}

// DeleteCell removes a cell and notifies clients.
func (h *Hub) DeleteCell(cellID string) error {
	if err := h.Doc.DeleteCell(cellID); err != nil {
		return err
	}
	h.broadcastStructure()
	return nil
}

// MoveCell reorders a cell and notifies clients.
func (h *Hub) MoveCell(cellID string, toIndex int) error {
	if err := h.Doc.MoveCell(cellID, toIndex); err != nil {
		return err
	}
	h.broadcastStructure()
	return nil
}

// SetCellType changes code/markdown and notifies clients to rebuild.
func (h *Hub) SetCellType(cellID, cellType string) error {
	ct := document.CellType(cellType)
	if ct != document.CellCode && ct != document.CellMarkdown && ct != document.CellRaw {
		return fmt.Errorf("invalid cell type %q", cellType)
	}
	if err := h.Doc.SetCellType(cellID, ct); err != nil {
		return err
	}
	h.broadcastStructure()
	return nil
}

func (h *Hub) broadcastStructure() {
	cells := h.Doc.SnapshotCells()
	b, _ := json.Marshal(map[string]any{
		"type":  "notebook.structure",
		"cells": cells,
	})
	h.BroadcastJSON(b, "")
	h.scheduleSave()
}

// SetCellSource updates cell source in the CRDT and notifies other clients.
// skipClient is the originator (they already have the text); empty = notify all.
func (h *Hub) SetCellSource(cellID, source string, skipClient string) error {
	if err := h.Doc.SetSourceServer(cellID, source); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]any{
		"type":    "cell.source",
		"cell_id": cellID,
		"source":  source,
	})
	h.BroadcastJSON(b, skipClient)
	return nil
}

// ExecuteCell runs a cell by id (kernel must already be started).
// onStream may be nil; when set, called for each stdout/stderr chunk.
func (h *Hub) ExecuteCell(ctx context.Context, cellID string, onStream func(kernel.StreamChunk)) (kernel.ExecuteResult, error) {
	h.mu.Lock()
	k := h.kernel
	src := h.Doc.Source(cellID)
	if k == nil {
		h.mu.Unlock()
		return kernel.ExecuteResult{}, fmt.Errorf("kernel not started")
	}
	h.phase = PhaseBusy
	st := h.statusLocked()
	h.mu.Unlock()
	h.broadcastKernelStatus(st)

	// clear outputs signal
	b0, _ := json.Marshal(map[string]any{
		"type":    "exec.clear",
		"cell_id": cellID,
	})
	h.BroadcastJSON(b0, "")

	res, err := k.ExecuteOpts(ctx, src, kernel.ExecuteOpts{
		OnStream: onStream,
	})

	h.mu.Lock()
	if h.kernel != nil {
		h.phase = PhaseReady
	}
	st = h.statusLocked()
	h.mu.Unlock()
	h.broadcastKernelStatus(st)
	return res, err
}

// AddClient registers a peer.
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

// HandleSyncMessage applies a y-protocols sync frame.
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
		}
	}
}

// BroadcastJSON sends a text frame to all clients (or all but skip).
func (h *Hub) BroadcastJSON(data []byte, skipClientID string) {
	h.broadcast(data, false, skipClientID)
}

func (h *Hub) broadcastKernelStatus(st KernelStatus) {
	b, _ := json.Marshal(map[string]any{
		"type":   "kernel.status",
		"status": st,
	})
	h.BroadcastJSON(b, "")
}

// SendKernelStatus sends status to one client (on join).
func (h *Hub) SendKernelStatus(c *Client) {
	st := h.Status()
	b, _ := json.Marshal(map[string]any{
		"type":   "kernel.status",
		"status": st,
	})
	select {
	case c.Out <- Outbound{Data: b}:
	default:
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

// DocRaw exposes underlying ygo doc.
func (h *Hub) DocRaw() *ycrdt.Doc { return h.Doc.Doc }
