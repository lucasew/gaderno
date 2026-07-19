//go:build unix

package store

import (
	"os"
	"syscall"
)

// tryFlock applies an advisory lock. Best-effort: returns nil when the
// filesystem does not support flock so callers still Load/Save.
func tryFlock(f *os.File, exclusive bool) error {
	if f == nil {
		return nil
	}
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	err := syscall.Flock(int(f.Fd()), how)
	if err == nil {
		return nil
	}
	// ENOTSUP and EOPNOTSUPP are the same errno on Linux.
	if err == syscall.ENOSYS || err == syscall.ENOTSUP {
		return nil
	}
	return err
}

func tryFunlock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
