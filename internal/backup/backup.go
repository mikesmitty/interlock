// Package backup writes the data directory out as one zip file and
// restores one: the configuration, entities, automations, roster and
// profiles as they are, and a consistent snapshot of the database. A
// restore is staged next to the data and applied when Interlock starts,
// so nothing is replaced while it is open.
package backup

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mikesmitty/interlock/internal/atomicfile"
)

// ManifestName is the file in the archive that describes it.
const ManifestName = "interlock-backup.json"

// Manifest describes a backup.
type Manifest struct {
	// Version of Interlock that wrote it.
	Version string    `json:"version"`
	Created time.Time `json:"created"`
	// Files lists what the archive holds besides the manifest.
	Files []string `json:"files"`
}

// DB can write a consistent copy of the database to a new file.
type DB interface {
	Snapshot(ctx context.Context, path string) error
}

// Files in the data directory that a backup carries and a restore may
// replace. The database is handled apart from the others.
const (
	DBFile = "interlock.db"
	// PendingDir is where a staged restore waits inside the data directory.
	PendingDir = "restore.pending"
	// keptDirPrefix names the directory the replaced files are moved to.
	keptDirPrefix = "before-restore-"
	// MaxArchive bounds an uploaded backup.
	MaxArchive = 256 << 20
)

var plainFiles = []string{"interlock.yaml", "entities.yaml", "automations.yaml", "trains.yaml"}

// allowed reports whether an archive entry may be restored, and the clean
// path to restore it at.
func allowed(name string) (string, bool) {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", false
	}
	if clean == DBFile {
		return clean, true
	}
	for _, f := range plainFiles {
		if clean == f {
			return clean, true
		}
	}
	if dir, base := path.Split(clean); dir == "profiles/" && (strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml")) {
		return clean, true
	}
	return "", false
}

// Write writes a backup of dir to w. db may be nil when there is no
// database to include.
func Write(ctx context.Context, w io.Writer, dir string, db DB, version string) error {
	zw := zip.NewWriter(w)
	manifest := Manifest{Version: version, Created: time.Now()}
	add := func(name, from string) error {
		f, err := os.Open(from)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = name
		hdr.Method = zip.Deflate
		out, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, f); err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, name)
		return nil
	}
	for _, name := range plainFiles {
		from := filepath.Join(dir, name)
		if _, err := os.Stat(from); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err := add(name, from); err != nil {
			return fmt.Errorf("backup %s: %w", name, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "profiles"))
	if err == nil {
		for _, e := range entries {
			name := "profiles/" + e.Name()
			if clean, ok := allowed(name); ok && !e.IsDir() {
				if err := add(clean, filepath.Join(dir, "profiles", e.Name())); err != nil {
					return fmt.Errorf("backup %s: %w", name, err)
				}
			}
		}
	}
	if db != nil {
		tmp, err := os.MkdirTemp(dir, ".backup-")
		if err != nil {
			return fmt.Errorf("backup database: %w", err)
		}
		defer os.RemoveAll(tmp)
		snapshot := filepath.Join(tmp, DBFile)
		if err := db.Snapshot(ctx, snapshot); err != nil {
			return fmt.Errorf("backup database: %w", err)
		}
		if err := add(DBFile, snapshot); err != nil {
			return fmt.Errorf("backup database: %w", err)
		}
	}
	m, err := zw.Create(ManifestName)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(m).Encode(manifest); err != nil {
		return err
	}
	return zw.Close()
}

// FileName names a backup written now.
func FileName(now time.Time) string {
	return "interlock-backup-" + now.Format("2006-01-02-1504") + ".zip"
}

// InvalidError says an archive is not a backup Interlock can restore.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return "backup: " + e.Reason }

