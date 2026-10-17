package registry

import (
	"context"
	"errors"
	"time"

	"github.com/mikesmitty/interlock/internal/store"
	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/cdi"
	"github.com/mikesmitty/interlock/openlcb/node"
)

// ErrNodeOnline is returned by Forget for a node that is still present.
var ErrNodeOnline = errors.New("registry: node is online")

// ErrNoCDI is returned when a node's layout is not available.
var ErrNoCDI = errors.New("registry: no CDI for node")

// planCDILocked decides, after identification, whether the node's CDI is
// already known or must be read. The caller holds r.mu.
func (r *Registry) planCDILocked(info *Info) {
	version := ""
	if info.SNIP != nil {
		version = info.SNIP.SoftwareVersion
	}
	if entry, ok := r.cdis[info.ID]; ok && entry.version == version {
		if info.CDI == nil || info.CDI.State != CDIReady {
			info.CDI = &CDIStatus{State: CDIReady, FetchedAt: entry.fetchedAt, Cached: true, Truncated: entry.doc.Truncated, Segments: len(entry.doc.Segments)}
		}
		return
	}
	if info.CDI != nil && (info.CDI.State == CDIReading || info.CDI.State == CDIPending) {
		return
	}
	info.CDI = &CDIStatus{State: CDIPending}
}

func (r *Registry) requestCDI(id openlcb.NodeID) {
	r.mu.Lock()
	if r.cdiBusy[id] {
		r.mu.Unlock()
		return
	}
	r.cdiBusy[id] = true
	r.mu.Unlock()
	select {
	case r.cdiFetch <- id:
	default:
		r.mu.Lock()
		delete(r.cdiBusy, id)
		r.mu.Unlock()
	}
}

// cdiLoop reads CDIs one node at a time; each takes many datagrams.
func (r *Registry) cdiLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-r.cdiFetch:
			r.fetchCDI(ctx, id)
			r.mu.Lock()
			delete(r.cdiBusy, id)
			r.mu.Unlock()
		}
	}
}

// fetchCDI gets the document from the store if the version matches, else
// from the node, and publishes progress along the way.
func (r *Registry) fetchCDI(ctx context.Context, id openlcb.NodeID) {
	r.mu.RLock()
	info, ok := r.nodes[id]
	var version string
	if ok && info.SNIP != nil {
		version = info.SNIP.SoftwareVersion
	}
	r.mu.RUnlock()
	if !ok {
		return
	}

	if r.opts.Store != nil {
		cached, err := r.opts.Store.CDI(ctx, id)
		if err != nil {
			r.log.Warn("could not read cached CDI", "node", id, "error", err)
		} else if cached != nil && cached.SoftwareVersion == version {
			if doc, err := cdi.Parse(cached.XML); err == nil {
				r.setCDI(id, &cdiEntry{xml: cached.XML, doc: doc, version: version, fetchedAt: cached.FetchedAt}, true)
				return
			}
		}
	}

	r.updateCDI(id, func(s *CDIStatus) { *s = CDIStatus{State: CDIReading} })
	last := time.Now()
	progress := func(done, total int) {
		if time.Since(last) < 200*time.Millisecond {
			return
		}
		last = time.Now()
		r.updateCDI(id, func(s *CDIStatus) { s.Read, s.Total = done, total })
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	xml, err := r.opts.CDI.ReadCDI(ctx, id, progress)
	if err != nil {
		r.log.Warn("could not read CDI", "node", id, "error", err)
		r.updateCDI(id, func(s *CDIStatus) { *s = CDIStatus{State: CDIError, Error: err.Error()} })
		return
	}
	doc, err := cdi.Parse(xml)
	if err != nil {
		r.log.Warn("CDI does not parse", "node", id, "error", err, "bytes", len(xml))
		r.updateCDI(id, func(s *CDIStatus) { *s = CDIStatus{State: CDIError, Error: err.Error()} })
		return
	}
	entry := &cdiEntry{xml: xml, doc: doc, version: version, fetchedAt: time.Now()}
	r.log.Info("read CDI", "node", id, "bytes", len(xml), "segments", len(doc.Segments), "truncated", doc.Truncated)
	r.setCDI(id, entry, false)
	if r.opts.Store != nil {
		saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rec := store.CDIRecord{NodeID: id, SoftwareVersion: version, XML: xml, FetchedAt: entry.fetchedAt}
		if err := r.opts.Store.SaveCDI(saveCtx, rec); err != nil {
			r.log.Warn("could not cache CDI", "node", id, "error", err)
		}
	}
}

func (r *Registry) setCDI(id openlcb.NodeID, entry *cdiEntry, cached bool) {
	r.mu.Lock()
	info, ok := r.nodes[id]
	if !ok {
		r.mu.Unlock()
		return
	}
	r.cdis[id] = entry
	info.CDI = &CDIStatus{State: CDIReady, FetchedAt: entry.fetchedAt, Cached: cached, Truncated: entry.doc.Truncated, Segments: len(entry.doc.Segments)}
	snapshot := *info
	r.mu.Unlock()
	r.publish(snapshot)
}

func (r *Registry) updateCDI(id openlcb.NodeID, change func(*CDIStatus)) {
	r.mu.Lock()
	info, ok := r.nodes[id]
	if !ok {
		r.mu.Unlock()
		return
	}
	// Published snapshots share the pointer, so never change the status in
	// place.
	status := CDIStatus{}
	if info.CDI != nil {
		status = *info.CDI
	}
	change(&status)
	info.CDI = &status
	snapshot := *info
	r.mu.Unlock()
	r.publish(snapshot)
}

// CDI returns a node's parsed configuration description and its source
// XML.
func (r *Registry) CDI(id openlcb.NodeID) (*cdi.CDI, []byte, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.cdis[id]
	if !ok {
		return nil, nil, false
	}
	return entry.doc, entry.xml, true
}

// RefreshCDI drops what is known about a node's CDI and reads it again.
func (r *Registry) RefreshCDI(ctx context.Context, id openlcb.NodeID) error {
	r.mu.Lock()
	info, ok := r.nodes[id]
	if !ok {
		r.mu.Unlock()
		return node.ErrNotFound
	}
	if r.opts.CDI == nil || !info.ProtocolBits.Has(openlcb.ProtocolCDI) {
		r.mu.Unlock()
		return ErrNoCDI
	}
	delete(r.cdis, id)
	info.CDI = &CDIStatus{State: CDIPending}
	snapshot := *info
	r.mu.Unlock()
	if r.opts.Store != nil {
		if err := r.opts.Store.DeleteCDI(ctx, id); err != nil {
			return err
		}
	}
	r.publish(snapshot)
	r.requestCDI(id)
	return nil
}
