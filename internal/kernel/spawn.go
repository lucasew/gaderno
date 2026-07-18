package kernel

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ExpandArgv replaces Jupyter placeholders in kernelspec argv.
func ExpandArgv(argv []string, connectionFile, resourceDir string) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		a = strings.ReplaceAll(a, "{connection_file}", connectionFile)
		a = strings.ReplaceAll(a, "{resource_dir}", resourceDir)
		out[i] = a
	}
	return out
}

// StartProcess starts the kernel process with cwd and optional env from spec.
func StartProcess(spec Spec, connectionFile, cwd string) (*exec.Cmd, error) {
	argv := ExpandArgv(spec.Spec.Argv, connectionFile, spec.ResourceDir)
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty argv")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for k, v := range spec.Spec.Env {
		cmd.Env = append(cmd.Env, k+"="+os.ExpandEnv(v))
	}
	setProcessGroup(cmd)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
