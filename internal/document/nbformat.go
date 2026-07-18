package document

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Notebook is nbformat v4.
type Notebook struct {
	NBFormat      int            `json:"nbformat"`
	NBFormatMinor int            `json:"nbformat_minor"`
	Metadata      map[string]any `json:"metadata"`
	Cells         []Cell         `json:"cells"`
}

// CellType is a notebook cell kind.
type CellType string

const (
	CellCode     CellType = "code"
	CellMarkdown CellType = "markdown"
	CellRaw      CellType = "raw"
)

// Cell is a notebook cell.
type Cell struct {
	ID             string         `json:"id,omitempty"`
	CellType       CellType       `json:"cell_type"`
	Metadata       map[string]any `json:"metadata"`
	Source         *Multiline     `json:"source"`
	Outputs        []Output       `json:"outputs,omitempty"`
	ExecutionCount *int           `json:"execution_count,omitempty"`
}

// Output is a code cell output.
type Output struct {
	OutputType     string            `json:"output_type"`
	Name           string            `json:"name,omitempty"`
	Text           *Multiline        `json:"text,omitempty"`
	Data           map[string]any    `json:"data,omitempty"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
	Ename          string            `json:"ename,omitempty"`
	Evalue         string            `json:"evalue,omitempty"`
	Traceback      []string          `json:"traceback,omitempty"`
	ExecutionCount *int              `json:"execution_count,omitempty"`
	Transient      map[string]any    `json:"transient,omitempty"`
}

// Multiline is nbformat source/text: string or array of strings.
type Multiline struct {
	lines []string
}

// NewEmpty returns a minimal valid notebook with one empty code cell.
func NewEmpty() *Notebook {
	nb := &Notebook{
		NBFormat:      4,
		NBFormatMinor: 5,
		Metadata: map[string]any{
			"kernelspec": map[string]any{
				"display_name": "Python 3",
				"language":     "python",
				"name":         "python3",
			},
			"language_info": map[string]any{
				"name": "python",
			},
		},
		Cells: []Cell{
			{
				ID:       newCellID(),
				CellType: CellCode,
				Metadata: map[string]any{},
				Source:   NewMultiline(""),
			},
		},
	}
	return nb
}

// Decode parses nbformat JSON.
func Decode(raw []byte) (*Notebook, error) {
	var nb Notebook
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&nb); err != nil {
		return nil, fmt.Errorf("decode notebook: %w", err)
	}
	if nb.NBFormat != 4 {
		return nil, fmt.Errorf("unsupported nbformat %d", nb.NBFormat)
	}
	if nb.Metadata == nil {
		nb.Metadata = map[string]any{}
	}
	EnsureCellIDs(&nb)
	return &nb, nil
}

// Encode serializes notebook as pretty JSON with trailing newline.
func Encode(nb *Notebook) ([]byte, error) {
	EnsureCellIDs(nb)
	raw, err := json.MarshalIndent(nb, "", "  ")
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	return raw, nil
}

// EnsureCellIDs assigns ids to cells missing them (nbformat 4.5+).
func EnsureCellIDs(nb *Notebook) {
	if nb == nil {
		return
	}
	if nb.NBFormatMinor < 5 {
		nb.NBFormatMinor = 5
	}
	seen := map[string]bool{}
	for i := range nb.Cells {
		id := nb.Cells[i].ID
		if id == "" || seen[id] {
			// Missing or duplicate IDs bind multiple editors to the same Y.Text.
			id = newCellID()
			nb.Cells[i].ID = id
		}
		seen[id] = true
		if nb.Cells[i].Metadata == nil {
			nb.Cells[i].Metadata = map[string]any{}
		}
		if nb.Cells[i].Source == nil {
			nb.Cells[i].Source = NewMultiline("")
		}
	}
}

// NewCellID returns a fresh cell id (for structure ops).
func NewCellID() string { return newCellID() }

// SourceString returns cell source as a single string.
func (c Cell) SourceString() string {
	if c.Source == nil {
		return ""
	}
	return c.Source.String()
}

// OutputPlain is a rough text view of an output for HTML listing.
func OutputPlain(o Output) string {
	switch o.OutputType {
	case "stream":
		if o.Text != nil {
			return o.Text.String()
		}
	case "error":
		return o.Ename + ": " + o.Evalue
	case "execute_result", "display_data":
		if o.Data != nil {
			if v, ok := o.Data["text/plain"]; ok {
				return anyString(v)
			}
		}
	}
	return o.OutputType
}

func anyString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var b strings.Builder
		for _, x := range t {
			b.WriteString(fmt.Sprint(x))
		}
		return b.String()
	default:
		return fmt.Sprint(v)
	}
}

func newCellID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// NewMultiline builds Multiline from a full source string.
func NewMultiline(s string) *Multiline {
	if s == "" {
		return &Multiline{lines: nil}
	}
	// Prefer single string form when encoding small sources; store as one piece.
	return &Multiline{lines: []string{s}}
}

func (m *Multiline) String() string {
	if m == nil || len(m.lines) == 0 {
		return ""
	}
	return strings.Join(m.lines, "")
}

// UnmarshalJSON accepts string or []string.
func (m *Multiline) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		m.lines = nil
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		m.lines = []string{s}
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	m.lines = arr
	return nil
}

// MarshalJSON emits a single string when possible, else array of lines.
func (m *Multiline) MarshalJSON() ([]byte, error) {
	if m == nil || len(m.lines) == 0 {
		return json.Marshal("")
	}
	if len(m.lines) == 1 {
		return json.Marshal(m.lines[0])
	}
	return json.Marshal(m.lines)
}
