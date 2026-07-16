package crdt

import (
	"testing"

	"github.com/lucasew/gaderno/internal/document"
)

func TestMoveUpSequence(t *testing.T) {
	d := New()
	nb := document.NewEmpty()
	nb.Cells[0].Source = document.NewMultiline("A")
	if err := d.LoadFromNotebook(nb); err != nil {
		t.Fatal(err)
	}
	idB, err := d.InsertCell(1, document.CellCode, "B")
	if err != nil {
		t.Fatal(err)
	}
	idC, err := d.InsertCell(2, document.CellCode, "C")
	if err != nil {
		t.Fatal(err)
	}
	ids := d.CellIDs()
	idA := ids[0]
	t.Logf("start %v", ids)

	// Move C up (index 2 -> 1): expect A,C,B
	if err := d.MoveCell(idC, 1); err != nil {
		t.Fatal(err)
	}
	ids = d.CellIDs()
	t.Logf("C to 1 %v sources %q %q %q", ids, d.Source(ids[0]), d.Source(ids[1]), d.Source(ids[2]))
	if ids[0] != idA || ids[1] != idC || ids[2] != idB {
		t.Fatalf("want A C B got %v", ids)
	}

	// Move C up again (index 1 -> 0): expect C,A,B
	if err := d.MoveCell(idC, 0); err != nil {
		t.Fatal(err)
	}
	ids = d.CellIDs()
	t.Logf("C to 0 %v", ids)
	if ids[0] != idC || ids[1] != idA || ids[2] != idB {
		t.Fatalf("want C A B got %v", ids)
	}

	// Move B up from 2 to 1: C,B,A
	if err := d.MoveCell(idB, 1); err != nil {
		t.Fatal(err)
	}
	ids = d.CellIDs()
	t.Logf("B to 1 %v", ids)
	if ids[0] != idC || ids[1] != idB || ids[2] != idA {
		t.Fatalf("want C B A got %v", ids)
	}
}

func TestMoveUpAdjacent(t *testing.T) {
	d := New()
	nb := document.NewEmpty()
	nb.Cells[0].Source = document.NewMultiline("A")
	_ = d.LoadFromNotebook(nb)
	idB, _ := d.InsertCell(1, document.CellCode, "B")
	ids := d.CellIDs()
	idA := ids[0]
	// move B up to 0
	if err := d.MoveCell(idB, 0); err != nil {
		t.Fatal(err)
	}
	ids = d.CellIDs()
	if ids[0] != idB || ids[1] != idA {
		t.Fatalf("want B A got %v sources %q %q", ids, d.Source(ids[0]), d.Source(ids[1]))
	}
}
