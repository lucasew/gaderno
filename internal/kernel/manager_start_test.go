package kernel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStartFailsFastWhenProcessExitsEarly ensures a kernelspec whose argv
// exits immediately does not burn the full dial timeout. Before the fix,
// cmd.ProcessState was never set (no Wait), so Start retried Dial for up to
// 2 minutes against a dead process.
func TestStartFailsFastWhenProcessExitsEarly(t *testing.T) {
	root := t.TempDir()
	kdir := filepath.Join(root, "kernels", "gaderno-exit")
	if err := os.MkdirAll(kdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// /bin/false — no ZMQ server, process dies at once.
	kj := SpecFile{
		Argv:        []string{"/bin/false"},
		DisplayName: "gaderno-exit",
		Language:    "python",
	}
	raw, err := json.Marshal(kj)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kdir, "kernel.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JUPYTER_PATH", root)
	t.Setenv("JUPYTER_DATA_DIR", filepath.Join(t.TempDir(), "empty"))
	// Avoid shelling out to `uv python list` (seconds) so the timer measures
	// the dial fail-fast path, not catalog discovery.
	t.Setenv("PATH", "/bin:/usr/bin")
	ResetCatalogForTest()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	_, err = Start(ctx, "gaderno-exit", t.TempDir())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected Start to fail when kernel argv exits immediately")
	}
	if !strings.Contains(err.Error(), "exited early") {
		t.Fatalf("want exited early error, got: %v", err)
	}
	// Must be far below the 2-minute dial ceiling; instant-exit argv should
	// surface within a second of Wait completing (no multi-socket dial wait).
	if elapsed > 2*time.Second {
		t.Fatalf("Start took %v; expected fail-fast well under 2s", elapsed)
	}
	t.Logf("failed in %v: %v", elapsed, err)
}
