package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mikesmitty/interlock/internal/backup"
)

func TestBackupEndpoints(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "trains.yaml"), []byte("trains: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var restarts atomic.Int32
	a := New(Deps{Node: fakeNode{}, Registry: newFakeRegistry(), Bus: &fakeBus{},
		Backups: &backup.Manager{Dir: dir, Version: "test"}, Restart: func() { restarts.Add(1) }})
	mux := http.NewServeMux()
	a.Register(mux)
	ta := &testAPI{api: a, mux: mux}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/backup", http.NoBody))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" || rec.Header().Get("Content-Disposition") == "" {
		t.Fatalf("download: %d %v", rec.Code, rec.Header())
	}
	archive := rec.Body.Bytes()
	if m, err := backup.Inspect(bytes.NewReader(archive), int64(len(archive))); err != nil || len(m.Files) != 1 || m.Files[0] != "trains.yaml" {
		t.Fatalf("archive = %+v, %v", m, err)
	}

	rec = upload(t, mux, "/api/v1/restore", "interlock-backup.zip", archive)
	if rec.Code != http.StatusAccepted || !bytes.Contains(rec.Body.Bytes(), []byte(`"restarting":true`)) {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	if !backup.Pending(dir) {
		t.Error("restore did not stage the archive")
	}
	if rec := upload(t, mux, "/api/v1/restore", "notes.txt", []byte("not a zip")); rec.Code != http.StatusBadRequest {
		t.Errorf("bad archive: %d %s", rec.Code, rec.Body)
	}
	rec, body := ta.do(t, http.MethodPost, "/api/v1/restart", "")
	if rec.Code != http.StatusAccepted || body["restarting"] != true {
		t.Errorf("restart: %d %v", rec.Code, body)
	}
}
