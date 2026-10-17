package decoder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestExpandCVs(t *testing.T) {
	tests := []struct {
		attr string
		want string
	}{
		{"29", "29"},
		{"16.0.259", "16.0.259"},
		{"257.31=8.32=0", "257.31=8.32=0"},
		{"1,9", "1 9"},
		{"508,509,510", "508 509 510"},
		{"16.4.(258,257)", "16.4.258 16.4.257"},
		{"0.255.285:2", "0.255.285 0.255.286"},
		{"192:4", "192 193 194 195"},
		{"265:4.31=0.32=255", "265.31=0.32=255 266.31=0.32=255 267.31=0.32=255 268.31=0.32=255"},
		{"317.31=0.32=255,316.31=0.32=255", "317.31=0.32=255 316.31=0.32=255"},
		{"16.3.25(1-7)", "16.3.251 16.3.252 16.3.253 16.3.254 16.3.255 16.3.256 16.3.257"},
		{"16.3.2(53-48)", "16.3.253 16.3.252 16.3.251 16.3.250 16.3.249 16.3.248"},
		{"300:0", "300"},
		{"10:-3", "10 9 8"},
	}
	for _, tt := range tests {
		got, err := expandCVs(tt.attr)
		if err != nil || strings.Join(got, " ") != tt.want {
			t.Errorf("expandCVs(%q) = %q, %v; want %q", tt.attr, got, err, tt.want)
		}
	}
	for _, bad := range []string{"1(2", "a:b", "x-3"} {
		if got, err := expandCVs(bad); err == nil {
			t.Errorf("expandCVs(%q) = %q, want an error", bad, got)
		}
	}
}

