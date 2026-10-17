// Package nodeconfig reads and writes node configuration through the
// node's CDI layout: values go in and out as strings keyed by address, and
// the layout says how each is encoded.
package nodeconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/memconfig"
)

// Memory is the part of the memory configuration client the service uses.
type Memory interface {
	ReadAll(ctx context.Context, id openlcb.NodeID, space uint8, address uint32, size int, progress memconfig.Progress) ([]byte, error)
	Write(ctx context.Context, id openlcb.NodeID, space uint8, address uint32, data []byte) error
	UpdateComplete(ctx context.Context, id openlcb.NodeID) error
	Reboot(ctx context.Context, id openlcb.NodeID) error
	FactoryReset(ctx context.Context, id openlcb.NodeID) error
}

// Layouts supplies parsed CDIs.
type Layouts interface {
	CDI(id openlcb.NodeID) (*cdi.CDI, []byte, bool)
}

// ErrNoLayout is returned when the node's CDI is not available, so values
// cannot be interpreted.
var ErrNoLayout = errors.New("nodeconfig: the node's configuration description is not available")

// Service ties the layout to the memory client.
type Service struct {
	mem     Memory
	layouts Layouts
	log     *slog.Logger
	written func(openlcb.NodeID)
}

// OnWritten sets a function called after values are written to a node,
// so caches of its configuration can be refreshed.
func (s *Service) OnWritten(fn func(openlcb.NodeID)) {
	s.written = fn
}

// New returns a service.
func New(mem Memory, layouts Layouts, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{mem: mem, layouts: layouts, log: logger}
}

// MaxRead bounds one Read request.
const MaxRead = 64 * 1024

// Values maps variable keys ("space:address") to string values.
type Values map[string]string

// ReadResult is what a Read returns.
type ReadResult struct {
	Values Values `json:"values"`
	// Errors lists variables in the range whose bytes did not decode.
	Errors map[string]string `json:"errors,omitempty"`
	// Bytes is how many bytes the node returned, fewer than asked at the
	// end of a space.
	Bytes int `json:"bytes"`
}

// Read fetches size bytes at address in space and decodes every variable
// of the layout that lies within them.
func (s *Service) Read(ctx context.Context, id openlcb.NodeID, space uint8, address uint32, size int) (*ReadResult, error) {
	if size <= 0 || size > MaxRead {
		return nil, fmt.Errorf("nodeconfig: size must be 1 to %d", MaxRead)
	}
	doc, _, ok := s.layouts.CDI(id)
	if !ok {
		return nil, ErrNoLayout
	}
	data, err := s.mem.ReadAll(ctx, id, space, address, size, nil)
	if err != nil {
		return nil, err
	}
	result := &ReadResult{Values: Values{}, Bytes: len(data)}
	end := address + uint32(len(data))
	for _, v := range doc.Variables() {
		if v.Space != space || v.Address < address || v.Address+uint32(v.Size) > end || v.Size == 0 {
			continue
		}
		value, err := v.Element.Decode(data[v.Address-address:])
		if err != nil {
			if result.Errors == nil {
				result.Errors = map[string]string{}
			}
			result.Errors[v.Key()] = err.Error()
			continue
		}
		result.Values[v.Key()] = value
	}
	return result, nil
}

// WriteResult says how a Write went.
type WriteResult struct {
	// Written lists the keys stored, in the order written.
	Written []string `json:"written"`
	// Invalid lists values refused before anything was written.
	Invalid map[string]string `json:"invalid,omitempty"`
	// Failed is the key whose write the node refused, with the reason;
	// keys after it in address order were not attempted.
	Failed      string `json:"failed,omitempty"`
	FailedError string `json:"failed_error,omitempty"`
}

// InvalidError is returned when values do not encode; nothing was written.
type InvalidError struct {
	Invalid map[string]string
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("nodeconfig: %d values are not valid", len(e.Invalid))
}

// Write encodes every value against the layout, refusing the whole batch
// if any does not fit, then writes them in address order. After the
// writes it sends Update Complete so the node applies them.
func (s *Service) Write(ctx context.Context, id openlcb.NodeID, values Values) (*WriteResult, error) {
	doc, _, ok := s.layouts.CDI(id)
	if !ok {
		return nil, ErrNoLayout
	}
	index := doc.Lookup()
	type pending struct {
		key  string
		v    cdi.Variable
		data []byte
	}
	var batch []pending
	invalid := map[string]string{}
	for key, value := range values {
		v, ok := index[key]
		if !ok {
			invalid[key] = "no such variable in the node's layout"
			continue
		}
		data, err := v.Element.Encode(value)
		if err != nil {
			invalid[key] = err.Error()
			continue
		}
		batch = append(batch, pending{key: key, v: v, data: data})
	}
	if len(invalid) > 0 {
		return &WriteResult{Invalid: invalid}, &InvalidError{Invalid: invalid}
	}
	sort.Slice(batch, func(i, j int) bool {
		if batch[i].v.Space != batch[j].v.Space {
			return batch[i].v.Space < batch[j].v.Space
		}
		return batch[i].v.Address < batch[j].v.Address
	})
	result := &WriteResult{Written: []string{}}
	defer func() {
		if len(result.Written) > 0 {
			s.updateComplete(ctx, id)
			if s.written != nil {
				s.written(id)
			}
		}
	}()
	for _, p := range batch {
		if err := s.mem.Write(ctx, id, p.v.Space, p.v.Address, p.data); err != nil {
			result.Failed = p.key
			result.FailedError = err.Error()
			return result, err
		}
		result.Written = append(result.Written, p.key)
	}
	return result, nil
}

func (s *Service) updateComplete(ctx context.Context, id openlcb.NodeID) {
	if err := s.mem.UpdateComplete(ctx, id); err != nil {
		// Nodes that do not implement it may reject it; the writes stand.
		s.log.Debug("update complete not accepted", "node", id, "error", err)
	}
}

// Reboot restarts the node.
func (s *Service) Reboot(ctx context.Context, id openlcb.NodeID) error {
	return s.mem.Reboot(ctx, id)
}

// FactoryReset returns the node to factory defaults.
func (s *Service) FactoryReset(ctx context.Context, id openlcb.NodeID) error {
	return s.mem.FactoryReset(ctx, id)
}
