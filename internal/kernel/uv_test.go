package kernel

import (
	"testing"
)

func TestParseUVPythonListDedupe(t *testing.T) {
	text := `
cpython-3.13.7-linux-x86_64-gnu                   /path/a
cpython-3.13.7-linux-x86_64-gnu                   /path/b
cpython-3.12.12-linux-x86_64-gnu                  <download available>
pypy-3.11.15-linux-x86_64-gnu                     <download available>
`
	keys := parseUVPythonList(text)
	if len(keys) != 3 {
		t.Fatalf("keys %v", keys)
	}
	if keys[0] != "cpython-3.13.7-linux-x86_64-gnu" {
		t.Fatal(keys[0])
	}
}

func TestUVKernelName(t *testing.T) {
	cases := map[string]string{
		"cpython-3.13.7-linux-x86_64-gnu":              "uv-cpython-3.13.7",
		"cpython-3.14.6+freethreaded-linux-x86_64-gnu": "uv-cpython-3.14.6-freethreaded",
		"pypy-3.11.15-linux-x86_64-gnu":                "uv-pypy-3.11.15",
	}
	for in, want := range cases {
		if got := uvKernelName(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}
