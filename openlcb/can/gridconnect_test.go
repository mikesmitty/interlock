package can

import (
	"errors"
	"testing"
)

func TestParseGridConnect(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Frame
		wantErr bool
	}{
		{name: "extended no data", in: ":X10700123N;", want: Frame{ID: 0x10700123, Extended: true}},
		{name: "extended data", in: ":X19100123N050101012200;", want: Frame{ID: 0x19100123, Extended: true, Len: 6, Data: [8]byte{5, 1, 1, 1, 0x22, 0}}},
		{name: "lower case", in: ":x19100123n05aabb;", want: Frame{ID: 0x19100123, Extended: true, Len: 3, Data: [8]byte{5, 0xAA, 0xBB}}},
		{name: "short id", in: ":X123N;", want: Frame{ID: 0x123, Extended: true}},
		{name: "standard", in: ":S7FFN01;", want: Frame{ID: 0x7FF, Len: 1, Data: [8]byte{1}}},
		{name: "remote", in: ":X10700123R;", want: Frame{ID: 0x10700123, Extended: true, RTR: true}},
		{name: "eight bytes", in: ":X1A456123N0001020304050607;", want: Frame{ID: 0x1A456123, Extended: true, Len: 8, Data: [8]byte{0, 1, 2, 3, 4, 5, 6, 7}}},
		{name: "nine bytes", in: ":X1A456123N000102030405060708;", wantErr: true},
		{name: "odd digits", in: ":X19100123N050;", wantErr: true},
		{name: "no id", in: ":XN;", wantErr: true},
		{name: "id too long", in: ":X119100123N;", wantErr: true},
		{name: "id too big", in: ":XFFFFFFFFN;", wantErr: true},
		{name: "standard id too big", in: ":S800N;", wantErr: true},
		{name: "bad type", in: ":Y123N;", wantErr: true},
		{name: "bad separator", in: ":X123Q;", wantErr: true},
		{name: "bad hex", in: ":X123NZZ;", wantErr: true},
		{name: "two frames", in: ":X123N;:X456N;", wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "no terminator", in: ":X123N", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGridConnect(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrGridConnect) {
					t.Fatalf("ParseGridConnect(%q) = %v, %v; want ErrGridConnect", tt.in, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseGridConnect(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseGridConnect(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestGridConnectRoundTrip(t *testing.T) {
	frames := []Frame{
		New(0x19100123, []byte{5, 1, 1, 1, 0x22, 0}),
		New(0x10700123, nil),
		{ID: 0x7FF, Len: 2, Data: [8]byte{0xAB, 0xCD}},
		{ID: 0x1FFFFFFF, Extended: true, RTR: true},
	}
	for _, f := range frames {
		text := f.String()
		back, err := ParseGridConnect(text)
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		if back != f {
			t.Errorf("%s round trip = %+v, want %+v", text, back, f)
		}
	}
	if got := New(0x19100123, nil).String(); got != ":X19100123N;" {
		t.Errorf("String() = %s", got)
	}
}

func TestGridConnectParserStream(t *testing.T) {
	var p GridConnectParser
	var got []Frame
	chunks := []string{
		"garbage before\r\n:X1070",
		"0123N;\n:X19100123N0501",
		"01012200;:S7FFN01;\n",
		":X19100123N05;:XBROKEN;:X10702123N;",
		"::X10700456N;", // restart mid frame
	}
	for _, chunk := range chunks {
		got = append(got, p.Push([]byte(chunk))...)
	}
	want := []string{":X10700123N;", ":X19100123N050101012200;", ":S7FFN01;", ":X19100123N05;", ":X10702123N;", ":X10700456N;"}
	if len(got) != len(want) {
		t.Fatalf("got %d frames %v, want %d", len(got), got, len(want))
	}
	for i, f := range got {
		if f.String() != want[i] {
			t.Errorf("frame %d = %s, want %s", i, f, want[i])
		}
	}
	if p.Errors != 1 {
		t.Errorf("Errors = %d, want 1 (the BROKEN frame)", p.Errors)
	}
}

func TestGridConnectParserBoundsGarbage(t *testing.T) {
	var p GridConnectParser
	long := make([]byte, 1000)
	for i := range long {
		long[i] = 'A'
	}
	if frames := p.Push(append([]byte(":X"), long...)); len(frames) != 0 {
		t.Fatalf("got frames from garbage: %v", frames)
	}
	if p.Errors != 1 {
		t.Errorf("Errors = %d, want 1", p.Errors)
	}
	if frames := p.Push([]byte(":X10700123N;")); len(frames) != 1 {
		t.Fatalf("parser did not recover: %v", frames)
	}
}
