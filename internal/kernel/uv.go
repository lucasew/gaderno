package kernel

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var (
	uvOnce   sync.Once
	uvSpecs  []Spec
	uvLoaded bool
)

func resetUVCache() {
	uvOnce = sync.Once{}
	uvSpecs = nil
	uvLoaded = false
}

// listUVSynthetics returns in-memory kernelspecs from `uv python list`.
// Empty if uv is missing or listing fails.
func listUVSynthetics() []Spec {
	uvOnce.Do(func() {
		uvSpecs = loadUVSynthetics()
		uvLoaded = true
	})
	return uvSpecs
}

func loadUVSynthetics() []Spec {
	uvPath, err := exec.LookPath("uv")
	if err != nil {
		return nil
	}
	cmd := exec.Command(uvPath, "python", "list")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil
	}

	keys := parseUVPythonList(stdout.String())
	if len(keys) == 0 {
		return nil
	}

	uvDir := filepath.Dir(uvPath)
	var out []Spec
	seenName := map[string]bool{}
	for _, key := range keys {
		name := uvKernelName(key)
		if name == "" || seenName[name] {
			continue
		}
		seenName[name] = true
		request := uvPythonRequest(key)
		out = append(out, Spec{
			Name:        name,
			ResourceDir: "", // synthetic
			Spec: SpecFile{
				Argv: []string{
					uvPath, "run",
					"--python", request,
					"--with", "ipykernel",
					"--with", "pyzmq",
					"--no-project",
					"--isolated",
					"--refresh",
					"python", "-m", "ipykernel_launcher",
					"-f", "{connection_file}",
				},
				DisplayName: "uv · " + key,
				Language:    "python",
				Env: map[string]string{
					"PATH": uvDir + string(os.PathListSeparator) + "${PATH}",
				},
			},
		})
	}
	return out
}

// parseUVPythonList returns unique first-column keys from `uv python list` text.
func parseUVPythonList(text string) []string {
	var keys []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// first field
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		key := fields[0]
		if seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys
}

// uvKernelName maps a uv list key to a portable kernelspec name.
// e.g. cpython-3.13.7-linux-x86_64-gnu → uv-cpython-3.13.7
// cpython-3.14.6+freethreaded-linux-x86_64-gnu → uv-cpython-3.14.6-freethreaded
var uvKeyRE = regexp.MustCompile(`^([a-z0-9]+)-([0-9]+(?:\.[0-9]+){1,3})(\+([a-z0-9]+))?`)

func uvKernelName(key string) string {
	// strip platform suffix after last version/variant segment
	// keys look like: impl-version[-platform] or impl-version+variant-platform
	m := uvKeyRE.FindStringSubmatch(key)
	if m == nil {
		// fallback: sanitize full key
		safe := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
				return r
			}
			if r == '+' {
				return '-'
			}
			return -1
		}, strings.ToLower(key))
		if safe == "" {
			return ""
		}
		return "uv-" + safe
	}
	impl, ver, variant := m[1], m[2], m[4]
	name := fmt.Sprintf("uv-%s-%s", impl, ver)
	if variant != "" {
		name += "-" + variant
	}
	return name
}

// uvPythonRequest picks a --python argument uv accepts for this list key.
func uvPythonRequest(key string) string {
	// Full list key is accepted by uv for installed and downloadable rows.
	return key
}
