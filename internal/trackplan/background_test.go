package trackplan

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func TestSaveBackground(t *testing.T) {
	f, err := OpenFile(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, ext string
		data      []byte
	}{
		{"png", ".png", pngHeader},
		{"jpeg", ".jpg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")},
		{"svg with a prolog", ".svg", []byte(`<?xml version="1.0"?>` + "\n" + `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, err := f.SaveBackground(tt.data)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(name, "background-") || !strings.HasSuffix(name, tt.ext) {
				t.Errorf("name = %q, want background-<hash>%s", name, tt.ext)
			}
			p, err := f.BackgroundFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(p); string(got) != string(tt.data) {
				t.Error("the stored file differs from what was saved")
			}
		})
	}
	var invalidErr *InvalidError
	if _, err := f.SaveBackground([]byte("just some text")); !errors.As(err, &invalidErr) {
		t.Errorf("text = %v, want an *InvalidError", err)
	}
	if _, err := f.SaveBackground(nil); !errors.As(err, &invalidErr) {
		t.Errorf("nothing = %v, want an *InvalidError", err)
	}
	for _, name := range []string{"../trackplan.yaml", "background-0123456789abcdef.exe", "background-0123456789abcdef.png"} {
		if _, err := f.BackgroundFile(name); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("BackgroundFile(%q) = %v, want os.ErrNotExist", name, err)
		}
	}
}

func TestPutBackground(t *testing.T) {
	dir := t.TempDir()
	f, err := OpenFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.SaveBackground(pngHeader)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.SaveBackground(append(append([]byte(nil), pngHeader...), 1))
	if err != nil {
		t.Fatal(err)
	}

	plan := station()
	plan.Background = &Background{Image: first, X: -1.5, Y: 2, Width: 40, Height: 22.5, Opacity: 0.4}
	saved, err := f.Put(plan, f.Get().Revision)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Plan.Background == nil || saved.Plan.Background.Image != first {
		t.Fatalf("saved background = %+v", saved.Plan.Background)
	}
	if _, err := f.BackgroundFile(second); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the unused image is still there: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil || !strings.Contains(string(body), "background:\n  image: "+first) {
		t.Errorf("the file lacks the background:\n%s", body)
	}

	plan.Background = &Background{Image: second, Width: 1, Height: 1, Opacity: 1}
	var invalidErr *InvalidError
	if _, err := f.Put(plan, saved.Revision); !errors.As(err, &invalidErr) || !strings.Contains(err.Error(), "choose it again") {
		t.Errorf("a plan naming a deleted image = %v", err)
	}

	plan.Background = nil
	if _, err := f.Put(plan, saved.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := f.BackgroundFile(first); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the image of a removed background is still there: %v", err)
	}
}

func TestValidateBackground(t *testing.T) {
	good := Background{Image: "background-0123456789abcdef.png", Width: 10, Height: 5, Opacity: 0.5}
	tests := []struct {
		name   string
		change func(*Background)
		want   string
	}{
		{"fine", func(*Background) {}, ""},
		{"a name Interlock didn't make", func(b *Background) { b.Image = "../interlock.db" }, "not one Interlock stored"},
		{"no width", func(b *Background) { b.Width = 0 }, "wide and high"},
		{"not a number", func(b *Background) { b.X = math.NaN() }, "must be numbers"},
		{"turned", func(b *Background) { b.Rotation = 92.5 }, ""},
		{"turned backward", func(b *Background) { b.Rotation = -90 }, "rotation"},
		{"turned all the way", func(b *Background) { b.Rotation = 360 }, "rotation"},
		{"far away", func(b *Background) { b.Y = MaxCoord + 1 }, "within"},
		{"invisible", func(b *Background) { b.Opacity = 0 }, "opacity"},
		{"too opaque", func(b *Background) { b.Opacity = 1.5 }, "opacity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := good
			tt.change(&b)
			err := Plan{Grid: DefaultGrid, Background: &b}.Validate()
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("Validate: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("Validate = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}
