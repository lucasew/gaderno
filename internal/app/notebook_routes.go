package app

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucasew/gaderno/internal/document"
	"github.com/lucasew/gaderno/internal/session"
	"github.com/lucasew/gaderno/internal/store"
)

var notebookPage = loadTemplate("notebook.html")

func registerNotebookRoutes(mux *http.ServeMux, st *store.Store, reg *session.Registry, defaultKernel string, logger *slog.Logger) {
	mux.HandleFunc("GET /n/{path...}", func(w http.ResponseWriter, r *http.Request) {
		path := r.PathValue("path")
		nb, err := loadCurrentNotebook(r.Context(), st, reg, path)
		if err != nil {
			if store.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			logger.Error("load notebook", "path", path, "err", err)
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		type cellView struct {
			Type       string
			ID         string
			Source     string
			SourceJSON template.JS
		}
		var cells []cellView
		for _, c := range nb.Cells {
			src := c.SourceString()
			raw, _ := json.Marshal(src)
			cells = append(cells, cellView{
				Type:       string(c.CellType),
				ID:         c.ID,
				Source:     src,
				SourceJSON: template.JS(raw),
			})
		}
		pathJSON, _ := json.Marshal(path)
		kernelJSON, _ := json.Marshal(defaultKernel)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := notebookPage.Execute(w, map[string]any{
			"Path":       path,
			"PathJSON":   template.JS(pathJSON),
			"KernelJSON": template.JS(kernelJSON),
			"Cells":      cells,
		}); err != nil {
			logger.Error("render notebook", "err", err)
		}
	})

	mux.HandleFunc("POST /api/save", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
			http.Error(w, "path required", http.StatusBadRequest)
			return
		}
		hub, err := reg.GetOrOpen(r.Context(), body.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if err := hub.Save(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/execute", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path   string `json:"path"`
			CellID string `json:"cell_id"`
			Kernel string `json:"kernel"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" || body.CellID == "" {
			http.Error(w, "path and cell_id required", http.StatusBadRequest)
			return
		}
		hub, err := reg.GetOrOpen(r.Context(), body.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		if err := hub.EnsureKernel(ctx, body.Kernel); err != nil {
			http.Error(w, "kernel: "+err.Error(), http.StatusBadGateway)
			return
		}
		res, err := hub.ExecuteCell(ctx, body.CellID, nil, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})

	// JSON or download of the *current* notebook: live CRDT projection when a
	// hub is open (or can be opened), matching the page SSR path. Disk alone
	// lags debounced saves and drops in-flight edits from Export.
	mux.HandleFunc("GET /api/notebooks/{path...}", func(w http.ResponseWriter, r *http.Request) {
		path := r.PathValue("path")
		nb, err := loadCurrentNotebook(r.Context(), st, reg, path)
		if err != nil {
			if store.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if r.URL.Query().Get("download") == "1" {
			raw, err := document.Encode(nb)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/x-ipynb+json")
			w.Header().Set("Content-Disposition", attachmentDisposition(path))
			_, _ = w.Write(raw)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(nb)
	})
}

// loadCurrentNotebook returns the live CRDT projection when the hub can be
// opened, otherwise the on-disk snapshot (same policy as notebook SSR).
func loadCurrentNotebook(ctx context.Context, st *store.Store, reg *session.Registry, path string) (*document.Notebook, error) {
	if hub, err := reg.GetOrOpen(ctx, path); err == nil {
		return hub.Doc.ProjectNotebook(), nil
	} else if !store.IsNotExist(err) {
		// Hub open failed for a reason other than missing file — still try disk.
		if nb, loadErr := st.Load(ctx, path); loadErr == nil {
			return nb, nil
		}
		return nil, err
	}
	return st.Load(ctx, path)
}

// attachmentDisposition builds a Content-Disposition header using the notebook
// basename so Export downloads keep the real name (not a generic notebook.ipynb).
func attachmentDisposition(path string) string {
	return fmt.Sprintf(`attachment; filename="%s"`, attachmentFilename(path))
}

func attachmentFilename(path string) string {
	name := filepath.Base(strings.TrimSpace(path))
	name = strings.ReplaceAll(name, `"`, "")
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", "")
	if name == "" || name == "." || name == ".." {
		return "notebook.ipynb"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".ipynb") {
		name += ".ipynb"
	}
	return name
}
