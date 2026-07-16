package crdt

import (
	"encoding/json"
	"fmt"

	"github.com/lucasew/gaderno/internal/document"
	ycrdt "github.com/reearth/ygo/crdt"
)

// Root type names in the shared Y.Doc.
const (
	RootCells    = "cells"    // Y.Array of cell id strings
	RootCellData = "cellData" // Y.Map flattened "id.field" keys
	RootMeta     = "meta"     // Y.Map notebook metadata
)

// Origins used for Transact so observers can distinguish writers.
const (
	OriginServer = "gaderno.server"
	OriginLoad   = "gaderno.load"
)

// NotebookDoc wraps a ygo document with notebook helpers.
type NotebookDoc struct {
	Doc *ycrdt.Doc
}

// New empty collaborative notebook document.
func New() *NotebookDoc {
	return &NotebookDoc{Doc: ycrdt.New()}
}

// ApplyUpdate applies a Yjs binary update.
func (n *NotebookDoc) ApplyUpdate(update []byte) error {
	return n.Doc.ApplyUpdate(update)
}

// EncodeStateAsUpdate returns full document state as a Yjs update.
func (n *NotebookDoc) EncodeStateAsUpdate() []byte {
	return n.Doc.EncodeStateAsUpdate()
}

func sourceKey(cellID string) string {
	return "source:" + cellID
}

// LoadFromNotebook populates the CRDT from an ipynb model (fresh doc expected).
// Clears cells order first so reloads never duplicate ids.
func (n *NotebookDoc) LoadFromNotebook(nb *document.Notebook) error {
	if nb == nil {
		return fmt.Errorf("nil notebook")
	}
	document.EnsureCellIDs(nb)

	meta := n.Doc.GetMap(RootMeta)
	cells := n.Doc.GetArray(RootCells)
	cellData := n.Doc.GetMap(RootCellData)

	sources := make(map[string]*ycrdt.YText, len(nb.Cells))
	for i := range nb.Cells {
		id := nb.Cells[i].ID
		sources[id] = n.Doc.GetText(sourceKey(id))
	}

	return n.Doc.TransactE(func(txn *ycrdt.Transaction) error {
		// Clear cell order (never double-push on load).
		if cells.Len() > 0 {
			cells.Delete(txn, 0, cells.Len())
		}
		for k, v := range flattenMeta(nb.Metadata) {
			meta.Set(txn, k, v)
		}
		for i := range nb.Cells {
			c := nb.Cells[i]
			id := c.ID
			cells.Push(txn, []any{id})
			cellData.Set(txn, id+".type", string(c.CellType))
			cellData.Set(txn, id+".status", "idle")
			if c.ExecutionCount != nil {
				cellData.Set(txn, id+".execution_count", float64(*c.ExecutionCount))
			}
			outs, _ := json.Marshal(c.Outputs)
			cellData.Set(txn, id+".outputs_json", string(outs))

			st := sources[id]
			// Replace source entirely (load is authoritative).
			if n := st.Len(); n > 0 {
				st.Delete(txn, 0, n)
			}
			if s := c.SourceString(); s != "" {
				st.Insert(txn, 0, s, nil)
			}
		}
		return nil
	}, OriginLoad)
}

// Source returns cell source text.
func (n *NotebookDoc) Source(cellID string) string {
	if cellID == "" {
		return ""
	}
	return n.Doc.GetText(sourceKey(cellID)).ToString()
}