// Inspect checks an archive and returns its manifest.
func Inspect(r io.ReaderAt, size int64) (Manifest, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return Manifest{}, &InvalidError{"the file is not a zip archive"}
	}
	var manifest Manifest
	found := false
	var total uint64
	for _, f := range zr.File {
		if f.Name == ManifestName {
			rc, err := f.Open()
			if err != nil {
				return Manifest{}, &InvalidError{"the manifest cannot be read"}
			}
			err = json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&manifest)
			rc.Close()
			if err != nil {
				return Manifest{}, &InvalidError{"the manifest is not valid: " + err.Error()}
			}
			found = true
			continue
		}
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		if _, ok := allowed(f.Name); !ok {
			return Manifest{}, &InvalidError{fmt.Sprintf("%q is not a file Interlock keeps", f.Name)}
		}
		total += f.UncompressedSize64
		if total > MaxArchive {
			return Manifest{}, &InvalidError{"the archive unpacks to more than 256 MB"}
		}
	}
	if !found {
		return Manifest{}, &InvalidError{"the archive has no " + ManifestName + ", so it was not made by Interlock"}
	}
	return manifest, nil
}

// Stage unpacks an archive into dir's pending directory, replacing any
// earlier staged restore. Apply puts it in place at the next start.
func Stage(r io.ReaderAt, size int64, dir string) (Manifest, error) {
	manifest, err := Inspect(r, size)
	if err != nil {
		return Manifest{}, err
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return Manifest{}, &InvalidError{"the file is not a zip archive"}
	}
	pending := filepath.Join(dir, PendingDir)
	if err := os.RemoveAll(pending); err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(pending, 0o755); err != nil {
		return Manifest{}, err
	}
	for _, f := range zr.File {
		clean, ok := allowed(f.Name)
		if !ok {
			continue
		}
		dest := filepath.Join(pending, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return Manifest{}, err
		}
		if err := extract(f, dest); err != nil {
			return Manifest{}, fmt.Errorf("unpack %s: %w", clean, err)
		}
	}
	return manifest, nil
}

func extract(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, MaxArchive)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Pending reports whether a staged restore is waiting in dir.
func Pending(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, PendingDir))
	return err == nil && info.IsDir()
}

// Apply moves a staged restore into place: the files it replaces go to a
// before-restore directory next to them, named for the time. It returns
// that directory's name, or "" when nothing was pending. Call it before
// anything in dir is opened.
func Apply(dir string) (kept string, err error) {
	pending := filepath.Join(dir, PendingDir)
	if !Pending(dir) {
		return "", nil
	}
	var files []string
	err = filepath.WalkDir(pending, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(pending, p)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("restore: %w", err)
	}
	sort.Strings(files)
	keptDir := filepath.Join(dir, keptDirPrefix+time.Now().Format("2006-01-02-150405"))
	if err := os.MkdirAll(keptDir, 0o755); err != nil {
		return "", fmt.Errorf("restore: %w", err)
	}
	move := func(from, to string) error {
		if _, err := os.Stat(from); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return atomicfile.Rename(from, to)
	}
	for _, rel := range files {
		current := filepath.Join(dir, rel)
		if err := move(current, filepath.Join(keptDir, rel)); err != nil {
			return "", fmt.Errorf("restore: set aside %s: %w", rel, err)
		}
		if rel == DBFile {
			// A stale write-ahead log would be applied to the restored
			// database; set those aside too.
			for _, suffix := range []string{"-wal", "-shm"} {
				if err := move(current+suffix, filepath.Join(keptDir, rel+suffix)); err != nil {
					return "", fmt.Errorf("restore: set aside %s: %w", rel+suffix, err)
				}
			}
		}
		if err := move(filepath.Join(pending, rel), current); err != nil {
			return "", fmt.Errorf("restore: put %s in place: %w", rel, err)
		}
	}
	if err := os.RemoveAll(pending); err != nil {
		return "", fmt.Errorf("restore: %w", err)
	}
	return filepath.Base(keptDir), nil
}

// Manager backs up and restores one data directory.
type Manager struct {
	// Dir is the data directory.
	Dir string
	// DB is the open database, snapshotted into backups; may be nil.
	DB DB
	// Version is written into manifests.
	Version string
}

// Write writes a backup to w.
func (m *Manager) Write(ctx context.Context, w io.Writer) error {
	return Write(ctx, w, m.Dir, m.DB, m.Version)
}

// Stage checks an uploaded backup and stages it for the next start.
func (m *Manager) Stage(r io.ReaderAt, size int64) (Manifest, error) {
	return Stage(r, size, m.Dir)
}
