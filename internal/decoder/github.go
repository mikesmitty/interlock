package decoder

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/mikesmitty/interlock/internal/atomicfile"
)

// githubFS reads definition files from the cache and fetches the ones it
// lacks from JMRI's repository at one ref. It only opens files; nothing
// lists its folders.
type githubFS struct {
	base   string // raw file address up to the xml folder, ending in /
	cache  string
	client *http.Client
	agent  string

	mu       sync.Mutex
	inflight map[string]*fetchCall
	// missing remembers files GitHub does not have, such as a pane a
	// definition includes but nobody committed, so they are asked for once.
	missing map[string]bool
}

type fetchCall struct {
	done chan struct{}
	err  error
}

func newGitHubFS(base, cache string, client *http.Client, agent string) *githubFS {
	return &githubFS{base: base, cache: cache, client: client, agent: agent, inflight: make(map[string]*fetchCall), missing: make(map[string]bool)}
}

func (g *githubFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	local := filepath.Join(g.cache, filepath.FromSlash(name))
	if f, err := os.Open(local); !errors.Is(err, fs.ErrNotExist) {
		return f, err
	}
	if err := g.fetch(name, local); err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return os.Open(local)
}

// fetch downloads name to local once, however many callers ask for it at
// the same time.
func (g *githubFS) fetch(name, local string) error {
	g.mu.Lock()
	if g.missing[name] {
		g.mu.Unlock()
		return fs.ErrNotExist
	}
	if call, ok := g.inflight[name]; ok {
		g.mu.Unlock()
		<-call.done
		return call.err
	}
	call := &fetchCall{done: make(chan struct{})}
	g.inflight[name] = call
	g.mu.Unlock()

	call.err = g.download(name, local)

	g.mu.Lock()
	delete(g.inflight, name)
	if errors.Is(call.err, fs.ErrNotExist) {
		g.missing[name] = true
	}
	g.mu.Unlock()
	close(call.done)
	return call.err
}

func (g *githubFS) download(name, local string) error {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.base+name, http.NoBody)
	if err != nil {
		return err
	}
	if g.agent != "" {
		req.Header.Set("User-Agent", g.agent)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch from GitHub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fs.ErrNotExist
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("fetch from GitHub: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFileSize+1))
	if err != nil {
		return fmt.Errorf("fetch from GitHub: %w", err)
	}
	if len(data) > maxFileSize {
		return errors.New("fetch from GitHub: file too large")
	}
	return atomicfile.Write(local, data, 0o644)
}

// prefetchLimit is how many files are fetched from GitHub at once.
const prefetchLimit = 8

// prefetch fetches files that are not cached yet, and everything they
// include, several at a time: a definition's first load then waits for a
// few round trips rather than one per file. Failures are left for the
// load itself to report.
func (g *githubFS) prefetch(names ...string) {
	var (
		mu   sync.Mutex
		seen = make(map[string]bool)
		wg   sync.WaitGroup
		sem  = make(chan struct{}, prefetchLimit)
	)
	var visit func(name string)
	visit = func(name string) {
		mu.Lock()
		defer mu.Unlock()
		if seen[name] {
			return
		}
		seen[name] = true
		wg.Go(func() {
			sem <- struct{}{}
			hrefs, err := g.includes(name)
			<-sem
			if err != nil {
				return
			}
			for _, href := range hrefs {
				if target, ok := resolveHref(name, href); ok {
					visit(target)
				}
			}
		})
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(g.cache, filepath.FromSlash(name))); errors.Is(err, fs.ErrNotExist) {
			visit(name)
		}
	}
	wg.Wait()
}

// includes opens a file, fetching it if needed, and lists the files it
// includes.
func (g *githubFS) includes(name string) ([]string, error) {
	f, err := g.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	dec := xml.NewDecoder(f)
	dec.CharsetReader = CharsetReader
	var hrefs []string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return hrefs, nil
		}
		if err != nil {
			return hrefs, err
		}
		if st, ok := tok.(xml.StartElement); ok && st.Name.Space == xincludeNS && st.Name.Local == "include" {
			for _, a := range st.Attr {
				if a.Name.Space == "" && a.Name.Local == "href" {
					hrefs = append(hrefs, a.Value)
				}
			}
		}
	}
}
