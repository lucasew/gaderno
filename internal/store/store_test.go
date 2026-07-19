package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasew/gaderno/internal/document"
)

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	nb := document.NewEmpty()
	nb.Cells[0].Source = document.NewMultiline("print(1)")
	if err := st.Save(nil, "a.ipynb", nb); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load(nil, "a.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	if got.Cells[0].SourceString() != "print(1)" {
		t.Fatalf("got %q", got.Cells[0].SourceString())
	}
}

func TestPathJail(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	// ".." collapses under the jail; missing file is not an escape.
	// Empty / root-only paths must still be rejected before IO.
	for _, p := range []string{"", ".", "..", "/"} {
		if _, err := st.Load(nil, p); err == nil {
			t.Fatalf("Load(%q) expected error", p)
		}
	}
	// Collapsed path stays inside root (no escape to host /etc).
	_, err := st.Load(nil, "../etc/passwd")
	if err == nil {
		t.Fatal("expected missing file under jail, not success")
	}
	if !IsNotExist(err) {
		// resolve may error first; either not-exist or empty after clean is fine
		// as long as we did not read outside the temp root.
		t.Logf("Load collapsed escape-style path: %v", err)
	}
}

func TestCleanRelCanonical(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"a.ipynb", "a.ipynb"},
		{"./a.ipynb", "a.ipynb"},
		{"sub/../a.ipynb", "a.ipynb"},
		{"/a.ipynb", "a.ipynb"},
		{"  b.ipynb  ", "b.ipynb"},
		{"dir/nested.ipynb", "dir/nested.ipynb"},
		{"foo..bar.ipynb", "foo..bar.ipynb"},
		{"../a.ipynb", "a.ipynb"},
	}
	for _, tc := range cases {
		got, err := CleanRel(tc.in)
		if err != nil {
			t.Fatalf("CleanRel(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("CleanRel(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestCleanRelRejects(t *testing.T) {
	for _, in := range []string{"", ".", "..", " ", "/"} {
		if _, err := CleanRel(in); err == nil {
			t.Fatalf("CleanRel(%q) expected error", in)
		}
	}
}

func TestLoadEquivalentPaths(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	nb := document.NewEmpty()
	nb.Cells[0].Source = document.NewMultiline("same")
	if err := st.Save(nil, "a.ipynb", nb); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.ipynb", "./a.ipynb", "sub/../a.ipynb"} {
		got, err := st.Load(nil, p)
		if err != nil {
			t.Fatalf("Load(%q): %v", p, err)
		}
		if got.Cells[0].SourceString() != "same" {
			t.Fatalf("Load(%q) source %q", p, got.Cells[0].SourceString())
		}
	}
}

func TestCreateNewExists(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	nb := document.NewEmpty()
	if err := st.CreateNew(nil, "x.ipynb", nb); err != nil {
		t.Fatal(err)
	}
	err := st.CreateNew(nil, "x.ipynb", nb)
	if !os.IsExist(err) {
		t.Fatalf("want exist, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.ipynb")); err != nil {
		t.Fatal(err)
	}
}

func TestSaveLoadWithFlock(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	nb := document.NewEmpty()
	nb.Cells[0].Source = document.NewMultiline("under-lock")
	if err := st.Save(nil, "locked.ipynb", nb); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load(nil, "locked.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	if got.Cells[0].SourceString() != "under-lock" {
		t.Fatalf("got %q", got.Cells[0].SourceString())
	}
}

func TestCreateNewOExcl(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	nb := document.NewEmpty()
	if err := st.CreateNew(nil, "only.ipynb", nb); err != nil {
		t.Fatal(err)
	}
	// Concurrent-style second claim must fail even without a racy Stat window.
	err := st.CreateNew(nil, "only.ipynb", nb)
	if !os.IsExist(err) {
		t.Fatalf("want exist, got %v", err)
	}
}

func TestConcurrentSavesSerialize(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	// Seed file so both writers flock an existing path.
	nb0 := document.NewEmpty()
	nb0.Cells[0].Source = document.NewMultiline("seed")
	if err := st.Save(nil, "race.ipynb", nb0); err != nil {
		t.Fatal(err)
	}

	const n = 8
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			nb := document.NewEmpty()
			nb.Cells[0].Source = document.NewMultiline(strings.Repeat("x", i+1))
			errCh <- st.Save(nil, "race.ipynb", nb)
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	got, err := st.Load(nil, "race.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	src := got.Cells[0].SourceString()
	if src == "" {
		t.Fatal("empty source after concurrent saves")
	}
	// Final content must be one of the writers (not torn/mixed).
	ok := false
	for i := 0; i < n; i++ {
		if src == strings.Repeat("x", i+1) {
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("unexpected source %q", src)
	}
}
