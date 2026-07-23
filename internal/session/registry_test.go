package session

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/lucasew/gaderno/internal/document"
	"github.com/lucasew/gaderno/internal/store"
)

func TestGetOrOpenCanonicalPath(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	nb := document.NewEmpty()
	if err := st.Save(context.Background(), "n.ipynb", nb); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(st, dir, "python3")
	defer reg.CloseAll(context.Background())

	h1, err := reg.GetOrOpen(context.Background(), "./n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := reg.GetOrOpen(context.Background(), "sub/../n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("expected same hub for equivalent paths, got %p vs %p", h1, h2)
	}
	if h1.Path != "n.ipynb" {
		t.Fatalf("hub path = %q, want n.ipynb", h1.Path)
	}
	// Third spelling also hits the same hub (map key is CleanRel).
	h3, err := reg.GetOrOpen(context.Background(), "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	if h3 != h1 {
		t.Fatalf("expected same hub for bare path")
	}
}

func TestGetOrOpenRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	reg := NewRegistry(st, dir, "python3")
	for _, p := range []string{"", ".", ".."} {
		if _, err := reg.GetOrOpen(context.Background(), p); err == nil {
			t.Fatalf("GetOrOpen(%q) expected error", p)
		}
	}
}

// Concurrent first-open of the same path must yield one shared hub (no
// split-brain) even though Open runs outside the registry mutex.
func TestGetOrOpenConcurrentSamePath(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	if err := st.Save(context.Background(), "n.ipynb", document.NewEmpty()); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(st, dir, "python3")
	defer reg.CloseAll(context.Background())

	const n = 16
	hubs := make([]*Hub, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			h, err := reg.GetOrOpen(context.Background(), "n.ipynb")
			hubs[i] = h
			errs[i] = err
		}()
	}
	wg.Wait()

	var first *Hub
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("GetOrOpen[%d]: %v", i, errs[i])
		}
		if hubs[i] == nil {
			t.Fatalf("GetOrOpen[%d]: nil hub", i)
		}
		if first == nil {
			first = hubs[i]
			continue
		}
		if hubs[i] != first {
			t.Fatalf("split hub: %p vs %p", first, hubs[i])
		}
	}
}

// Opening distinct notebooks concurrently must not serialize on each other's
// disk load (regression guard for holding r.mu across Open).
func TestGetOrOpenConcurrentDistinctPaths(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	const n = 8
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("n%d.ipynb", i)
		if err := st.Save(context.Background(), name, document.NewEmpty()); err != nil {
			t.Fatal(err)
		}
	}
	reg := NewRegistry(st, dir, "python3")
	defer reg.CloseAll(context.Background())

	hubs := make([]*Hub, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			h, err := reg.GetOrOpen(context.Background(), fmt.Sprintf("n%d.ipynb", i))
			hubs[i] = h
			errs[i] = err
		}()
	}
	wg.Wait()

	seen := map[*Hub]string{}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("GetOrOpen[%d]: %v", i, errs[i])
		}
		if hubs[i] == nil {
			t.Fatalf("GetOrOpen[%d]: nil hub", i)
		}
		if prev, ok := seen[hubs[i]]; ok {
			t.Fatalf("distinct paths shared hub %p (%s and n%d.ipynb)", hubs[i], prev, i)
		}
		seen[hubs[i]] = hubs[i].Path
	}
}

// CloseAll must leave the registry empty and be idempotent.
func TestCloseAllClearsRegistry(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	if err := st.Save(context.Background(), "n.ipynb", document.NewEmpty()); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(st, dir, "python3")
	if _, err := reg.GetOrOpen(context.Background(), "n.ipynb"); err != nil {
		t.Fatal(err)
	}
	reg.CloseAll(context.Background())
	reg.CloseAll(context.Background()) // idempotent

	// Re-open after CloseAll creates a new hub (map was cleared).
	h, err := reg.GetOrOpen(context.Background(), "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	if h == nil {
		t.Fatal("expected hub after re-open")
	}
	reg.CloseAll(context.Background())
}
