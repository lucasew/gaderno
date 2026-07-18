package crdt

import (
	"testing"

	"github.com/lucasew/gaderno/internal/document"
)

func TestInsertMoveDelete(t *testing.T) {
	d := New()
	nb := document.NewEmpty()
	if err := d.LoadFromNotebook(nb); err != nil {
		t.Fatal(err)
	}
	ids := d.CellIDs()
	if len(ids) != 1 {
		t.Fatalf("want 1 got %v", ids)
	}
	id2, err := d.InsertCell(1, document.CellMarkdown, "# hi")
	if err != nil {
		t.Fatal(err)
	}
	ids = d.CellIDs()
	if len(ids) != 2 || ids[1] != id2 {
		t.Fatalf("%v", ids)
	}
	if err := d.MoveCell(id2, 0); err != nil {
		t.Fatal(err)
	}
	ids = d.CellIDs()
	if ids[0] != id2 {
		t.Fatalf("move %v", ids)
	}
	if err := d.DeleteCell(id2); err != nil {
		t.Fatal(err)
	}
	ids = d.CellIDs()
	if len(ids) != 1 {
		t.Fatalf("after delete %v", ids)
	}
	// ProjectNotebook must not invent cells
	proj := d.ProjectNotebook()
	if len(proj.Cells) != 1 {
		t.Fatalf("project %d", len(proj.Cells))
	}
}

func TestMoveMultiple(t *testing.T) {
	d := New()
	nb := document.NewEmpty()
	nb.Cells[0].Source = document.NewMultiline("a")
	if err := d.LoadFromNotebook(nb); err != nil {
		t.Fatal(err)
	}
	idB, err := d.InsertCell(1, document.CellCode, "b")
	if err != nil {
		t.Fatal(err)
	}
	idC, err := d.InsertCell(2, document.CellCode, "c")
	if err != nil {
		t.Fatal(err)
	}
	ids := d.CellIDs()
	if len(ids) != 3 {
		t.Fatalf("want 3 got %v", ids)
	}
	idA := ids[0]
	// move last to front: C,A,B
	if err := d.MoveCell(idC, 0); err != nil {
		t.Fatal(err)
	}
	ids = d.CellIDs()
	if ids[0] != idC || ids[1] != idA || ids[2] != idB {
		t.Fatalf("after move to 0: %v want %v %v %v", ids, idC, idA, idB)
	}
	// move middle down: C,B,A
	if err := d.MoveCell(idA, 2); err != nil {
		t.Fatal(err)
	}
	ids = d.CellIDs()
	if ids[0] != idC || ids[1] != idB || ids[2] != idA {
		t.Fatalf("after move A to 2: %v", ids)
	}
	// sources follow ids
	if d.Source(idA) != "a" || d.Source(idB) != "b" || d.Source(idC) != "c" {
		t.Fatalf("sources corrupted a=%q b=%q c=%q", d.Source(idA), d.Source(idB), d.Source(idC))
	}
	snap := d.SnapshotCells()
	if len(snap) != 3 || snap[0].ID != idC || snap[0].Source != "c" {
		t.Fatalf("snapshot %+v", snap)
	}
}

func TestEnsureUniqueIDs(t *testing.T) {
	nb := &document.Notebook{
		NBFormat: 4,
		Cells: []document.Cell{
			{ID: "same", CellType: document.CellCode, Source: document.NewMultiline("a")},
			{ID: "same", CellType: document.CellCode, Source: document.NewMultiline("b")},
		},
	}
	document.EnsureCellIDs(nb)
	if nb.Cells[0].ID == nb.Cells[1].ID {
		t.Fatal("duplicate ids not fixed")
	}
}
