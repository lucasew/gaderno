package session

import (
	"context"
	"testing"

	"github.com/lucasew/gaderno/internal/document"
	"github.com/lucasew/gaderno/internal/store"
)

func TestOpenSave(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	nb := document.NewEmpty()
	nb.Cells[0].Source = document.NewMultiline("x = 1")
	if err := st.Save(context.Background(), "n.ipynb", nb); err != nil {
		t.Fatal(err)
	}
	h, err := Open(context.Background(), st, dir, "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	ids := h.Doc.CellIDs()
	if len(ids) != 1 {
		t.Fatal(ids)
	}
	if h.Doc.Source(ids[0]) != "x = 1" {
		t.Fatal(h.Doc.Source(ids[0]))
	}
	if err := h.Doc.SetSourceServer(ids[0], "x = 2"); err != nil {
		t.Fatal(err)
	}
	if err := h.Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load(context.Background(), "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	if got.Cells[0].SourceString() != "x = 2" {
		t.Fatalf("%q", got.Cells[0].SourceString())
	}
}
