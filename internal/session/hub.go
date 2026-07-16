package session

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/lucasew/gaderno/internal/crdt"
	"github.com/lucasew/gaderno/internal/document"
	"github.com/lucasew/gaderno/internal/kernel"
	"github.com/lucasew/gaderno/internal/store"
)

// Hub is the per-notebook actor: document CRDT + optional kernel.
type Hub struct {
	Path       string
	Root       string // workspace absolute root
	Doc        *crdt.NotebookDoc
	store      *store.Store
	mu         sync.Mutex
	kernel     *kernel.Manager
	kernelName string
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
	return &Hub{
		Path:       rel,
		Root:       root,
		Doc:        doc,
		store:      st,
		kernelName: kernelspecName(nb),
	}, nil
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

// Save projects CRDT to ipynb and writes store.
func (h *Hub) Save(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	nb := h.Doc.ProjectNotebook()
	return h.store.Save(ctx, h.Path, nb)
}

// EnsureKernel starts the kernel if needed (cwd = notebook directory).
func (h *Hub) EnsureKernel(ctx context.Context, name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.kernel != nil {
		return nil
	}
	if name == "" {
		name = h.kernelName
	}
	cwd := filepath.Join(h.Root, filepath.Dir(h.Path))
	if filepath.Dir(h.Path) == "." {
		cwd = h.Root
	}
	m, err := kernel.Start(ctx, name, cwd)
	if err != nil {
		return err
	}
	h.kernel = m
	return nil
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

// Close shuts down the kernel if any.
func (h *Hub) Close(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.kernel != nil {
		err := h.kernel.Shutdown(ctx)
		h.kernel = nil
		return err
	}
	return nil
}
