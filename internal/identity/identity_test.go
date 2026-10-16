package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/mikesmitty/interlock/openlcb"
)

type fakeSettings struct {
	values map[string]string
	err    error
}

func (f *fakeSettings) Setting(_ context.Context, key string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.values[key]
	return v, ok, nil
}

func (f *fakeSettings) SetSetting(_ context.Context, key, value string) error {
	if f.err != nil {
		return f.err
	}
	if f.values == nil {
		f.values = map[string]string{}
	}
	f.values[key] = value
	return nil
}

func TestResolve(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		configured string
		stored     string
		want       openlcb.NodeID
		wantLocal  bool
		wantErr    bool
	}{
		{name: "configured", configured: "05.01.01.01.22.00", want: 0x050101012200},
		{name: "configured wins over stored", configured: "05.01.01.01.22.00", stored: "0A.00.00.00.00.01", want: 0x050101012200},
		{name: "configured invalid", configured: "nope", wantErr: true},
		{name: "stored", stored: "0A.00.00.00.00.01", want: 0x0A0000000001},
		{name: "stored invalid", stored: "garbage", wantErr: true},
		{name: "generated", wantLocal: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &fakeSettings{values: map[string]string{}}
			if tt.stored != "" {
				settings.values[SettingKey] = tt.stored
			}
			got, err := Resolve(ctx, tt.configured, settings)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Resolve = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if tt.wantLocal {
				if !got.IsLocal() || !got.Valid() {
					t.Fatalf("Resolve = %v, want a valid locally assigned ID", got)
				}
				if settings.values[SettingKey] != got.String() {
					t.Fatalf("generated ID %v was not saved (settings = %v)", got, settings.values)
				}
				again, err := Resolve(ctx, "", settings)
				if err != nil {
					t.Fatalf("second Resolve: %v", err)
				}
				if again != got {
					t.Fatalf("second Resolve = %v, want the saved %v", again, got)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("Resolve = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveStoreError(t *testing.T) {
	boom := errors.New("boom")
	_, err := Resolve(context.Background(), "", &fakeSettings{err: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("Resolve error = %v, want %v", err, boom)
	}
}
