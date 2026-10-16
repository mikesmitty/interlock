// Package server runs the HTTP server: the API, the embedded web interface
// and the server lifecycle.
package server

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"
	"time"
)

// Routes is anything that can add handlers to a mux, such as the API.
type Routes interface {
	Register(mux *http.ServeMux)
}

// Options configures a Server.
type Options struct {
	// Listen is the address to bind, for example ":8080".
	Listen string
	// Logger receives request and lifecycle logs. nil means slog.Default.
	Logger *slog.Logger
	// API registers the /api routes. nil means no API.
	API Routes
	// UI is the built web interface rooted at its index.html. nil, or a
	// tree without index.html, serves a page explaining how to build it.
	UI fs.FS
}

// Server is the HTTP server.
type Server struct {
	opts    Options
	handler http.Handler
}

// New builds a server from opts. Nothing listens until Run.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	mux := http.NewServeMux()
	if opts.API != nil {
		opts.API.Register(mux)
	}
	mux.Handle("/", uiHandler(opts.UI))
	return &Server{opts: opts, handler: logRequests(opts.Logger, mux)}
}

// Handler returns the server's root handler, for tests and embedding.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Run serves until ctx is canceled, then shuts down gracefully. It returns
// nil after a clean shutdown.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.opts.Listen,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	listener, err := net.Listen("tcp", s.opts.Listen)
	if err != nil {
		return err
	}
	s.opts.Logger.Info("web interface listening", "address", listener.Addr().String())

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(listener) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	s.opts.Logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// uiHandler serves the single page application: real files as they are,
// every other path as index.html so the client router can handle it.
func uiHandler(ui fs.FS) http.Handler {
	var index []byte
	if ui != nil {
		index, _ = fs.ReadFile(ui, "index.html")
	}
	if index == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(uiNotBuiltPage))
		})
	}
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if info, err := fs.Stat(ui, name); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	})
}

const uiNotBuiltPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Interlock</title>
<style>body{font-family:system-ui,sans-serif;max-width:40rem;margin:4rem auto;padding:0 1rem;line-height:1.5}code{background:#eee;padding:.1em .3em;border-radius:3px}</style>
</head><body>
<h1>Interlock</h1>
<p>The server is running, but the web interface has not been built into this binary.</p>
<p>Run <code>make web</code> and rebuild, or run <code>make dev</code> for the Vite development server.</p>
<p>The API is up: <a href="/api/v1/health">/api/v1/health</a></p>
</body></html>
`

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the real writer, so handlers
// can flush (the event stream needs that) through this wrapper.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// logRequests logs each request at debug level, and at warn for server
// errors, so normal browsing does not flood the log.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		level := slog.LevelDebug
		if rec.status >= http.StatusInternalServerError {
			level = slog.LevelWarn
		}
		logger.Log(r.Context(), level, "http",
			"method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration", time.Since(start))
	})
}
