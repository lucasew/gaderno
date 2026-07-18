//go:build unix

package kernel

import (
	"os"
	"os/exec"
	"syscall"
)

// Own process group so Shutdown can kill children (same idea as ciborg workers).
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup signals the process group. With Setpgid, pgid == pid.
func killProcessGroup(cmd *exec.Cmd, sig os.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	s, ok := sig.(syscall.Signal)
	if !ok {
		return cmd.Process.Signal(sig)
	}
	// ciborg pattern: kill(-pid) after Setpgid.
	if err := syscall.Kill(-cmd.Process.Pid, s); err != nil {
		return cmd.Process.Signal(sig)
	}
	return nil
}
