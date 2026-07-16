package app

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/lucasew/gaderno/internal/kernel"
	"github.com/lucasew/gaderno/internal/session"
)

func registerKernelRoutes(mux *http.ServeMux, reg *session.Registry, logger *slog.Logger) {
	mux.HandleFunc("GET /api/kernels", func(w http.ResponseWriter, r *http.Request) {
		cat, err := kernel.LoadCatalog()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kernels": cat.Entries(),
			"groups":  cat.Groups(),
		})
	})

	mux.HandleFunc("POST /api/kernel/bind", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" || body.Name == "" {
			http.Error(w, "path and name required", http.StatusBadRequest)
			return
		}
		hub, err := reg.GetOrOpen(r.Context(), body.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if err := hub.BindKernel(body.Name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hub.Status())
	})

	mux.HandleFunc("GET /api/kernel/status", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			http.Error(w, "path required", http.StatusBadRequest)
			return
		}
		hub, err := reg.GetOrOpen(r.Context(), path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hub.Status())
	})
	_ = logger
}
