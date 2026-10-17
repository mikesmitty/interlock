// Package decoder reads the DCC decoder definitions the JMRI project
// maintains: an index of manufacturers, families and models, and a file
// per family with its settings and how to lay them out. The files are
// GPL-2.0-only and Interlock is GPL-3.0, so Interlock never ships them. A
// Library reads them from a folder or zip the user already has, or fetches
// them from JMRI's GitHub repository into a cache in the data directory.
package decoder

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"
)

// Sources a Library can read from.
const (
	// SourceGitHub fetches each file from JMRI's repository the first time
	// it is needed and keeps it in the cache.
	SourceGitHub = "github"
	// SourceArchive downloads the repository archive once and keeps its
	// decoder files, fetching single files from GitHub until the download
	// is done.
	SourceArchive = "archive"
	// SourceFolder reads a JMRI install, a checkout of the JMRI
	// repository, or a zip of either.
	SourceFolder = "folder"
)

// DefaultRef is the JMRI release the definitions come from when the
// configuration names none.
const DefaultRef = "v5.16"

// CacheDirName is the folder in the data directory that fetched
// definitions are kept in.
const CacheDirName = "decoders"

const (
	defaultRawURL     = "https://raw.githubusercontent.com/JMRI/JMRI"
	defaultArchiveURL = "https://codeload.github.com/JMRI/JMRI/tar.gz"
	// indexFile is the decoder index, at the root of JMRI's xml folder.
	indexFile = "decoderIndex.xml"
	// completeMarker marks a cache folder the archive was fully unpacked
	// into.
	completeMarker = ".complete"
	// fetchTimeout bounds one file request to GitHub.
	fetchTimeout = 30 * time.Second
	// maxFileSize bounds one definition file; the largest is under 3 MB.
	maxFileSize = 64 << 20
)

var refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidRef reports whether ref can name a JMRI tag or branch.
func ValidRef(ref string) bool { return refPattern.MatchString(ref) }

// Options configures a Library.
type Options struct {
	// Source is SourceGitHub, SourceArchive or SourceFolder.
	Source string
	// Ref is the JMRI tag or branch for SourceGitHub and SourceArchive;
	// DefaultRef when empty.
	Ref string
	// Path is the folder or zip file for SourceFolder.
	Path string
	// CacheDir holds fetched files, one folder per ref.
	CacheDir string
	// Client makes the requests to GitHub; http.DefaultClient when nil.
	// Each file request has its own timeout.
	Client *http.Client
	// UserAgent identifies Interlock in requests to GitHub.
	UserAgent string
	Logger    *slog.Logger
	// RawURL and ArchiveURL replace GitHub's addresses in tests.
	RawURL, ArchiveURL string
}

// Status describes where a Library reads from.
type Status struct {
	Source string `json:"source"`
	Ref    string `json:"ref,omitempty"`
	Path   string `json:"path,omitempty"`
	// Archive is the download's progress, for SourceArchive.
	Archive *ArchiveStatus `json:"archive,omitempty"`
}

// Archive download states.
const (
	ArchiveWaiting     = "waiting"
	ArchiveDownloading = "downloading"
	ArchiveDone        = "done"
	ArchiveFailed      = "failed"
)

// ArchiveStatus is the progress of the archive download.
type ArchiveStatus struct {
	State string `json:"state"`
	// Bytes is how much of the archive has arrived; GitHub does not say
	// how big it is in advance.
	Bytes int64 `json:"bytes"`
	// Files is how many definition files have been kept.
	Files int    `json:"files"`
	Error string `json:"error,omitempty"`
}

// Library is a set of decoder definitions.
type Library struct {
	opts   Options
	log    *slog.Logger
	fsys   fs.FS
	closer io.Closer
	// cache is the folder fetched files go to; empty for SourceFolder.
	cache string

	mu      sync.Mutex
	index   *Index
	archive ArchiveStatus
	// defs keeps the last few definitions read, newest last.
	defs []*Definition
}

// keepDefinitions is how many read definitions are kept; a session
// usually works on one decoder at a time.
const keepDefinitions = 4

// ErrUnknownDecoder means the index lists no such family file and model.
var ErrUnknownDecoder = errors.New("decoder definitions: no such decoder")

// Open prepares a Library. A folder or zip is checked now; nothing is
// fetched until it is needed.
func Open(opts Options) (*Library, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Client == nil {
		opts.Client = http.DefaultClient
	}
	if opts.Ref == "" {
		opts.Ref = DefaultRef
	}
	if opts.RawURL == "" {
		opts.RawURL = defaultRawURL
	}
	if opts.ArchiveURL == "" {
		opts.ArchiveURL = defaultArchiveURL
	}
	l := &Library{opts: opts, log: opts.Logger}
	switch opts.Source {
	case SourceFolder:
		fsys, closer, err := openFolder(opts.Path)
		if err != nil {
			return nil, err
		}
		l.fsys, l.closer = fsys, closer
	case SourceGitHub, SourceArchive:
		if !ValidRef(opts.Ref) {
			return nil, fmt.Errorf("decoder definitions: %q is not a tag or branch name", opts.Ref)
		}
		if opts.CacheDir == "" {
			return nil, errors.New("decoder definitions: no cache folder")
		}
		l.cache = filepath.Join(opts.CacheDir, opts.Ref)
		l.fsys = newGitHubFS(opts.RawURL+"/"+opts.Ref+"/xml/", l.cache, opts.Client, opts.UserAgent)
		l.archive.State = ArchiveWaiting
		if _, err := os.Stat(filepath.Join(l.cache, completeMarker)); err == nil {
			l.archive.State = ArchiveDone
		}
	default:
		return nil, fmt.Errorf("decoder definitions: source %q must be %s, %s or %s", opts.Source, SourceGitHub, SourceArchive, SourceFolder)
	}
	return l, nil
}

