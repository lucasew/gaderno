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
	// waitCh receives the result of the single cmd.Wait() started at spawn.
	// Shutdown drains it instead of calling Wait again (double-Wait panics /
	// races after reaping).
	waitCh chan error
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

	// One Wait ownership for the process lifetime. ProcessState is only set
	// after Wait, so the old "if ProcessState != nil && Exited()" check never
	// fired and a crashing kernelspec spun the dial loop for up to 2 minutes.
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	waited := false

	// Socket lifetime context — must outlive dial attempts (not per-try timeout).
	sockCtx, sockCancel := context.WithCancel(context.Background())

	cleanupFail := func(ret error) (*Manager, error) {
		sockCancel()
		if !waited && cmd.Process != nil {
			_ = killProcessGroup(cmd, syscall.SIGKILL)
		}
		if !waited {
			select {
			case <-waitCh:
				waited = true
			case <-time.After(3 * time.Second):
			}
		}
		_ = os.RemoveAll(tmp)
		return nil, ret
	}

	earlyExit := func(waitErr error) (*Manager, error) {
		waited = true
		if cmd.ProcessState != nil {
			return cleanupFail(fmt.Errorf("kernel process exited early: %s", cmd.ProcessState.String()))
		}
		if waitErr != nil {
			return cleanupFail(fmt.Errorf("kernel process exited early: %w", waitErr))
		}
		return cleanupFail(fmt.Errorf("kernel process exited early"))
	}

	// Reap an abandoned Dial so sockets do not leak if the process dies mid-dial.
	// zmq dial often ignores cancel until its own timeout, so callers must not
	// block on this when fail-fast matters.
	abandonDial := func(dialCh <-chan dialOutcome) {
		go func() {
			o := <-dialCh
			if o.conn != nil {
				_ = o.conn.Close()
			}
		}()
	}

	var conn *Conn
	deadline := time.Now().Add(2 * time.Minute)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return cleanupFail(err)
		}

		// Cheap poll: catch instant-exit argv (/bin/false, missing binary) before
		// paying multi-socket Dial cost.
		select {
		case waitErr := <-waitCh:
			return earlyExit(waitErr)
		default:
		}

		dialCh := make(chan dialOutcome, 1)
		go func() {
			c, e := Dial(sockCtx, cf, session)
			dialCh <- dialOutcome{conn: c, err: e}
		}()

		var outcome dialOutcome
		select {
		case waitErr := <-waitCh:
			// Process died while dialing — do not wait for zmq timeouts.
			sockCancel()
			abandonDial(dialCh)
			return earlyExit(waitErr)
		case <-ctx.Done():
			sockCancel()
			abandonDial(dialCh)
			return cleanupFail(ctx.Err())
		case outcome = <-dialCh:
		}

		if outcome.err == nil {
			conn = outcome.conn
			break
		}
		lastErr = outcome.err

		select {
		case waitErr := <-waitCh:
			return earlyExit(waitErr)
		case <-ctx.Done():
			return cleanupFail(ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
	if conn == nil {
		if lastErr == nil {
			lastErr = fmt.Errorf("dial timeout")
		}
		return cleanupFail(lastErr)
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
		waitCh:   waitCh,
	}
	infoCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if _, err := conn.KernelInfo(infoCtx); err != nil {
		_ = m.Shutdown(context.Background())
		return nil, fmt.Errorf("kernel_info: %w", err)
	}
	return m, nil
}

type dialOutcome struct {
	conn *Conn
	err  error
}

// Interrupt asks the kernel to stop the current execution via control-channel
// interrupt_request (Jupyter protocol). Best-effort: returns nil after the
// message is sent; does not wait for the kernel to become idle.
func (m *Manager) Interrupt(ctx context.Context) error {
	if m.Conn == nil {
		return fmt.Errorf("no connection")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	req := Message{
		Header:  NewHeader(m.Session, "interrupt_request"),
		Content: map[string]any{},
	}
	return m.Conn.SendControl(req)
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
		// Prefer the Wait started at spawn; fall back only if missing (tests).
		waitCh := m.waitCh
		if waitCh == nil {
			waitCh = make(chan error, 1)
			cmd := m.Cmd
			go func() { waitCh <- cmd.Wait() }()
		}
		select {
		case <-waitCh:
		case <-time.After(3 * time.Second):
			_ = killProcessGroup(m.Cmd, syscall.SIGKILL)
			<-waitCh
		case <-ctx.Done():
			_ = killProcessGroup(m.Cmd, syscall.SIGKILL)
			<-waitCh
		}
		m.Cmd = nil
		m.waitCh = nil
	}
	if m.tmpDir != "" {
		_ = os.RemoveAll(m.tmpDir)
		m.tmpDir = ""
	}
	return nil
}

// ConnectionPath returns the connection file path (for tests).
func (m *Manager) ConnectionPath() string {
	return filepath.Clean(m.connPath)
}
