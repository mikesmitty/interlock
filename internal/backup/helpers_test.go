package backup_test

import (
	"archive/zip"
	"bytes"
)

// newZip writes a zip holding files into buf.
func newZip(buf *bytes.Buffer, files map[string]string) {
	zw := zip.NewWriter(buf)
	for name, content := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(content))
	}
	_ = zw.Close()
}
