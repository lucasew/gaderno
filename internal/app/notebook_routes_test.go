package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasew/gaderno/internal/document"
	"github.com/lucasew/gaderno/internal/session"
	"github.com/lucasew/gaderno/internal/store"
)

func TestAttachmentFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"analysis.ipynb", "analysis.ipynb"},
		{"sub/analysis.ipynb", "analysis.ipynb"},
		{"", "notebook.ipynb"},
		{".", "notebook.ipynb"},
		{"..", "notebook.ipynb"},
		{`evil"name.ipynb`, "evilname.ipynb"},
		{"noext", "noext.ipynb"},
		{"dir/My Notebook.ipynb", "My Notebook.ipynb"},
	}
	for _, tc := range cases {
		if got := attachmentFilename(tc.in); got != tc.want {
			t.Errorf("attachmentFilename(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
	if !strings.Contains(attachmentDisposition("x.ipynb"), `filename="x.ipynb"`) {
		t.Fatal(attachmentDisposition("x.ipynb"))
	}
}

func TestLoadCurrentNotebookPrefersLiveCRDT(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	nb := document.NewEmpty()
	nb.Cells[0].Source = document.NewMultiline("print('disk')\n")
	if err := st.Save(context.Background(), "n.ipynb", nb); err != nil {
		t.Fatal(err)
	}

	reg := session.NewRegistry(st, dir, "")
	defer reg.CloseAll(context.Background())

	hub, err := reg.GetOrOpen(context.Background(), "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	// Mutate live source without saving to disk.
	cellID := hub.Doc.ProjectNotebook().Cells[0].ID
	if err := hub.SetCellSource(cellID, "print('live')\n", ""); err != nil {
		t.Fatal(err)
	}

	// Disk still has old content.
	disk, err := st.Load(context.Background(), "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	if disk.Cells[0].SourceString() != "print('disk')\n" {
		t.Fatalf("disk mutated early: %q", disk.Cells[0].SourceString())
	}

	got, err := loadCurrentNotebook(context.Background(), st, reg, "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	if src := got.Cells[0].SourceString(); src != "print('live')\n" {
		t.Fatalf("export/API should project live CRDT, got %q", src)
	}

	// Sanity: on-disk file still old until Save.
	raw, err := os.ReadFile(filepath.Join(dir, "n.ipynb"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "print('live')") {
		t.Fatal("disk should not have live edit yet")
	}
}

func TestLoadCurrentNotebookMissing(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	reg := session.NewRegistry(st, dir, "")
	defer reg.CloseAll(context.Background())
	_, err := loadCurrentNotebook(context.Background(), st, reg, "missing.ipynb")
	if err == nil || !store.IsNotExist(err) {
		t.Fatalf("want not-exist, got %v", err)
	}
}
