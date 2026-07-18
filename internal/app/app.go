package app

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/lucasew/gaderno/internal/auth"
	"github.com/lucasew/gaderno/internal/config"
	"github.com/lucasew/gaderno/internal/log"
	"github.com/lucasew/gaderno/internal/session"
	"github.com/lucasew/gaderno/internal/store"
	"github.com/lucasew/gaderno/internal/web"
	"github.com/lucasew/gaderno/internal/workspace"
)

// Run starts the HTTP server until ctx is cancelled.
func Run(ctx context.Context, cfg config.Config, version string) error {
	logger := log.New()

	root, err := cfg.AbsRoot()
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}

	// Bind safety is enforced in cli before Run; re-check so library callers
	// cannot accidentally expose a token-less non-loopback listener.
	if err := auth.CheckBind(cfg.Listen, cfg.Token, cfg.IUnderstand); err != nil {
		return err
	}

	ws := workspace.New(root)
	st := store.New(root)
	reg := session.NewRegistry(st, root, cfg.Kernel)
	defer reg.CloseAll(context.Background())

	staticFS, err := fs.Sub(web.Static, "static")
	if err != nil {
		return fmt.Errorf("static fs: %w", err)
	}

	gate := auth.New(cfg.Token)

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintln(w, version)
	})
	gate.RegisterTicketRoute(mux)
	registerWorkspaceRoutes(mux, ws, logger)
	registerNotebookRoutes(mux, st, reg, cfg.Kernel, logger)
	registerKernelRoutes(mux, reg, logger)
	registerWS(mux, reg, logger)

	handler := gate.Middleware(withLogging(logger, mux))

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	authMode := "none"
	if gate.Enabled() {
		authMode = "token"
	}
	logger.Info("listening",
		"addr", ln.Addr().String(),
		"root", root,
		"version", version,
		"kernel", cfg.Kernel,
		"auth", authMode,
	)
	if cfg.IUnderstand && !auth.IsLoopbackListen(cfg.Listen) && cfg.Token == "" {
		logger.Warn("non-loopback listen without token; --i-understand set (open RCE as this OS user)")
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		reg.CloseAll(shutdownCtx)
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/healthz" && r.URL.Path != "/static/app.css" && r.URL.Path != "/static/app.js" {
			// Never log raw query: may contain ?token= during bootstrap.
			logger.Info("http", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start))
		}
	})
}
