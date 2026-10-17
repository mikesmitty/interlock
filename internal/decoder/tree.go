package decoder

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	xincludeNS = "http://www.w3.org/2001/XInclude"
	xmlNS      = "http://www.w3.org/XML/1998/namespace"
	// maxIncludeDepth stops a file that includes itself, directly or not.
	maxIncludeDepth = 16
)

// element is one XML element of a definition file, with its includes
// already replaced by what they include. Attribute names are local names,
// except xml:lang, which keeps its prefix.
type element struct {
	Name     string
	Attr     map[string]string
	Children []*element
	// Text is the element's own text, trimmed.
	Text string
}

func (e *element) attr(name string) string { return e.Attr[name] }

// child returns the first child with a name, or nil.
func (e *element) child(name string) *element {
	for _, c := range e.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// all returns the children with a name.
func (e *element) all(name string) []*element {
	var out []*element
	for _, c := range e.Children {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// treeLoader reads definition files into elements, following includes.
type treeLoader struct {
	fsys fs.FS
	// Missing lists includes that name files the source does not have;
	// JMRI skips them, and so does Interlock.
	Missing []string
}

// load reads a file, by its path under the xml folder.
func (t *treeLoader) load(name string) (*element, error) {
	return t.loadDepth(name, 0)
}

func (t *treeLoader) loadDepth(name string, depth int) (*element, error) {
	if depth > maxIncludeDepth {
		return nil, fmt.Errorf("%s: includes nest too deeply", name)
	}
	f, err := t.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	root, err := t.parse(f, name, depth)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return root, nil
}

// parse builds the element tree of one file. Includes are read in place;
// elements in other namespaces (the DocBook copyright and revision
// history) are dropped.
func (t *treeLoader) parse(r io.Reader, name string, depth int) (*element, error) {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charsetReader
	var stack []*element
	var root *element
	skip := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			if skip > 0 {
				skip++
				continue
			}
			if tok.Name.Space == xincludeNS {
				if tok.Name.Local == "include" {
					if err := t.include(tok, name, depth, stack); err != nil {
						return nil, err
					}
				}
				skip = 1
				continue
			}
			if tok.Name.Space != "" {
				skip = 1
				continue
			}
			el := &element{Name: tok.Name.Local, Attr: make(map[string]string, len(tok.Attr))}
			for _, a := range tok.Attr {
				switch a.Name.Space {
				case "":
					el.Attr[a.Name.Local] = a.Value
				case xmlNS:
					el.Attr["xml:"+a.Name.Local] = a.Value
				}
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, el)
			} else {
				root = el
			}
			stack = append(stack, el)
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			el := stack[len(stack)-1]
			el.Text = strings.TrimSpace(el.Text)
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if skip == 0 && len(stack) > 0 {
				stack[len(stack)-1].Text += string(tok)
			}
		}
	}
	if root == nil {
		return nil, errors.New("no root element")
	}
	return root, nil
}

// include reads the file an xi:include names and adds its root element
// where the include stood.
func (t *treeLoader) include(tok xml.StartElement, from string, depth int, stack []*element) error {
	var href string
	for _, a := range tok.Attr {
		if a.Name.Space == "" && a.Name.Local == "href" {
			href = a.Value
		}
	}
	target, ok := resolveHref(from, href)
	if !ok {
		t.Missing = append(t.Missing, href)
		return nil
	}
	el, err := t.loadDepth(target, depth+1)
	if errors.Is(err, fs.ErrNotExist) {
		t.Missing = append(t.Missing, target)
		return nil
	}
	if err != nil {
		return err
	}
	if len(stack) == 0 {
		return errors.New("an include cannot be the root element")
	}
	parent := stack[len(stack)-1]
	parent.Children = append(parent.Children, el)
	return nil
}

// resolveHref turns an include's href into a path under the xml folder.
// JMRI's files name each other by their address on jmri.org, which
// mirrors the xml folder; relative names are relative to the including
// file.
func resolveHref(from, href string) (string, bool) {
	for _, prefix := range []string{"http://jmri.org/xml/", "https://jmri.org/xml/", "http://www.jmri.org/xml/", "https://www.jmri.org/xml/", "/xml/"} {
		if rest, ok := strings.CutPrefix(href, prefix); ok {
			return validTarget(path.Clean(rest))
		}
	}
	if href == "" || strings.Contains(href, "://") || strings.HasPrefix(href, "/") {
		return "", false
	}
	return validTarget(path.Join(path.Dir(from), href))
}

func validTarget(p string) (string, bool) {
	return p, fs.ValidPath(p) && p != "."
}

// charsetReader decodes the encodings the definition files declare besides
// UTF-8: a few say ISO-8859-1, whose bytes are the first 256 code points.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1":
		return &latin1Reader{r: input}, nil
	case "us-ascii", "ascii":
		return input, nil
	}
	return nil, fmt.Errorf("unsupported encoding %q", charset)
}

// latin1Reader turns ISO-8859-1 bytes into UTF-8.
type latin1Reader struct {
	r   io.Reader
	buf []byte
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	if len(l.buf) == 0 {
		raw := make([]byte, max(len(p)/2, 1))
		n, err := l.r.Read(raw)
		for _, b := range raw[:n] {
			l.buf = utf8.AppendRune(l.buf, rune(b))
		}
		if len(l.buf) == 0 {
			return 0, err
		}
	}
	n := copy(p, l.buf)
	l.buf = l.buf[n:]
	return n, nil
}
