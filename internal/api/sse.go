package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mikesmitty/interlock/internal/automation"
	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/monitor"
	"github.com/mikesmitty/interlock/internal/programmer"
	"github.com/mikesmitty/interlock/internal/registry"
	"github.com/mikesmitty/interlock/internal/train"
	"github.com/mikesmitty/interlock/openlcb"
)

// The event stream at /api/v1/events uses server-sent events: one
// long-lived GET per browser tab, with named events carrying JSON.
//
//	ready        {"seq": N}         sent first; N is the monitor's newest sequence
//	node         registry.Info      a node appeared, changed or went offline
//	links        {"links": [...]}   link status changed
//	frame        monitor.Entry      bus traffic (topic "monitor")
//	entity       entity.Entity      an entity changed, appeared or was removed
//	event_names  {"node": id}       a node's named events changed
//	automation   automation.Status  an automation changed, ran or was removed (topic "automations")
//	trace        automation.Trace   a run started, took a step or ended (topic "automations")
//	train        train.Train        a train changed, was added or removed (topic "trains")
//	layout       train.Layout       track power or emergency stop changed (topic "trains")
//	cv           programmer.Op      a CV operation finished (topic "trains")
//	lagged       {}                 the client fell behind and entries were skipped
//
// The topics query parameter selects a comma separated subset of
// nodes, links, monitor, entities, automations and trains; the default is
// all of them.

const (
	sseQueue     = 1024
	sseHeartbeat = 15 * time.Second
	ssePollLinks = 2 * time.Second
)

type sseEvent struct {
	name string
	data any
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	flush := func() bool {
		return rc.Flush() == nil
	}
	topics := map[string]bool{"nodes": true, "links": true, "monitor": true, "entities": true, "automations": true, "trains": true}
	if s := r.URL.Query().Get("topics"); s != "" {
		topics = map[string]bool{}
		for _, t := range strings.Split(s, ",") {
			topics[strings.TrimSpace(t)] = true
		}
	}

	ch := make(chan sseEvent, sseQueue)
	var lagged atomic.Bool
	push := func(ev sseEvent) {
		select {
		case ch <- ev:
		default:
			lagged.Store(true)
		}
	}
	if topics["nodes"] && a.deps.Registry != nil {
		unsubscribe := a.deps.Registry.Subscribe(func(info registry.Info) { push(sseEvent{"node", info}) })
		defer unsubscribe()
	}
	if topics["monitor"] && a.deps.Monitor != nil {
		unsubscribe := a.deps.Monitor.Subscribe(func(e monitor.Entry) { push(sseEvent{"frame", e}) })
		defer unsubscribe()
	}
	if topics["entities"] && a.deps.Entities != nil {
		unsubscribe := a.deps.Entities.Subscribe(func(e entity.Entity) { push(sseEvent{"entity", e}) })
		defer unsubscribe()
	}
	if topics["automations"] && a.deps.Automations != nil {
		unsubscribe := a.deps.Automations.Subscribe(func(s automation.Status) { push(sseEvent{"automation", s}) })
		defer unsubscribe()
		unsubscribeTraces := a.deps.Automations.SubscribeTraces(func(tr automation.Trace) { push(sseEvent{"trace", tr}) })
		defer unsubscribeTraces()
	}
	if topics["trains"] && a.deps.Trains != nil {
		unsubscribe := a.deps.Trains.Subscribe(func(tr train.Train) { push(sseEvent{"train", tr}) })
		defer unsubscribe()
		unsubscribeLayout := a.deps.Trains.SubscribeLayout(func(l train.Layout) { push(sseEvent{"layout", l}) })
		defer unsubscribeLayout()
	}
	if topics["trains"] && a.deps.Programmer != nil {
		unsubscribe := a.deps.Programmer.Subscribe(func(op programmer.Op) { push(sseEvent{"cv", op}) })
		defer unsubscribe()
	}
	if topics["entities"] && a.deps.EventNames != nil {
		unsubscribe := a.deps.EventNames.Subscribe(func(id openlcb.NodeID) {
			push(sseEvent{"event_names", map[string]openlcb.NodeID{"node": id}})
		})
		defer unsubscribe()
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var seq uint64
	if a.deps.Monitor != nil {
		seq = a.deps.Monitor.LastSeq()
	}
	if err := writeSSE(w, "ready", map[string]uint64{"seq": seq}); err != nil {
		return
	}
	if !flush() {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()
	var linkTick <-chan time.Time
	var lastLinks []byte
	if topics["links"] && a.deps.Bus != nil {
		ticker := time.NewTicker(ssePollLinks)
		defer ticker.Stop()
		linkTick = ticker.C
		lastLinks, _ = json.Marshal(a.deps.Bus.Links())
		if err := writeSSE(w, "links", map[string]any{"links": a.deps.Bus.Links()}); err != nil {
			return
		}
		if !flush() {
			return
		}
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			if lagged.Swap(false) {
				if err := writeSSE(w, "lagged", struct{}{}); err != nil {
					return
				}
			}
			if err := writeSSE(w, ev.name, ev.data); err != nil {
				return
			}
			// Drain whatever else is ready before flushing.
			for drained := 0; drained < 64; drained++ {
				select {
				case more := <-ch:
					if err := writeSSE(w, more.name, more.data); err != nil {
						return
					}
					continue
				default:
				}
				break
			}
			if !flush() {
				return
			}
		case <-linkTick:
			links := a.deps.Bus.Links()
			current, _ := json.Marshal(links)
			if !bytes.Equal(current, lastLinks) {
				lastLinks = current
				if err := writeSSE(w, "links", map[string]any{"links": links}); err != nil {
					return
				}
				if !flush() {
					return
				}
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if !flush() {
				return
			}
		}
	}
}

func writeSSE(w http.ResponseWriter, name string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, body)
	return err
}
