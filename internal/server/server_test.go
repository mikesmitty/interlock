package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type fakeRoutes struct{}

func (fakeRoutes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
}

var builtUI = fstest.MapFS{
	"index.html":       {Data: []byte("<html>app</html>")},
	"assets/app.js":    {Data: []byte("console.log('hi')")},
	"assets/style.css": {Data: []byte("body{}")},
}

func TestHandler(t *testing.T) {
	tests := []struct {
		name       string
		ui         fstest.MapFS
		path       string
		wantStatus int
		wantBody   string
		wantType   string
	}{
		{name: "api", ui: builtUI, path: "/api/v1/health", wantStatus: 200, wantBody: `{"status":"ok"}`},
		{name: "index", ui: builtUI, path: "/", wantStatus: 200, wantBody: "<html>app</html>", wantType: "text/html"},
		{name: "explicit index", ui: builtUI, path: "/index.html", wantStatus: 200, wantBody: "<html>app</html>"},
		{name: "asset", ui: builtUI, path: "/assets/app.js", wantStatus: 200, wantBody: "console.log('hi')", wantType: "javascript"},
		{name: "css asset", ui: builtUI, path: "/assets/style.css", wantStatus: 200, wantBody: "body{}", wantType: "text/css"},
		{name: "spa route", ui: builtUI, path: "/nodes/05.01.01.01.22.00", wantStatus: 200, wantBody: "<html>app</html>"},
		{name: "directory falls back", ui: builtUI, path: "/assets/", wantStatus: 200, wantBody: "<html>app</html>"},
		{name: "missing asset falls back", ui: builtUI, path: "/assets/nope.js", wantStatus: 200, wantBody: "<html>app</html>"},
		{name: "ui not built", ui: fstest.MapFS{}, path: "/", wantStatus: 200, wantBody: "make web", wantType: "text/html"},
		{name: "ui not built api still works", ui: fstest.MapFS{}, path: "/api/v1/health", wantStatus: 200, wantBody: `{"status":"ok"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{API: fakeRoutes{}, UI: tt.ui, Logger: slog.New(slog.DiscardHandler)})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, http.NoBody))
			if rec.Code != tt.wantStatus {
				t.Fatalf("GET %s: status = %d, want %d", tt.path, rec.Code, tt.wantStatus)
			}
			if body := rec.Body.String(); !strings.Contains(body, tt.wantBody) {
				t.Fatalf("GET %s: body = %q, want it to contain %q", tt.path, body, tt.wantBody)
			}
			if tt.wantType != "" {
				if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, tt.wantType) {
					t.Fatalf("GET %s: Content-Type = %q, want %q", tt.path, ct, tt.wantType)
				}
			}
		})
	}
}

// TestStreamingThroughMiddleware makes sure a handler can flush through the
// logging wrapper, which the event stream relies on.
func TestStreamingThroughMiddleware(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/stream", func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "flushed")
	})
	srv := New(Options{API: routesFunc(func(m *http.ServeMux) { m.Handle("/api/", mux) }), UI: builtUI, Logger: slog.New(slog.DiscardHandler)})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/stream", http.NoBody))
	if rec.Code != http.StatusOK || rec.Body.String() != "flushed" || !rec.Flushed {
		t.Fatalf("status %d body %q flushed %v", rec.Code, rec.Body, rec.Flushed)
	}
}

type routesFunc func(*http.ServeMux)

func (f routesFunc) Register(mux *http.ServeMux) { f(mux) }

func TestNilUI(t *testing.T) {
	srv := New(Options{UI: nil, Logger: slog.New(slog.DiscardHandler)})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "make web") {
		t.Fatalf("nil UI: status %d body %q", rec.Code, rec.Body)
	}
}

func TestRunServesAndShutsDown(t *testing.T) {
	srv := New(Options{
		Listen: "127.0.0.1:0",
		API:    fakeRoutes{},
		UI:     builtUI,
		Logger: slog.New(slog.DiscardHandler),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	// Run picks the port; there is no accessor yet, so just make sure it
	// stops cleanly when asked.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after cancel, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunBadAddress(t *testing.T) {
	srv := New(Options{Listen: "256.256.256.256:1", Logger: slog.New(slog.DiscardHandler)})
	if err := srv.Run(context.Background()); err == nil {
		t.Fatal("Run succeeded on an unusable address")
	}
}
