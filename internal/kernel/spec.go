package kernel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Spec is a Jupyter kernelspec (kernel.json + directory).
type Spec struct {
	Name        string
	ResourceDir string
	Spec        SpecFile
}

// SpecFile is the contents of kernel.json.
type SpecFile struct {
	Argv         []string          `json:"argv"`
	DisplayName  string            `json:"display_name"`
	Language     string            `json:"language"`
	InterruptMode string           `json:"interrupt_mode,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Metadata     map[string]any    `json:"metadata,omitempty"`
}

// Discover finds kernelspecs using Jupyter-compatible search paths.
// First match for a given name wins (paths ordered highest precedence first).
func Discover() ([]Spec, error) {
	var out []Spec
	seen := map[string]bool{}
	for _, dir := range searchPaths() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			// skip unreadable dirs
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if seen[name] {
				continue
			}
			res := filepath.Join(dir, name)
			raw, err := os.ReadFile(filepath.Join(res, "kernel.json"))
			if err != nil {
				continue
			}
			var sf SpecFile
			if err := json.Unmarshal(raw, &sf); err != nil {
				continue
			}
			if len(sf.Argv) == 0 {
				continue
			}
			seen[name] = true
			out = append(out, Spec{Name: name, ResourceDir: res, Spec: sf})
		}
	}
	return out, nil
}


// searchPaths mirrors jupyter_core.paths.jupyter_path("kernels") order:
// JUPYTER_PATH entries, then data dirs' kernels/ subdirs (user → env → system).
func searchPaths() []string {
	var paths []string
	if jp := os.Getenv("JUPYTER_PATH"); jp != "" {
		for _, p := range splitPathList(jp) {
			if p != "" {
				paths = append(paths, filepath.Join(p, "kernels"))
			}
		}
	}
	for _, d := range jupyterDataDirs() {
		paths = append(paths, filepath.Join(d, "kernels"))
	}
	return paths
}

// jupyterDataDirs approximates jupyter_core.paths.jupyter_data_dir list.
func jupyterDataDirs() []string {
	var dirs []string
	// 1. JUPYTER_DATA_DIR
	if d := os.Getenv("JUPYTER_DATA_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	// 2. User data dir
	dirs = append(dirs, userJupyterDataDir())
	// 3. Env/prefix share (sys.prefix/share/jupyter)
	if p := os.Getenv("VIRTUAL_ENV"); p != "" {
		dirs = append(dirs, filepath.Join(p, "share", "jupyter"))
	}
	if p := os.Getenv("CONDA_PREFIX"); p != "" {
		dirs = append(dirs, filepath.Join(p, "share", "jupyter"))
	}
	// Python sys.prefix is often under PATH's python — try common env
	if home, err := os.UserHomeDir(); err == nil {
		// mise/uv local envs not enumerated; rely on VIRTUAL_ENV/CONDA
		_ = home
	}
	// 4. System dirs
	switch runtime.GOOS {
	case "windows":
		if pd := os.Getenv("PROGRAMDATA"); pd != "" {
			dirs = append(dirs, filepath.Join(pd, "jupyter"))
		}
	case "darwin":
		dirs = append(dirs,
			"/usr/local/share/jupyter",
			"/usr/share/jupyter",
		)
	default:
		dirs = append(dirs,
			"/usr/local/share/jupyter",
			"/usr/share/jupyter",
		)
	}
	return uniqueExistingPreferOrder(dirs)
}

func userJupyterDataDir() string {
	if runtime.GOOS == "windows" {
		if app := os.Getenv("APPDATA"); app != "" {
			return filepath.Join(app, "jupyter")
		}
	}
	if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Jupyter")
		}
	}
	// Linux / XDG
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "jupyter")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "jupyter")
	}
	return ""
}

func splitPathList(p string) []string {
	sep := string(os.PathListSeparator)
	return strings.Split(p, sep)
}

func uniqueExistingPreferOrder(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range in {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}