// CellIDs returns ordered unique cell ids (skips empty / duplicates).
func (n *NotebookDoc) CellIDs() []string {
	arr := n.Doc.GetArray(RootCells)
	out := make([]string, 0, arr.Len())
	seen := map[string]bool{}
	for i := 0; i < arr.Len(); i++ {
		v := arr.Get(i)
		s, ok := v.(string)
		if !ok || s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// ProjectNotebook builds an ipynb snapshot from CRDT state.
// Never invents a new notebook with fresh random cells when empty.
func (n *NotebookDoc) ProjectNotebook() *document.Notebook {
	nb := &document.Notebook{
		NBFormat:      4,
		NBFormatMinor: 5,
		Metadata:      map[string]any{},
		Cells:         []document.Cell{},
	}
	meta := n.Doc.GetMap(RootMeta)
	for _, k := range meta.Keys() {
		if v, ok := meta.Get(k); ok {
			nb.Metadata[k] = v
		}
	}
	cellData := n.Doc.GetMap(RootCellData)
	for _, id := range n.CellIDs() {
		c := document.Cell{
			ID:       id,
			CellType: document.CellCode,
			Metadata: map[string]any{},
			Source:   document.NewMultiline(n.Source(id)),
		}
		if t, ok := cellData.Get(id + ".type"); ok {
			if s, ok := t.(string); ok && s != "" {
				c.CellType = document.CellType(s)
			}
		}
		nb.Cells = append(nb.Cells, c)
	}
	return nb
}

// SetSourceServer replaces cell source (server-side writer). Prefer yjs collab for live typing.
func (n *NotebookDoc) SetSourceServer(cellID, source string) error {
	if cellID == "" {
		return fmt.Errorf("empty cell id")
	}
	st := n.Doc.GetText(sourceKey(cellID))
	return n.Doc.TransactE(func(txn *ycrdt.Transaction) error {
		if n := st.Len(); n > 0 {
			st.Delete(txn, 0, n)
		}
		if source != "" {
			st.Insert(txn, 0, source, nil)
		}
		return nil
	}, OriginServer)
}

// InsertCell inserts a cell at index (0..len). Returns new cell id.
func (n *NotebookDoc) InsertCell(index int, cellType document.CellType, source string) (string, error) {
	if cellType == "" {
		cellType = document.CellCode
	}
	id := document.NewCellID()
	cells := n.Doc.GetArray(RootCells)
	cellData := n.Doc.GetMap(RootCellData)
	st := n.Doc.GetText(sourceKey(id))

	err := n.Doc.TransactE(func(txn *ycrdt.Transaction) error {
		n := cells.Len()
		if index < 0 {
			index = 0
		}
		if index > n {
			index = n
		}
		if index >= n {
			cells.Push(txn, []any{id})
		} else {
			cells.Insert(txn, index, []any{id})
		}
		cellData.Set(txn, id+".type", string(cellType))
		cellData.Set(txn, id+".status", "idle")
		cellData.Set(txn, id+".outputs_json", "[]")
		if source != "" {
			st.Insert(txn, 0, source, nil)
		}
		return nil
	}, OriginServer)
	return id, err
}

// DeleteCell removes a cell by id from order (source text may remain orphaned; ok for yjs).
func (n *NotebookDoc) DeleteCell(cellID string) error {
	if cellID == "" {
		return fmt.Errorf("empty cell id")
	}
	ids := n.CellIDs()
	idx := -1
	for i, id := range ids {
		if id == cellID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("cell %q not found", cellID)
	}
	cells := n.Doc.GetArray(RootCells)
	return n.Doc.TransactE(func(txn *ycrdt.Transaction) error {
		cells.Delete(txn, idx, 1)
		return nil
	}, OriginServer)
}

// MoveCell moves cellID to new index (0..len-1).
// Index lookup is outside Transact — ygo Get takes RLock and deadlocks under write txn.
//
// Implemented as delete+insert rather than YArray.Move: ygo's ContentMove path
// fails on subsequent "move up" after a prior move (visible order stalls).
// Delete+insert is correct for a single server-king writer of cell order.
func (n *NotebookDoc) MoveCell(cellID string, toIndex int) error {
	if cellID == "" {
		return fmt.Errorf("empty cell id")
	}
	ids := n.CellIDs()
	from := -1
	for i, id := range ids {
		if id == cellID {
			from = i
			break
		}
	}
	if from < 0 {
		return fmt.Errorf("cell %q not found", cellID)
	}
	if len(ids) == 0 {
		return nil
	}
	if toIndex < 0 {
		toIndex = 0
	}
	if toIndex >= len(ids) {
		toIndex = len(ids) - 1
	}
	if from == toIndex {
		return nil
	}
	cells := n.Doc.GetArray(RootCells)
	return n.Doc.TransactE(func(txn *ycrdt.Transaction) error {
		cells.Delete(txn, from, 1)
		// After delete, insert at the desired final index (same as splice semantics).
		if toIndex >= cells.Len() {
			cells.Push(txn, []any{cellID})
		} else {
			cells.Insert(txn, toIndex, []any{cellID})
		}
		return nil
	}, OriginServer)
}

// SetCellType updates cell type metadata.
func (n *NotebookDoc) SetCellType(cellID string, cellType document.CellType) error {
	if cellID == "" {
		return fmt.Errorf("empty cell id")
	}
	cellData := n.Doc.GetMap(RootCellData)
	return n.Doc.TransactE(func(txn *ycrdt.Transaction) error {
		cellData.Set(txn, cellID+".type", string(cellType))
		return nil
	}, OriginServer)
}

// CellSnapshot is a JSON-friendly cell for structure broadcasts / UI rebuild.
type CellSnapshot struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Source string `json:"source"`
}

// SnapshotCells returns ordered cells for the UI.
func (n *NotebookDoc) SnapshotCells() []CellSnapshot {
	nb := n.ProjectNotebook()
	out := make([]CellSnapshot, 0, len(nb.Cells))
	for _, c := range nb.Cells {
		out = append(out, CellSnapshot{
			ID:     c.ID,
			Type:   string(c.CellType),
			Source: c.SourceString(),
		})
	}
	return out
}

func flattenMeta(m map[string]any) map[string]any {
	out := map[string]any{}
	if m == nil {
		return out
	}
	for k, v := range m {
		switch v.(type) {
		case string, bool, float64, int, int64, nil:
			out[k] = v
		default:
			b, err := json.Marshal(v)
			if err != nil {
				continue
			}
			out[k+"_json"] = string(b)
		}
	}
	return out
}
