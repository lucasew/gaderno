package app

import (
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/lucasew/gaderno/internal/web"
	"github.com/lucasew/gaderno/internal/workspace"
)

func loadTemplate(name string) *template.Template {
	b, err := fs.ReadFile(web.Templates, "templates/"+name)
	if err != nil {
		panic(err)
	}
	return template.Must(template.New(name).Parse(string(b)))
}

var listPage = loadTemplate("workspace.html")

// notebookLink is one workspace list row: display name + safe /n/… href.
type notebookLink struct {
	Name string
	Href template.URL // pre-escaped; do not re-escape in the template
}

func registerWorkspaceRoutes(mux *http.ServeMux, ws *workspace.Workspace, logger *slog.Logger) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		list, err := ws.List()
		if err != nil {
			logger.Error("list notebooks", "err", err)
			http.Error(w, "list failed", http.StatusInternalServerError)
			return
		}
		links := make([]notebookLink, 0, len(list))
		for _, name := range list {
			links = append(links, notebookLink{
				Name: name,
				Href: template.URL("/n/" + EscapeNotebookPath(name)),
			})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := listPage.Execute(w, map[string]any{"Notebooks": links}); err != nil {
			logger.Error("render list", "err", err)
		}
	})

	mux.HandleFunc("GET /api/notebooks", func(w http.ResponseWriter, r *http.Request) {
		list, err := ws.List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"notebooks": list})
	})

	mux.HandleFunc("POST /api/notebooks", func(w http.ResponseWriter, r *http.Request) {
		name := r.FormValue("name")
		if name == "" && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				name = body.Name
			}
		}
		name = strings.TrimSpace(name)
		if name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		path, err := ws.Create(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if wantsHTML(r) {
			http.Redirect(w, r, "/n/"+EscapeNotebookPath(path), http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"path": path})
	})
}

func wantsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html") ||
		r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" ||
		r.FormValue("name") != ""
}
