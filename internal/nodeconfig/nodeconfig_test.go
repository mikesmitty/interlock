package nodeconfig

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/sim"
)

// fakeMemory is a node's configuration space in memory.
type fakeMemory struct {
	data     []byte
	writes   []string
	complete int
	failAt   uint32 // address whose write fails, 0 for none
}

func (f *fakeMemory) ReadAll(_ context.Context, _ openlcb.NodeID, space uint8, address uint32, size int, _ memconfig.Progress) ([]byte, error) {
	if space != memconfig.SpaceConfig {
		return nil, &openlcb.Error{Code: memconfig.ErrCodeSpaceUnknown}
	}
	if int(address) >= len(f.data) {
		return nil, &openlcb.Error{Code: memconfig.ErrCodeOutOfBounds}
	}
	end := min(int(address)+size, len(f.data))
	return append([]byte{}, f.data[address:end]...), nil
}

func (f *fakeMemory) Write(_ context.Context, _ openlcb.NodeID, _ uint8, address uint32, data []byte) error {
	if f.failAt != 0 && address == f.failAt {
		return &openlcb.Error{Code: memconfig.ErrCodeWriteReadOnly, Op: "write"}
	}
	copy(f.data[address:], data)
	f.writes = append(f.writes, cdi.Key(memconfig.SpaceConfig, address))
	return nil
}

func (f *fakeMemory) UpdateComplete(context.Context, openlcb.NodeID) error { f.complete++; return nil }
func (f *fakeMemory) Reboot(context.Context, openlcb.NodeID) error         { return nil }
func (f *fakeMemory) FactoryReset(context.Context, openlcb.NodeID) error   { return nil }

type fakeLayouts struct{ doc *cdi.CDI }

func (l fakeLayouts) CDI(openlcb.NodeID) (*cdi.CDI, []byte, bool) {
	if l.doc == nil {
		return nil, nil, false
	}
	return l.doc, nil, true
}

func TestReadAndWrite(t *testing.T) {
	doc, err := cdi.Parse(sim.OutpostCDI)
	if err != nil {
		t.Fatal(err)
	}
	mem := &fakeMemory{data: make([]byte, 996)}
	// Servo 1 thrown position = 16384, closed = 49152, description "Main".
	copy(mem.data[172:], []byte{0x40, 0x00, 0xC0, 0x00})
	copy(mem.data[185:], "Main")
	copy(mem.data[132:], []byte{5, 1, 1, 1, 0x22, 0, 0, 1})
	svc := New(mem, fakeLayouts{doc}, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	id := openlcb.NodeID(0x0A0000000042)

	res, err := svc.Read(ctx, id, memconfig.SpaceConfig, 132, 120)
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes != 120 || len(res.Errors) != 0 {
		t.Errorf("result = %+v", res)
	}
	want := map[string]string{
		"253:132": "05.01.01.01.22.00.00.01",
		"253:172": "16384",
		"253:174": "49152",
		"253:185": "Main",
		"253:180": "0",
	}
	for k, v := range want {
		if res.Values[k] != v {
			t.Errorf("%s = %q, want %q", k, res.Values[k], v)
		}
	}
	if _, ok := res.Values["253:252"]; ok {
		t.Error("value outside the range was decoded")
	}
	// A variable cut by the end of the range is left out.
	res, err = svc.Read(ctx, id, memconfig.SpaceConfig, 132, 173-132)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Values["253:172"]; ok {
		t.Error("partially covered variable was decoded")
	}

	// Write: invalid values refuse the whole batch.
	wr, err := svc.Write(ctx, id, Values{"253:172": "70000", "253:185": "Siding"})
	var inv *InvalidError
	if !errors.As(err, &inv) || len(wr.Invalid) != 1 || wr.Invalid["253:172"] == "" {
		t.Fatalf("Write with bad value = %+v, %v", wr, err)
	}
	if len(mem.writes) != 0 {
		t.Error("something was written despite an invalid value")
	}
	wr, err = svc.Write(ctx, id, Values{"253:9999": "1"})
	if !errors.As(err, &inv) || !strings.Contains(wr.Invalid["253:9999"], "no such variable") {
		t.Errorf("unknown key = %+v, %v", wr, err)
	}

	// A good batch is written in address order then Update Complete.
	wr, err = svc.Write(ctx, id, Values{"253:185": "Siding", "253:172": "20000", "253:180": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(wr.Written, ",") != "253:172,253:180,253:185" {
		t.Errorf("written = %v", wr.Written)
	}
	if mem.complete != 1 {
		t.Errorf("update complete sent %d times", mem.complete)
	}
	if string(mem.data[185:191]) != "Siding" || mem.data[172] != 0x4E || mem.data[173] != 0x20 || mem.data[180] != 1 {
		t.Errorf("memory after write = % X %q", mem.data[172:181], mem.data[185:191])
	}

	// A refused write stops the batch and reports where.
	mem.writes = nil
	mem.failAt = 180
	wr, err = svc.Write(ctx, id, Values{"253:172": "1", "253:180": "0", "253:185": "X"})
	if err == nil || wr.Failed != "253:180" || len(wr.Written) != 1 || wr.Written[0] != "253:172" {
		t.Errorf("failed write = %+v, %v", wr, err)
	}

	// No layout, no service.
	none := New(mem, fakeLayouts{}, nil)
	if _, err := none.Read(ctx, id, memconfig.SpaceConfig, 0, 8); !errors.Is(err, ErrNoLayout) {
		t.Errorf("Read without layout = %v", err)
	}
	if _, err := none.Write(ctx, id, Values{"253:0": "x"}); !errors.Is(err, ErrNoLayout) {
		t.Errorf("Write without layout = %v", err)
	}
	if _, err := svc.Read(ctx, id, memconfig.SpaceConfig, 0, 0); err == nil {
		t.Error("Read of size 0 accepted")
	}
}
