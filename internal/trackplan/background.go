package trackplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/mikesmitty/interlock/internal/atomicfile"
)

// BackgroundDirName is the folder in the data directory that holds the
// background images.
const BackgroundDirName = "backgrounds"

// MaxBackgroundSize bounds an uploaded background image.
const MaxBackgroundSize = 25 << 20

// Background is an image drawn under the plan, such as a plan exported
// from track planning software, to trace the track over. Its place and
// size are in cells.
type Background struct {
	// Image is the file's name in the backgrounds folder.
	Image  string  `json:"image" yaml:"image"`
	X      float64 `json:"x" yaml:"x"`
	Y      float64 `json:"y" yaml:"y"`
	Width  float64 `json:"width" yaml:"width"`
	Height float64 `json:"height" yaml:"height"`
	// Opacity is from just above 0 (faint) to 1 (solid).
	Opacity float64 `json:"opacity" yaml:"opacity"`
	// Operating shows the image under the plan while operating too, not
	// only while editing.
	Operating bool `json:"operating,omitempty" yaml:"operating,omitempty"`
	// Invert draws the image with its light and dark swapped, so a plan
	// drawn dark on white reads light on the dark theme.
	Invert bool `json:"invert,omitempty" yaml:"invert,omitempty"`
}

// Images are kept under a name made from their contents, so a changed
// image is a new file and browsers can keep the old one cached.
var backgroundName = regexp.MustCompile(`^background-[0-9a-f]{16}\.(png|jpg|gif|webp|svg)$`)

// BackgroundTypes maps the image file extensions to their media types.
var BackgroundTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
}

func (b *Background) validate() error {
	if !backgroundName.MatchString(b.Image) {
		return invalid("the background image %q is not one Interlock stored", b.Image)
	}
	for _, v := range []float64{b.X, b.Y, b.Width, b.Height, b.Opacity} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return invalid("the background's place, size and opacity must be numbers")
		}
	}
	if math.Abs(b.X) > MaxCoord || math.Abs(b.Y) > MaxCoord {
		return invalid("the background must start within the grid (%d to %d)", -MaxCoord, MaxCoord)
	}
	if b.Width <= 0 || b.Height <= 0 || b.Width > 2*MaxCoord || b.Height > 2*MaxCoord {
		return invalid("the background must be between 0 and %d cells wide and high", 2*MaxCoord)
	}
	if b.Opacity <= 0 || b.Opacity > 1 {
		return invalid("the background's opacity must be above 0 and at most 1")
	}
	return nil
}

// backgroundExt works out an image's type from its first bytes: PNG,
// JPEG, GIF, WebP or SVG.
func backgroundExt(data []byte) (string, error) {
	switch http.DetectContentType(data) {
	case "image/png":
		return ".png", nil
	case "image/jpeg":
		return ".jpg", nil
	case "image/gif":
		return ".gif", nil
	case "image/webp":
		return ".webp", nil
	}
	head := data[:min(len(data), 4096)]
	if bytes.Contains(head, []byte("<svg")) {
		return ".svg", nil
	}
	return "", invalid("the background must be a PNG, JPEG, GIF, WebP or SVG image")
}

// SaveBackground stores an image for a background and returns its name.
// It is not part of the plan until a plan naming it is saved.
func (f *File) SaveBackground(data []byte) (string, error) {
	if len(data) == 0 {
		return "", invalid("the background image is empty")
	}
	if len(data) > MaxBackgroundSize {
		return "", invalid("the background image is larger than %d MB", MaxBackgroundSize>>20)
	}
	ext, err := backgroundExt(data)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	name := "background-" + hex.EncodeToString(sum[:8]) + ext
	if err := os.MkdirAll(f.backgroundDir(), 0o755); err != nil {
		return "", fmt.Errorf("store background: %w", err)
	}
	if err := atomicfile.Write(filepath.Join(f.backgroundDir(), name), data, 0o644); err != nil {
		return "", fmt.Errorf("store background: %w", err)
	}
	return name, nil
}

// BackgroundFile returns where a stored background image is, or
// os.ErrNotExist.
func (f *File) BackgroundFile(name string) (string, error) {
	if !backgroundName.MatchString(name) {
		return "", os.ErrNotExist
	}
	p := filepath.Join(f.backgroundDir(), name)
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

func (f *File) backgroundDir() string {
	return filepath.Join(filepath.Dir(f.path), BackgroundDirName)
}

// pruneBackgrounds deletes the stored images other than keep, such as
// ones chosen for an edit that was then discarded or replaced.
func (f *File) pruneBackgrounds(keep string) error {
	entries, err := os.ReadDir(f.backgroundDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if e.IsDir() || e.Name() == keep || !backgroundName.MatchString(e.Name()) {
			continue
		}
		if err := os.Remove(filepath.Join(f.backgroundDir(), e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
