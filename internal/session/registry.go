package session

import (
	"context"
	"sync"

	"github.com/lucasew/gaderno/internal/store"
)

// Registry holds live hubs keyed by notebook relative path.
type Registry struct {
	mu     sync.Mutex
	hubs   map[string]*Hub
	store  *store.Store
	root   string
	kernel string // default kernelspec
}

// NewRegistry creates an empty registry.
func NewRegistry(st *store.Store, root, defaultKernel string) *Registry {
	if defaultKernel == "" {
		defaultKernel = "python3"
	}
	return &Registry{
		hubs:   make(map[string]*Hub),
		store:  st,
		root:   root,
		kernel: defaultKernel,
	}
}

// GetOrOpen returns an existing hub or opens one.
func (r *Registry) GetOrOpen(ctx context.Context, rel string) (*Hub, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.hubs[rel]; ok {
		return h, nil
	}
	h, err := Open(ctx, r.store, r.root, rel)
	if err != nil {
		return nil, err
	}
	if h.kernelName == "python3" && r.kernel != "" {
		h.kernelName = r.kernel
	}
	r.hubs[rel] = h
	return h, nil
}

// CloseAll shuts down every hub.
func (r *Registry) CloseAll(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, h := range r.hubs {
		_ = h.Close(ctx)
		delete(r.hubs, k)
	}
}
