package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mikesmitty/interlock/internal/version"
)

func newTestMux() *http.ServeMux {
	mux := http.NewServeMux()
	New(0x050101012200).Register(mux)
	return mux
}

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	var got healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body %s", err, rec.Body)
	}
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
	if got.Version != version.Version {
		t.Errorf("version = %q, want %q", got.Version, version.Version)
	}
	if got.NodeID != 0x050101012200 {
		t.Errorf("node_id = %v, want 05.01.01.01.22.00", got.NodeID)
	}
	if got.UptimeSeconds < 0 {
		t.Errorf("uptime_seconds = %v, want >= 0", got.UptimeSeconds)
	}
}

func TestRouting(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantAllow  string
	}{
		{name: "health", method: http.MethodGet, path: "/api/v1/health", wantStatus: http.StatusOK},
		{name: "health wrong method", method: http.MethodPost, path: "/api/v1/health", wantStatus: http.StatusMethodNotAllowed, wantAllow: "GET, HEAD"},
		{name: "unknown endpoint", method: http.MethodGet, path: "/api/v1/nope", wantStatus: http.StatusNotFound},
		{name: "unknown endpoint post", method: http.MethodPost, path: "/api/v1/nope", wantStatus: http.StatusNotFound},
		{name: "api root", method: http.MethodGet, path: "/api/", wantStatus: http.StatusNotFound},
	}
	mux := newTestMux()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, http.NoBody))
			if rec.Code != tt.wantStatus {
				t.Fatalf("%s %s: status = %d, want %d; body %s", tt.method, tt.path, rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantAllow != "" {
				if allow := rec.Header().Get("Allow"); allow != tt.wantAllow {
					t.Errorf("Allow = %q, want %q", allow, tt.wantAllow)
				}
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q, want JSON", ct)
			}
			if tt.wantStatus != http.StatusOK {
				var body errorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error == "" {
					t.Fatalf("error body should be JSON with an error field, got %q (%v)", rec.Body, err)
				}
			}
		})
	}
}
