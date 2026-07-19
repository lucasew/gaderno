package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucasew/gaderno/internal/document"
)

// Store loads and saves notebooks as .ipynb under a jail root.
type Store struct {
	root string
}

// New returns a store rooted at root.
func New(root string) *Store {
	return &Store{root: root}
}

// CleanRel returns a canonical jail-relative path (no leading slash, no "." / "..").
// Callers that key caches or hubs by path must use this so equivalent spellings
// ("./a.ipynb", "a.ipynb", "sub/../a.ipynb") share one entry.
func CleanRel(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	// Force absolute-style Clean so ".." segments collapse even when rel is relative.
	rel = filepath.Clean("/" + rel)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || rel == "." {
		return "", fmt.Errorf("empty path")
	}
	// After leading-slash Clean, ".." path segments are gone. Reject any leftover
	// segment (should not happen) without false-positive on names like "foo..bar".
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == ".." {
			return "", fmt.Errorf("path escapes root")
		}
	}
	return rel, nil
}

// Load reads and parses a notebook at relative path.
// Takes a shared advisory flock while reading (best-effort on unsupported FS).
func (s *Store) Load(_ context.Context, rel string) (*document.Notebook, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := tryFlock(f, false); err != nil {
		return nil, err
	}
	defer tryFunlock(f)

	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return document.Decode(raw)
}

// Save writes notebook atomically (temp + rename) under an exclusive advisory
// flock (SPEC: store flock best-effort). If the file does not exist yet, the
// lock is taken on a short-lived create before rename.
func (s *Store) Save(_ context.Context, rel string, nb *document.Notebook) error {
	abs, err := s.resolve(rel)
	if err != nil {
		return err
	}
	document.EnsureCellIDs(nb)
	raw, err := document.Encode(nb)
	if err != nil {
		return err
	}
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".gaderno-*.ipynb.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	// Exclusive lock on the destination path for the rename window so concurrent
	// Save/Load pair with external tools that also flock the ipynb.
	lockf, err := os.OpenFile(abs, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer lockf.Close()
	if err := tryFlock(lockf, true); err != nil {
		return err
	}
	defer tryFunlock(lockf)

	if err := os.Rename(tmpName, abs); err != nil {
		return err
	}
	tmpName = "" // rename took ownership; skip deferred Remove
	return nil
}

// CreateNew saves only if the path does not exist.
// Uses O_EXCL to claim the path atomically before writing content (avoids
// two concurrent creates both passing a Stat check).
func (s *Store) CreateNew(ctx context.Context, rel string, nb *document.Notebook) error {
	rel, err := CleanRel(rel)
	if err != nil {
		return err
	}
	abs, err := s.resolve(rel)
	if err != nil {
		return err
	}
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Atomic claim: empty exclusive create, then Save fills content under flock.
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return os.ErrExist
		}
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := s.Save(ctx, rel, nb); err != nil {
		// Drop the empty claim so a retry can succeed.
		_ = os.Remove(abs)
		return err
	}
	return nil
}

func (s *Store) resolve(rel string) (string, error) {
	rel, err := CleanRel(rel)
	if err != nil {
		return "", err
	}
	root := filepath.Clean(s.root)
	abs := filepath.Join(root, rel)
	// filepath.Rel rejects paths outside root more reliably than HasPrefix
	// (avoids "/tmp/foo" vs "/tmp/foobar" prefix tricks when root is absolute).
	relToRoot, err := filepath.Rel(root, abs)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes root")
	}
	if relToRoot == "." {
		return "", fmt.Errorf("empty path")
	}
	return abs, nil
}

// IsNotExist reports whether err is a missing file.
func IsNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist) || os.IsNotExist(err)
}
