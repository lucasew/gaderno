package kernel

import (
	"fmt"
	"sort"
	"sync"
)

// Group names for the chooser UI.
const (
	GroupJupyter = "jupyter"
	GroupUV      = "uv"
)

// Entry is one selectable kernelspec in the catalog.
type Entry struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Language    string `json:"language"`
	Group       string `json:"group"` // jupyter | uv
}

// Catalog is Jupyter discovery plus optional uv synthetics.
type Catalog struct {
	entries []Entry
	byName  map[string]Spec
}

var (
	catalogOnce sync.Once
	catalogSnap *Catalog
	catalogErr  error
)

// LoadCatalog builds (once) the full kernelspec catalog for this process.
func LoadCatalog() (*Catalog, error) {
	catalogOnce.Do(func() {
		catalogSnap, catalogErr = buildCatalog()
	})
	return catalogSnap, catalogErr
}

// ResetCatalogForTest clears the process cache (tests only).
func ResetCatalogForTest() {
	catalogOnce = sync.Once{}
	catalogSnap = nil
	catalogErr = nil
	resetUVCache()
}

func buildCatalog() (*Catalog, error) {
	c := &Catalog{
		byName: make(map[string]Spec),
	}

	// Real on-disk kernels first (win name collisions).
	real, err := Discover()
	if err != nil {
		return nil, err
	}
	for _, s := range real {
		c.add(Entry{
			Name:        s.Name,
			DisplayName: displayOrName(s),
			Language:    s.Spec.Language,
			Group:       GroupJupyter,
		}, s)
	}

	// Optional uv synthetics (skip names already taken).
	for _, s := range listUVSynthetics() {
		if _, ok := c.byName[s.Name]; ok {
			continue
		}
		c.add(Entry{
			Name:        s.Name,
			DisplayName: displayOrName(s),
			Language:    s.Spec.Language,
			Group:       GroupUV,
		}, s)
	}

	sort.SliceStable(c.entries, func(i, j int) bool {
		if c.entries[i].Group != c.entries[j].Group {
			// jupyter before uv
			return c.entries[i].Group < c.entries[j].Group
		}
		return c.entries[i].DisplayName < c.entries[j].DisplayName
	})
	return c, nil
}

func (c *Catalog) add(e Entry, s Spec) {
	c.entries = append(c.entries, e)
	c.byName[e.Name] = s
}

// Entries returns a copy of catalog entries for the API.
func (c *Catalog) Entries() []Entry {
	out := make([]Entry, len(c.entries))
	copy(out, c.entries)
	return out
}

// Groups returns entries bucketed for the chooser (omits empty groups).
func (c *Catalog) Groups() map[string][]Entry {
	out := map[string][]Entry{}
	for _, e := range c.entries {
		out[e.Group] = append(out[e.Group], e)
	}
	return out
}

// Has reports whether name is available.
func (c *Catalog) Has(name string) bool {
	_, ok := c.byName[name]
	return ok
}

// Spec returns the kernelspec for name.
func (c *Catalog) Spec(name string) (Spec, error) {
	s, ok := c.byName[name]
	if !ok {
		return Spec{}, fmt.Errorf("kernelspec %q not found", name)
	}
	return s, nil
}

func displayOrName(s Spec) string {
	if s.Spec.DisplayName != "" {
		return s.Spec.DisplayName
	}
	return s.Name
}

// Find resolves a kernelspec by name from the unified catalog.
func Find(name string) (Spec, error) {
	c, err := LoadCatalog()
	if err != nil {
		return Spec{}, err
	}
	return c.Spec(name)
}
