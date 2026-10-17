// Package sim builds simulated LCC nodes from real CDI documents, for
// developing and testing Interlock without hardware. A simulated node
// answers identification, serves its CDI and a configuration space laid
// out as the CDI describes, with defaults filled in, and accepts writes.
package sim

import (
	_ "embed"
	"fmt"
	"log/slog"
	"sync"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
	"github.com/mikesmitty/interlock/openlcb/node"
)

// CDI documents of the devices Interlock was built against, copied from
// their firmware.
var (
	//go:embed canbus-outpost.cdi.xml
	OutpostCDI []byte
	//go:embed simple-dcc.cdi.xml
	CommandStationCDI []byte
)

// Node is a simulated node.
type Node struct {
	*node.Node
	// Doc is the parsed CDI.
	Doc *cdi.CDI
	// Server exposes the spaces; Config is space 0xFD.
	Server *memconfig.Server
	Config *memconfig.Memory
	// Client lets the simulated node read other nodes too.
	Client *memconfig.Client

	mu     sync.Mutex
	resets int
}

// New builds a node from cfg and a CDI document. The SNIP manufacturer,
// model and versions default to the CDI's identification; the protocols
// default to what a configurable node needs. cfg.Send must be set.
func New(cfg node.Config, cdiXML []byte) (*Node, error) {
	doc, err := cdi.Parse(cdiXML)
	if err != nil {
		return nil, fmt.Errorf("sim: %w", err)
	}
	if ident := doc.Identification; ident != nil {
		if cfg.SNIP.Manufacturer == "" {
			cfg.SNIP.Manufacturer = ident.Manufacturer
		}
		if cfg.SNIP.Model == "" {
			cfg.SNIP.Model = ident.Model
		}
		if cfg.SNIP.HardwareVersion == "" {
			cfg.SNIP.HardwareVersion = ident.HardwareVersion
		}
		if cfg.SNIP.SoftwareVersion == "" {
			cfg.SNIP.SoftwareVersion = ident.SoftwareVersion
		}
	}
	if cfg.Protocols == 0 {
		cfg.Protocols = openlcb.ProtocolSimple | openlcb.ProtocolDatagram | openlcb.ProtocolMemoryConfiguration |
			openlcb.ProtocolEventExchange | openlcb.ProtocolSimpleNodeInformation | openlcb.ProtocolCDI
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	s := &Node{Node: node.New(cfg), Doc: doc}
	initial := defaultConfig(doc)
	seedUserStrings(doc, initial, cfg.SNIP.UserName, cfg.SNIP.UserDescription)
	s.Config = memconfig.NewMemory(initial, false)
	s.Server = memconfig.NewServer()
	s.Server.Name = cfg.SNIP.Model
	s.Server.AddSpace(memconfig.SpaceCDI, memconfig.NewMemory(append(append([]byte{}, cdiXML...), 0), true))
	s.Server.AddSpace(memconfig.SpaceConfig, s.Config)
	s.Server.OnFactoryReset = func() {
		_ = s.Config.Write(0, defaultConfig(doc))
		cfg.Logger.Info("sim: factory reset")
		s.mu.Lock()
		s.resets++
		s.mu.Unlock()
	}
	s.Server.OnReboot = func() { cfg.Logger.Info("sim: reboot requested") }
	s.Server.OnUpdateComplete = func() { cfg.Logger.Info("sim: update complete") }
	s.Client = memconfig.NewClient(s.Node, memconfig.WithServer(s.Server), memconfig.WithLogger(cfg.Logger))
	s.syncSNIP()
	s.Config.OnWrite = func(uint32, int) { s.syncSNIP() }
	return s, nil
}

// FactoryResets counts how many times the node was reset.
func (s *Node) FactoryResets() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resets
}

// defaultConfig builds space 0xFD from the CDI: large enough for every
// segment in that space, with each variable's default value in place.
func defaultConfig(doc *cdi.CDI) []byte {
	var size uint32
	for _, seg := range doc.Segments {
		if seg.Space == memconfig.SpaceConfig && seg.Origin+seg.Size > size {
			size = seg.Origin + seg.Size
		}
	}
	data := make([]byte, size)
	for _, v := range doc.Variables() {
		if v.Space != memconfig.SpaceConfig || v.Element.Default == "" {
			continue
		}
		copy(data[v.Address:], v.Element.DefaultBytes())
	}
	return data
}

// userStrings returns the first two named strings of the configuration
// space, conventionally the user name and description.
func userStrings(doc *cdi.CDI) []cdi.Variable {
	var strs []cdi.Variable
	for _, v := range doc.Variables() {
		if v.Space == memconfig.SpaceConfig && v.Element.Kind == cdi.KindString && v.Element.Name != "" {
			strs = append(strs, v)
		}
		if len(strs) == 2 {
			break
		}
	}
	return strs
}

// seedUserStrings writes the initial user name and description.
func seedUserStrings(doc *cdi.CDI, data []byte, name, description string) {
	strs := userStrings(doc)
	for i, value := range []string{name, description} {
		if i >= len(strs) || value == "" {
			continue
		}
		if b, err := strs[i].Element.Encode(value); err == nil {
			copy(data[strs[i].Address:], b)
		}
	}
}

// syncSNIP mirrors the first two strings of the configuration space,
// conventionally user name and description, into the SNIP, as the real
// devices do.
func (s *Node) syncSNIP() {
	strs := userStrings(s.Doc)
	if len(strs) < 2 {
		return
	}
	data := s.Config.Bytes()
	name, _ := strs[0].Element.Decode(data[strs[0].Address:])
	desc, _ := strs[1].Element.Decode(data[strs[1].Address:])
	s.SetUserSNIP(name, desc)
}
