//go:build !unix

package store

import "os"

// Non-unix: no flock; Save/Load rely on atomic rename alone.
func tryFlock(f *os.File, exclusive bool) error { return nil }
func tryFunlock(f *os.File)                     {}