func TestResolve(t *testing.T) {
	esu := Programming{Index: &Indexing{PI: 31, SI: 32}}
	qsi := Programming{Index: &Indexing{PI: 49, SI: 50, CVFirst: true}}
	tcs := Programming{TCS: true}
	tests := []struct {
		p    Programming
		name string
		want string
	}{
		{Programming{}, "29", "CV29"},
		{esu, "16.0.259", "W31=16 W32=0 CV259"},
		{esu, "0.255.285", "W31=0 W32=255 CV285"},
		{esu, "16.259", "W31=16 CV259"},
		{qsi, "56.36.1", "W49=36 W50=1 CV56"},
		{qsi, "51.0", "W49=0 CV51"},
		{Programming{Index: &Indexing{PI: 31, SI: 32, CVFirst: true}}, "512.31=8.32=1", "W31=8 W32=1 CV512"},
		{Programming{Index: &Indexing{PI: 1021, CVFirst: true}}, "997.1021=2", "W1021=2 CV997"},
		{tcs, "T2CV.1.05", "T2CV 1 5"},
		{tcs, "T3CV.6.0.1", "T3CV 6 0 1"},
	}
	for _, tt := range tests {
		a, err := tt.p.Resolve(tt.name)
		if err != nil {
			t.Errorf("Resolve(%q): %v", tt.name, err)
			continue
		}
		if got := describeAccess(a); got != tt.want {
			t.Errorf("Resolve(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
	bad := []struct {
		p    Programming
		name string
	}{
		{Programming{}, "16.0.259"},
		{Programming{}, "T2CV.1.1"},
		{Programming{}, "0"},
		{Programming{}, "1025"},
		{Programming{}, "csOpSw.5"},
		{esu, "1.2.3.4"},
		{esu, "16.300.259"},
		{tcs, "T2CV.1"},
	}
	for _, tt := range bad {
		if a, err := tt.p.Resolve(tt.name); err == nil {
			t.Errorf("Resolve(%q) = %q, want an error", tt.name, describeAccess(a))
		}
	}
}

func describeAccess(a Access) string {
	switch a.Kind {
	case AccessTCS16:
		return fmt.Sprintf("T2CV %d %d", a.TCS[0], a.TCS[1])
	case AccessTCS8:
		return fmt.Sprintf("T3CV %d %d %d", a.TCS[0], a.TCS[1], a.TCS[2])
	}
	var parts []string
	for _, w := range a.Index {
		parts = append(parts, fmt.Sprintf("W%d=%d", w.CV, w.Value))
	}
	return strings.Join(append(parts, fmt.Sprintf("CV%d", a.CV)), " ")
}

// fakeBus is a decoder's CVs, with a log of every operation.
type fakeBus struct {
	cvs  map[int]int
	log  []string
	fail map[string]bool
	// onWrite lets a test act like a decoder that reacts to writes.
	onWrite func(cv, value int)
}

func (b *fakeBus) ReadCV(_ context.Context, cv int) (int, error) {
	op := fmt.Sprintf("R%d", cv)
	b.log = append(b.log, op)
	if b.fail[op] {
		return 0, errors.New("no acknowledgment")
	}
	return b.cvs[cv], nil
}

func (b *fakeBus) WriteCV(_ context.Context, cv, value int) error {
	op := fmt.Sprintf("W%d=%d", cv, value)
	b.log = append(b.log, op)
	if b.fail[op] {
		return errors.New("no acknowledgment")
	}
	b.cvs[cv] = value
	if b.onWrite != nil {
		b.onWrite(cv, value)
	}
	return nil
}

func newTestSession() *Session {
	s := NewSession()
	s.sleep = func(context.Context, time.Duration) error { return nil }
	return s
}

func TestSessionIndexCache(t *testing.T) {
	esu := Programming{Index: &Indexing{PI: 31, SI: 32, Cache: true}}
	bus := &fakeBus{cvs: map[int]int{259: 7, 260: 9}}
	s := newTestSession()
	ctx := context.Background()
	read := func(name string, cache bool) int {
		t.Helper()
		a, err := esu.Resolve(name)
		if err != nil {
			t.Fatal(err)
		}
		v, err := s.Read(ctx, bus, a, cache)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := read("16.0.259", true); v != 7 {
		t.Fatalf("read = %d", v)
	}
	if v := read("16.0.260", true); v != 9 {
		t.Fatalf("read = %d", v)
	}
	read("16.1.260", true)
	read("16.1.261", false)
	// A plain write to an index CV means the decoder no longer holds what
	// the session wrote there.
	if err := s.Write(ctx, bus, Access{Name: "32", CV: 32}, 5, true); err != nil {
		t.Fatal(err)
	}
	read("16.1.261", true)
	want := "W31=16 W32=0 R259 R260 W31=16 W32=1 R260 W31=16 W32=1 R261 W32=5 W31=16 W32=1 R261"
	if got := strings.Join(bus.log, " "); got != want {
		t.Errorf("operations:\n got %s\nwant %s", got, want)
	}

	bus.log = nil
	s.Forget()
	read("16.1.261", true)
	if got := strings.Join(bus.log, " "); got != "W31=16 W32=1 R261" {
		t.Errorf("after Forget: %s", got)
	}

	// A failed index write stops the access and forgets the index.
	bus.log, bus.fail = nil, map[string]bool{"W32=2": true}
	a, _ := esu.Resolve("16.2.300")
	if _, err := s.Read(ctx, bus, a, true); err == nil || !strings.Contains(err.Error(), "index CV 32") {
		t.Fatalf("failed index write = %v", err)
	}
	bus.fail = nil
	read("16.1.261", true)
	if got := strings.Join(bus.log, " "); got != "W31=16 W32=2 W31=16 W32=1 R261" {
		t.Errorf("after a failure: %s", got)
	}
}

func TestSessionTCS(t *testing.T) {
	tcs := Programming{TCS: true}
	bus := &fakeBus{cvs: map[int]int{202: 45}}
	// A TCS decoder answers a strobe of CV 204 by putting the value in
	// CVs 203 and 204.
	bus.onWrite = func(cv, value int) {
		if cv == 204 && value == 0 && bus.cvs[201] == 101 {
			bus.cvs[203], bus.cvs[204] = 0x04, 0x7C
		}
	}
	s := newTestSession()
	ctx := context.Background()
	a, _ := tcs.Resolve("T2CV.1.05")
	v, err := s.Read(ctx, bus, a, true)
	if err != nil || v != 0x047C {
		t.Fatalf("T2CV read = %d, %v", v, err)
	}
	if err := s.Write(ctx, bus, a, 1148, true); err != nil {
		t.Fatal(err)
	}
	b, _ := tcs.Resolve("T3CV.6.0.1")
	bus.cvs[202] = 45
	if v, err := s.Read(ctx, bus, b, true); err != nil || v != 45 {
		t.Fatalf("T3CV read = %d, %v", v, err)
	}
	if err := s.Write(ctx, bus, b, 50, true); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"W201=101", "W202=5", "W204=0", "R203", "R204",
		"W201=1", "W202=5", "W203=4", "W204=124",
		"W201=106", "W203=0", "W204=1", "R202",
		"W201=6", "W202=50", "W203=0", "W204=1",
	}
	if !slices.Equal(bus.log, want) {
		t.Errorf("operations:\n got %v\nwant %v", bus.log, want)
	}
	if err := s.Write(ctx, bus, a, 70000, true); err == nil {
		t.Error("a 17-bit TCS value was written")
	}
}