// Close releases an open zip.
func (l *Library) Close() error {
	if l.closer != nil {
		return l.closer.Close()
	}
	return nil
}

// Status says where the definitions come from and, for an archive, how
// far its download is.
func (l *Library) Status() Status {
	s := Status{Source: l.opts.Source}
	switch l.opts.Source {
	case SourceFolder:
		s.Path = l.opts.Path
	case SourceGitHub:
		s.Ref = l.opts.Ref
	case SourceArchive:
		s.Ref = l.opts.Ref
		l.mu.Lock()
		a := l.archive
		l.mu.Unlock()
		s.Archive = &a
	}
	return s
}

// Index returns the decoder index, reading it the first time.
func (l *Library) Index() (*Index, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.index != nil {
		return l.index, nil
	}
	f, err := l.fsys.Open(indexFile)
	if err != nil {
		return nil, fmt.Errorf("decoder index: %w", err)
	}
	defer func() { _ = f.Close() }()
	idx, err := parseIndex(f)
	if err != nil {
		return nil, fmt.Errorf("decoder index: %w", err)
	}
	l.index = idx
	return idx, nil
}

// openFolder opens a folder or zip and finds JMRI's xml folder in it.
func openFolder(p string) (fs.FS, io.Closer, error) {
	if p == "" {
		return nil, nil, errors.New("decoder definitions: no path for the folder source")
	}
	info, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("decoder definitions: %s does not exist", p)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("decoder definitions: %w", err)
	}
	var fsys fs.FS
	var closer io.Closer
	if info.IsDir() {
		fsys = os.DirFS(p)
	} else {
		z, err := zip.OpenReader(p)
		if err != nil {
			return nil, nil, fmt.Errorf("decoder definitions: %s is neither a folder nor a zip: %w", p, err)
		}
		fsys, closer = z, z
	}
	root, err := findRoot(fsys)
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		return nil, nil, fmt.Errorf("decoder definitions: no %s in %s; point it at a JMRI install or a checkout of the JMRI repository", indexFile, p)
	}
	sub, err := fs.Sub(fsys, root)
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		return nil, nil, fmt.Errorf("decoder definitions: %w", err)
	}
	return sub, closer, nil
}

// findRoot finds the folder holding decoderIndex.xml: the top, its xml
// folder, or the xml folder one level down (an unpacked or zipped
// release has everything under one top folder).
func findRoot(fsys fs.FS) (string, error) {
	for _, dir := range []string{".", "xml"} {
		if exists(fsys, path.Join(dir, indexFile)) {
			return dir, nil
		}
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, dir := range []string{e.Name(), path.Join(e.Name(), "xml")} {
			if exists(fsys, path.Join(dir, indexFile)) {
				return dir, nil
			}
		}
	}
	return "", fs.ErrNotExist
}

func exists(fsys fs.FS, name string) bool {
	_, err := fs.Stat(fsys, name)
	return err == nil
}

// Definition returns a model's resolved definition from its family file.
// Only files and models in the index can be read; the family's own name
// stands for the family as a whole.
func (l *Library) Definition(file, model string) (*Definition, error) {
	idx, err := l.Index()
	if err != nil {
		return nil, err
	}
	known := false
	for _, f := range idx.Families {
		if f.File == file && (f.Name == model || slices.ContainsFunc(f.Models, func(m Model) bool { return m.Name == model })) {
			known = true
			break
		}
	}
	if !known {
		return nil, ErrUnknownDecoder
	}
	l.mu.Lock()
	for i, d := range l.defs {
		if d.File == file && d.Model == model {
			l.defs = append(append(l.defs[:i:i], l.defs[i+1:]...), d)
			l.mu.Unlock()
			return d, nil
		}
	}
	l.mu.Unlock()
	if g, ok := l.fsys.(*githubFS); ok {
		g.prefetch(path.Join("decoders", file), path.Join("programmers", DefaultProgrammer))
	}
	d, err := loadDefinition(&treeLoader{fsys: l.fsys}, file, model)
	if err != nil {
		return nil, fmt.Errorf("decoder definition: %w", err)
	}
	l.mu.Lock()
	l.defs = append(l.defs, d)
	if len(l.defs) > keepDefinitions {
		l.defs = l.defs[len(l.defs)-keepDefinitions:]
	}
	l.mu.Unlock()
	return d, nil
}
