package kernel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// Manager owns one kernel process + ZMQ connection.
type Manager struct {
	Spec     Spec
	CF       ConnectionFile
	Conn     *Conn
	Cmd      *exec.Cmd
	Session  string
	WorkDir  string
	connPath string
	tmpDir   string
	cancel   context.CancelFunc
	// shellMu serializes shell request/response cycles (execute vs complete).
	shellMu sync.Mutex
}

// Start discovers the kernelspec, writes connection file, starts the process,
// dials ZMQ with long retries, and waits for kernel_info.
func Start(ctx context.Context, kernelName, workDir string) (*Manager, error) {
	spec, err := Find(kernelName)
	if err != nil {
		return nil, err
	}
	cf, err := NewConnectionFile(kernelName)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "gaderno-kernel-*")
	if err != nil {
		return nil, err
	}
	connPath, err := WriteConnectionFile(tmp, cf)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	session := uuid.NewString()

	cmd, err := StartProcess(spec, connPath, workDir)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}

	// Socket lifetime context — must outlive dial attempts (not per-try timeout).
	sockCtx, sockCancel := context.WithCancel(context.Background())

	var conn *Conn
	deadline := time.Now().Add(2 * time.Minute)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			sockCancel()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			_ = os.RemoveAll(tmp)
			return nil, err
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			sockCancel()
			_ = os.RemoveAll(tmp)
			return nil, fmt.Errorf("kernel process exited early: %v", cmd.ProcessState)
		}
		conn, lastErr = Dial(sockCtx, cf, session)
		if lastErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			sockCancel()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			_ = os.RemoveAll(tmp)
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if conn == nil {
		sockCancel()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(tmp)
		if lastErr == nil {
			lastErr = fmt.Errorf("dial timeout")
		}
		return nil, lastErr
	}

	m := &Manager{
		Spec:     spec,
		CF:       cf,
		Conn:     conn,
		Cmd:      cmd,
		Session:  session,
		WorkDir:  workDir,
		connPath: connPath,
		tmpDir:   tmp,
		cancel:   sockCancel,
	}
	infoCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if _, err := conn.KernelInfo(infoCtx); err != nil {
		_ = m.Shutdown(context.Background())
		return nil, fmt.Errorf("kernel_info: %w", err)
	}
	return m, nil
}

// Shutdown interrupts and kills the kernel, closes sockets.
func (m *Manager) Shutdown(ctx context.Context) error {
	if m.Conn != nil {
		_ = m.Conn.Close()
		m.Conn = nil
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.Cmd != nil && m.Cmd.Process != nil {
		_ = killProcessGroup(m.Cmd, syscall.SIGINT)
		done := make(chan error, 1)
		go func() { done <- m.Cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = killProcessGroup(m.Cmd, syscall.SIGKILL)
			<-done
		case <-ctx.Done():
			_ = killProcessGroup(m.Cmd, syscall.SIGKILL)
			<-done
		}
		m.Cmd = nil
	}
	if m.tmpDir != "" {
		_ = os.RemoveAll(m.tmpDir)
	}
	return nil
}

// ConnectionPath returns the connection file path (for tests).
func (m *Manager) ConnectionPath() string {
	return filepath.Clean(m.connPath)
}
