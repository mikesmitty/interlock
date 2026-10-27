package replace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/eventtable"
	"github.com/mikesmitty/interlock/internal/refs"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/openlcb"
)

const (
	oldNode  = openlcb.NodeID(0x0aff9024a634)
	newNode  = openlcb.NodeID(0x050101012200)
	local    = openlcb.NodeID(0x0ac0df13656d)
	elsewise = openlcb.NodeID(0x0a737c38ad26)
)

type fakeNodes struct {
	infos     map[openlcb.NodeID]registry.Info
	forgotten []openlcb.NodeID
}

func (f *fakeNodes) Get(id openlcb.NodeID) (registry.Info, bool) {
	info, ok := f.infos[id]
	return info, ok
}

func (f *fakeNodes) Forget(_ context.Context, id openlcb.NodeID) error {
	f.forgotten = append(f.forgotten, id)
	delete(f.infos, id)
	return nil
}

type fakeEvents map[openlcb.NodeID][]eventtable.Entry

func (f fakeEvents) NodeEntries(id openlcb.NodeID) []eventtable.Entry { return f[id] }

type fakeEntities map[string]bool

func (f fakeEntities) Get(id string) (entity.Entity, bool) { return entity.Entity{ID: id}, f[id] }

// part holds a few references and rewrites them as the real parts do.
type part struct {
	entities []string
	events   []openlcb.EventID
	nodes    []openlcb.NodeID
	applied  bool
}

func (p *part) Rewrite(rw refs.Rewrite, apply bool) ([]refs.Change, error) {
	var out []refs.Change
	for i := range p.entities {
		id := p.entities[i]
		if c, ok := rw.RenameEntity(&id); ok {
			out = append(out, c.In("Test", "entity", ""))
			if apply {
				p.entities[i] = id
			}
		}
	}
	for i := range p.events {
		ev := p.events[i]
		if c, ok := rw.RenameEvent(&ev); ok {
			out = append(out, c.In("Test", "event", ""))
			if apply {
				p.events[i] = ev
			}
		}
	}
	for i := range p.nodes {
		n := p.nodes[i]
		if c, ok := rw.RenameNode(&n); ok {
			out = append(out, c.In("Test", "node", ""))
			if apply {
				p.nodes[i] = n
			}
		}
	}
	p.applied = p.applied || apply
	return out, nil
}

func outpost(id openlcb.NodeID, online bool) registry.Info {
	return registry.Info{ID: id, Online: online, SNIP: &openlcb.SNIP{Manufacturer: "mikesmitty", Model: "CAN Bus Outpost"}}
}

func setup() (*Replacer, *fakeNodes, *part) {
	nodes := &fakeNodes{infos: map[openlcb.NodeID]registry.Info{
		oldNode: outpost(oldNode, false),
		newNode: outpost(newNode, true),
		local:   {ID: local, Local: true},
	}}
	events := fakeEvents{
		oldNode: {
			{Key: "253:100", Event: 0x0aff9024a6340000}, // a default that moved
			{Key: "253:108", Event: 0x0501010122000042}, // one set by hand, kept
			{Key: "253:116", Event: 0},                  // not set
		},
		newNode: {
			{Key: "253:100", Event: 0x0501010122000000},
			{Key: "253:108", Event: 0x0501010122000042},
			{Key: "253:116", Event: 0x0501010122000010},
		},
	}
	ents := fakeEntities{"turnout.050101012200.servo_1": true}
	p := &part{
		entities: []string{"turnout.0aff9024a634.servo_1", "block.0aff9024a634.track_1", "turnout.0a737c38ad26.servo_1"},
		events:   []openlcb.EventID{0x0aff9024a6340000, 0x0501010122000042},
		nodes:    []openlcb.NodeID{oldNode, elsewise},
	}
	return &Replacer{Nodes: nodes, Events: events, Entities: ents, Parts: []Part{p}}, nodes, p
}

func TestReplacePreviewAndApply(t *testing.T) {
	r, nodes, p := setup()
	res, err := r.Replace(context.Background(), oldNode, newNode, false, true)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range res.Changes {
		line := c.Item + " " + c.From + " > " + c.To
		if c.Missing {
			line += " (missing)"
		}
		got = append(got, line)
	}
	want := []string{
		"entity turnout.0aff9024a634.servo_1 > turnout.050101012200.servo_1",
		"entity block.0aff9024a634.track_1 > block.050101012200.track_1 (missing)",
		"event 0A.FF.90.24.A6.34.00.00 > 05.01.01.01.22.00.00.00",
		"node 0A.FF.90.24.A6.34 > 05.01.01.01.22.00",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if res.Applied || p.applied || len(nodes.forgotten) != 0 {
		t.Error("a preview changed something")
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "1 of the new node's entities") {
		t.Errorf("notes = %q", res.Notes)
	}

	res, err = r.Replace(context.Background(), oldNode, newNode, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applied || !res.Forgotten || len(nodes.forgotten) != 1 || nodes.forgotten[0] != oldNode {
		t.Errorf("applied %v, forgotten %v %v", res.Applied, res.Forgotten, nodes.forgotten)
	}
	if p.entities[0] != "turnout.050101012200.servo_1" || p.entities[2] != "turnout.0a737c38ad26.servo_1" || p.events[0] != 0x0501010122000000 || p.nodes[0] != newNode || p.nodes[1] != elsewise {
		t.Errorf("part after = %+v", p)
	}

	// The old node is gone now, so there is nothing left to forget, and
	// no record of its events to match.
	res, err = r.Replace(context.Background(), oldNode, newNode, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Forgotten || len(res.Changes) != 0 {
		t.Errorf("a second run = %+v", res)
	}
}

func TestReplaceNotes(t *testing.T) {
	r, nodes, _ := setup()
	other := outpost(newNode, true)
	other.SNIP = &openlcb.SNIP{Manufacturer: "someone", Model: "Signal Driver"}
	nodes.infos[newNode] = other
	res, err := r.Replace(context.Background(), oldNode, newNode, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Changes {
		if c.Kind == refs.KindEvent {
			t.Errorf("events of a different model were matched: %+v", c)
		}
	}
	if len(res.Notes) == 0 || !strings.Contains(res.Notes[0], "aren't the same model") {
		t.Errorf("notes = %q", res.Notes)
	}
}

func TestReplaceRefuses(t *testing.T) {
	r, _, _ := setup()
	tests := []struct {
		name     string
		from, to openlcb.NodeID
		want     string
	}{
		{"itself", newNode, newNode, "itself"},
		{"a node never seen", oldNode, 0x050101012299, "hasn't been seen"},
		{"Interlock's own node", oldNode, local, "own node"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Replace(context.Background(), tt.from, tt.to, true, true)
			var invalid *InvalidError
			if !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, tt.want) {
				t.Errorf("Replace = %v, want an *InvalidError about %q", err, tt.want)
			}
		})
	}
}
