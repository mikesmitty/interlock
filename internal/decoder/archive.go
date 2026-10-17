package decoder

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/mikesmitty/interlock/internal/atomicfile"
)

// Download fetches the repository archive at the Library's ref and keeps
// the decoder index, the decoder files and the programmer layouts from it,
// so the definitions work without a connection afterwards. Only
// SourceArchive downloads; for the other sources it does nothing. A
// finished download is not repeated.
func (l *Library) Download(ctx context.Context) error {
	if l.opts.Source != SourceArchive {
		return nil
	}
	l.mu.Lock()
	if l.archive.State == ArchiveDone || l.archive.State == ArchiveDownloading {
		l.mu.Unlock()
		return nil
	}
	l.archive = ArchiveStatus{State: ArchiveDownloading}
	l.mu.Unlock()

	var got atomic.Int64
	files, err := l.download(ctx, &got)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.archive.Bytes, l.archive.Files = got.Load(), files
	if err != nil {
		l.archive.State, l.archive.Error = ArchiveFailed, err.Error()
		return err
	}
	l.archive.State = ArchiveDone
	return nil
}

func (l *Library) download(ctx context.Context, got *atomic.Int64) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.opts.ArchiveURL+"/"+l.opts.Ref, nil)
	if err != nil {
		return 0, err
	}
	if l.opts.UserAgent != "" {
		req.Header.Set("User-Agent", l.opts.UserAgent)
	}
	resp, err := l.opts.Client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download the JMRI archive: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download the JMRI archive: %s", resp.Status)
	}
	progress := &countingReader{r: resp.Body, n: got, report: func(n int64) {
		l.mu.Lock()
		l.archive.Bytes = n
		l.mu.Unlock()
	}}
	gz, err := gzip.NewReader(progress)
	if err != nil {
		return 0, fmt.Errorf("download the JMRI archive: %w", err)
	}
	tr := tar.NewReader(gz)
	files := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return files, fmt.Errorf("download the JMRI archive: %w", err)
		}
		name, ok := keepFromArchive(hdr)
		if !ok {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxFileSize+1))
		if err != nil {
			return files, fmt.Errorf("download the JMRI archive: %w", err)
		}
		if len(data) > maxFileSize {
			return files, fmt.Errorf("download the JMRI archive: %s is too large", name)
		}
		if err := atomicfile.Write(filepath.Join(l.cache, filepath.FromSlash(name)), data, 0o644); err != nil {
			return files, fmt.Errorf("keep %s: %w", name, err)
		}
		files++
		l.mu.Lock()
		l.archive.Files = files
		l.mu.Unlock()
	}
	if files == 0 {
		return 0, errors.New("download the JMRI archive: it has no decoder definitions")
	}
	if err := os.WriteFile(filepath.Join(l.cache, completeMarker), []byte(l.opts.Ref+"\n"), 0o644); err != nil {
		return files, fmt.Errorf("download the JMRI archive: %w", err)
	}
	return files, nil
}

// keepFromArchive returns the cache path for an archive entry the Library
// keeps: the index, decoder files and programmer layouts from the xml
// folder under the archive's top folder.
func keepFromArchive(hdr *tar.Header) (string, bool) {
	if hdr.Typeflag != tar.TypeReg {
		return "", false
	}
	_, rest, ok := strings.Cut(hdr.Name, "/")
	if !ok {
		return "", false
	}
	rest, ok = strings.CutPrefix(path.Clean(rest), "xml/")
	if !ok || !fs.ValidPath(rest) {
		return "", false
	}
	if rest == indexFile || strings.HasPrefix(rest, "decoders/") || strings.HasPrefix(rest, "programmers/") {
		return rest, true
	}
	return "", false
}

// countingReader counts what passes through it and reports the total
// every megabyte or so.
type countingReader struct {
	r      io.Reader
	n      *atomic.Int64
	last   int64
	report func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	total := c.n.Add(int64(n))
	if total-c.last >= 1<<20 || err != nil {
		c.last = total
		c.report(total)
	}
	return n, err
}
