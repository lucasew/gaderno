package session

import (
	"context"
	"sync"

	"github.com/lucasew/gaderno/internal/store"
)

// Registry holds live hubs keyed by notebook relative path.
type Registry struct {
	mu    sync.Mutex
	hubs  map[string]*Hub
	store *store.Store
	root  string
}

// NewRegistry creates an empty registry.
// defaultKernel is unused for autostart; kept for CLI compat (ignored).
func NewRegistry(st *store.Store, root, _ string) *Registry {
	return &Registry{
		hubs:  make(map[string]*Hub),
		store: st,
		root:  root,
	}
}

// GetOrOpen returns an existing hub or opens one.
// Paths are canonicalized so "./n.ipynb" and "n.ipynb" share one hub
// (avoids split-brain CRDT / last-writer-wins on the same file).
//
// Disk load (Open) runs outside the registry lock so opening one notebook
// does not block GetOrOpen / CloseAll for others. Concurrent first opens of
// the same path race safely: losers discard their hub and return the winner.
func (r *Registry) GetOrOpen(ctx context.Context, rel string) (*Hub, error) {
	rel, err := store.CleanRel(rel)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if h, ok := r.hubs[rel]; ok {
		r.mu.Unlock()
		return h, nil
	}
	r.mu.Unlock()

	h, err := Open(ctx, r.store, r.root, rel)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	if existing, ok := r.hubs[rel]; ok {
		r.mu.Unlock()
		// Lost the race: another Open finished first. Drop our duplicate
		// (no clients attached yet) and return the registered hub.
		_ = h.Close(ctx)
		return existing, nil
	}
	r.hubs[rel] = h
	r.mu.Unlock()
	return h, nil
}

// CloseAll shuts down every hub.
// Snapshot and clear under the lock, then Close outside so kernel shutdown
// (SIGINT wait / SIGKILL) cannot stall concurrent GetOrOpen for other paths.
func (r *Registry) CloseAll(ctx context.Context) {
	r.mu.Lock()
	hubs := r.hubs
	r.hubs = make(map[string]*Hub)
	r.mu.Unlock()
	for _, h := range hubs {
		_ = h.Close(ctx)
	}
}
