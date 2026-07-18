//go:build windows

package kernel

import (
	"os"
	"os/exec"
)

func setProcessGroup(cmd *exec.Cmd) {
	// No process groups like Unix; leave SysProcAttr default.
}

func killProcessGroup(cmd *exec.Cmd, sig os.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// Windows: signal the process only (kernelspec children may orphan).
	return cmd.Process.Signal(sig)
}
