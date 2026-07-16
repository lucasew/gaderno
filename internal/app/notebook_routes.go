package app

import (
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/lucasew/gaderno/internal/document"
	"github.com/lucasew/gaderno/internal/session"
	"github.com/lucasew/gaderno/internal/store"
)

var notebookPage = loadTemplate("notebook.html")

func registerNotebookRoutes(mux *http.ServeMux, st *store.Store, reg *session.Registry, defaultKernel string, logger *slog.Logger) {
	mux.HandleFunc("GET /n/{path...}", func(w http.ResponseWriter, r *http.Request) {
		path := r.PathValue("path")
		var nb *document.Notebook
		if hub, err := reg.GetOrOpen(r.Context(), path); err == nil {
			nb = hub.Doc.ProjectNotebook()
		} else {
			var err2 error
			nb, err2 = st.Load(r.Context(), path)
			if err2 != nil {
				if store.IsNotExist(err2) {
					http.NotFound(w, r)
					return
				}
				logger.Error("load notebook", "path", path, "err", err2)
				http.Error(w, "load failed", http.StatusInternalServerError)
				return
			}
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
		res, err := hub.ExecuteCell(ctx, body.CellID, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})

	mux.HandleFunc("GET /api/notebooks/{path...}", func(w http.ResponseWriter, r *http.Request) {
		path := r.PathValue("path")
		nb, err := st.Load(r.Context(), path)
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
			w.Header().Set("Content-Disposition", `attachment; filename="notebook.ipynb"`)
			_, _ = w.Write(raw)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(nb)
	})
}
