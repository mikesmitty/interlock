// Package atomicfile replaces files in one step, so a reader sees either
// the old contents or the new ones and never a half-written file.
package atomicfile

import (
	"os"
	"path/filepath"
	"time"
)

// renameTimeout bounds how long Rename keeps retrying while another
// program holds the target open.
const renameTimeout = 2 * time.Second

// Write writes data to a temporary file beside path and renames it over
// path, creating the directory first if it is missing.
func Write(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Rename renames from to to, replacing to if it exists. On Windows a
// file that another program has open (an editor, a virus scanner, a
// backup tool) cannot be replaced until it is closed, so Rename retries
// for a moment before giving up. Elsewhere it is os.Rename.
func Rename(from, to string) error {
	delay := 10 * time.Millisecond
	deadline := time.Now().Add(renameTimeout)
	for {
		err := os.Rename(from, to)
		if err == nil || !transient(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		delay = min(2*delay, 200*time.Millisecond)
	}
}
