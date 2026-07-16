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

var notebookPage = template.Must(template.New("notebook").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{ .Path }} — gaderno</title>
  <style>
    body { font-family: system-ui, sans-serif; margin: 1.5rem; max-width: 52rem; }
    a { color: #06c; }
    .cell { border: 1px solid #ddd; border-radius: 6px; margin: 0.75rem 0; padding: 0.75rem; }
    .meta { color: #666; font-size: 0.85rem; margin-bottom: 0.5rem; display: flex; gap: 0.75rem; align-items: center; flex-wrap: wrap; }
    pre { white-space: pre-wrap; margin: 0; font-family: ui-monospace, monospace; font-size: 0.9rem; }
    .out { background: #f7f7f7; margin-top: 0.5rem; padding: 0.5rem; border-radius: 4px; }
    .err { background: #fee; }
    .toolbar { margin-bottom: 1rem; display: flex; gap: 0.75rem; align-items: center; flex-wrap: wrap; }
    button { padding: 0.35rem 0.7rem; cursor: pointer; }
    button:focus { outline: 2px solid #06c; outline-offset: 2px; }
    #status { color: #666; font-size: 0.9rem; }
    #chatlog { border-top: 1px solid #eee; margin-top: 1.5rem; padding-top: 0.75rem; max-height: 10rem; overflow: auto; font-size: 0.9rem; }
    #chatform { display: flex; gap: 0.5rem; margin-top: 0.5rem; }
    #chatform input { flex: 1; padding: 0.4rem; }
  </style>
</head>
<body>
  <div class="toolbar">
    <a href="/">← Workspace</a>
    <strong>{{ .Path }}</strong>
    <a href="/api/notebooks/{{ .Path }}?download=1">Export .ipynb</a>
    <button type="button" id="btnsave">Save</button>
    <span id="status">connecting…</span>
  </div>
  <div id="cells">
  {{ range .Cells }}
  <div class="cell" data-cell-id="{{ .ID }}">
    <div class="meta">
      <span>{{ .Type }} · {{ .ID }}</span>
      {{ if eq .Type "code" }}
      <button type="button" class="run" data-cell-id="{{ .ID }}">Run</button>
      {{ end }}
    </div>
    <pre class="src">{{ .Source }}</pre>
    <div class="out" hidden></div>
  </div>
  {{ else }}
  <p>Empty notebook.</p>
  {{ end }}
  </div>
  <div id="chatlog" aria-live="polite"></div>
  <form id="chatform">
    <input type="text" id="chatinput" placeholder="Session chat (RAM only)" aria-label="Chat message" autocomplete="off">
    <button type="submit">Send</button>
  </form>
  <script>
  (function () {
    const path = {{ .PathJSON }};
    const statusEl = document.getElementById('status');
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    let ws;
    function setStatus(s) { statusEl.textContent = s; }
    function connect() {
      ws = new WebSocket(proto + '://' + location.host + '/ws/notebooks/' + path);
      ws.binaryType = 'arraybuffer';
      ws.onopen = () => setStatus('Synced (WS)');
      ws.onclose = () => { setStatus('disconnected'); setTimeout(connect, 1500); };
      ws.onerror = () => setStatus('WS error');
      ws.onmessage = (ev) => {
        if (typeof ev.data !== 'string') {
          // binary yjs frame — keep for later full editor; ignore for now
          return;
        }
        let msg;
        try { msg = JSON.parse(ev.data); } catch { return; }
        if (msg.type === 'exec.result') {
          const cell = document.querySelector('.cell[data-cell-id="' + msg.cell_id + '"]');
          if (!cell) return;
          const out = cell.querySelector('.out');
          out.hidden = false;
          out.classList.toggle('err', msg.status === 'error');
          let t = '';
          if (msg.stdout) t += msg.stdout;
          if (msg.stderr) t += msg.stderr;
          if (msg.status === 'error') t += (msg.ename || '') + ': ' + (msg.evalue || '');
          out.innerHTML = '<pre></pre>';
          out.querySelector('pre').textContent = t || msg.status;
        } else if (msg.type === 'error') {
          setStatus('error: ' + msg.text);
        } else if (msg.type === 'chat.message') {
          const log = document.getElementById('chatlog');
          const line = document.createElement('div');
          line.textContent = (msg.from || '?') + ': ' + (msg.text || '');
          log.appendChild(line);
          log.scrollTop = log.scrollHeight;
        } else if (msg.type === 'pong') {
          // ok
        }
      };
    }
    connect();
    document.getElementById('cells').addEventListener('click', (e) => {
      const btn = e.target.closest('button.run');
      if (!btn || !ws || ws.readyState !== 1) return;
      setStatus('running…');
      ws.send(JSON.stringify({ type: 'exec.run', cell_id: btn.dataset.cellId }));
    });
    document.getElementById('btnsave').addEventListener('click', async () => {
      setStatus('saving…');
      const r = await fetch('/api/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: path })
      });
      setStatus(r.ok ? 'saved' : 'save failed');
    });
    document.getElementById('chatform').addEventListener('submit', (e) => {
      e.preventDefault();
      const input = document.getElementById('chatinput');
      const text = input.value.trim();
      if (!text || !ws || ws.readyState !== 1) return;
      ws.send(JSON.stringify({ type: 'chat.send', text: text }));
      input.value = '';
    });
  })();
  </script>
</body>
</html>`))

func registerNotebookRoutes(mux *http.ServeMux, st *store.Store, reg *session.Registry, logger *slog.Logger) {
	mux.HandleFunc("GET /n/{path...}", func(w http.ResponseWriter, r *http.Request) {
		path := r.PathValue("path")
		// Prefer live hub if open so we show latest CRDT projection
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
			Type   string
			ID     string
			Source string
		}
		var cells []cellView
		for _, c := range nb.Cells {
			cells = append(cells, cellView{
				Type:   string(c.CellType),
				ID:     c.ID,
				Source: c.SourceString(),
			})
		}
		pathJSON, _ := json.Marshal(path)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := notebookPage.Execute(w, map[string]any{
			"Path":     path,
			"PathJSON": template.JS(pathJSON),
			"Cells":    cells,
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
		res, err := hub.ExecuteCell(ctx, body.CellID)
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
