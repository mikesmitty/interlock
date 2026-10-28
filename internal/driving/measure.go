package driving

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mikesmitty/interlock/internal/interlocking"
	"github.com/mikesmitty/interlock/internal/tracking"
	"github.com/mikesmitty/interlock/internal/trackplan"
	"github.com/mikesmitty/interlock/internal/train"
)

// How a speed measuring run goes.
const (
	// runsPerStep is how many runs from one block into the next tracking
	// measures at each step.
	runsPerStep = 2
	// stepTimeout is how long a step may take before it is given up.
	stepTimeout = 5 * time.Minute
	// lookahead is how much more than its braking distance the way set
	// ahead of the train is kept, in millimeters.
	lookahead = 300.0
	// blockedFor is how long the train may stand waiting for the way on
	// before the run ends.
	blockedFor = time.Minute
)

// Measure begins a run to measure a train's speeds: it runs the train on
// around the layout as the turnouts lie, setting the route ahead of it
// as it goes, at each of the steps asked in turn, slowest first, until
// tracking has measured each over two runs from one block into the next.
// Then it stops the train at the end of the way set. The run goes on
// after it returns; a *RefusedError says why it can't begin.
func (d *Driver) Measure(ctx context.Context, id string, req MeasureRequest) (Drive, error) {
	steps := slices.Clone(req.Steps)
	if len(steps) == 0 {
		steps = slices.Clone(DefaultSteps)
	}
	slices.Sort(steps)
	steps = slices.Compact(steps)
	if steps[0] < 1 || steps[len(steps)-1] > train.MaxSpeed {
		return Drive{}, refuse("The steps must be from 1 to %d.", train.MaxSpeed)
	}
	s, err := d.prepare(ctx, id)
	if err != nil {
		return Drive{}, err
	}
	ways, err := directions(s.t, req.Direction)
	if err != nil {
		return Drive{}, err
	}
	reverse := ways[0]
	lead, at := leading(s.net, s.t.Stretch, reverse)
	if s.net.Block(lead) == "" {
		return Drive{}, refuse("%s's leading end is between blocks. Drive it into a block first.", s.t.Name)
	}
	c := s.p.build(at, s.p.walk(lead), nil)
	c.stop = c.end - endMargin
	r := d.newRun(s, reverse)
	r.view.Kind, r.view.Speed, r.view.Braking = KindMeasure, steps[0], DefaultBraking
	r.view.Measuring = &Measuring{Steps: steps, At: steps[0], Done: []int{}}
	owned := map[string]bool{}
	m := &measurer{steps: steps, began: d.opts.Now(), runs: runsAt(s.t.Speeds, steps[0]), owned: owned, plan: s.plan, names: s.names}
	return d.start(r, c, owned, brakes{cruise: steps[0], braking: DefaultBraking}, m, s.net, s.p), nil
}

// measurer is a speed measuring run's progress.
type measurer struct {
	steps []int
	// i is the step being measured, since began, and runs how many runs
	// tracking had measured at it then.
	i     int
	began time.Time
	runs  int
	// finished is set once every step is measured or given up.
	finished bool
	// owned are the routes the run has set; pending is one set but not
	// yet on the course, waiting for its turnouts, entering its block at
	// entry.
	owned   map[string]bool
	pending string
	entry   trackplan.Leg
	tried   time.Time
	blocked string
	// standing is since when the train has stood waiting for the way on.
	standing time.Time
	plan     trackplan.Plan
	names    map[string]string
}

func runsAt(speeds []tracking.SpeedPoint, step int) int {
	if i := slices.IndexFunc(speeds, func(p tracking.SpeedPoint) bool { return p.Step == step }); i >= 0 {
		return speeds[i].Runs
	}
	return 0
}

// tick moves the run on: to the next step once this one is measured,
// and the way set ahead of the train as it goes. It returns why the run
// can't go on, or "".
func (m *measurer) tick(ctx context.Context, d *Driver, r *run, c *course, b *brakes, t tracking.Train, pos float64, p *planner, net *trackplan.Network) string {
	now := d.opts.Now()
	if !m.finished {
		step := m.steps[m.i]
		measured := runsAt(t.Speeds, step) >= m.runs+runsPerStep
		if measured || now.Sub(m.began) > stepTimeout {
			d.update(r, true, func(v *Drive) {
				if measured {
					v.Measuring.Done = append(v.Measuring.Done, step)
				} else {
					v.Measuring.Skipped = append(v.Measuring.Skipped, step)
				}
			})
			m.i++
			if m.i == len(m.steps) {
				m.finish(ctx, d)
				return ""
			}
			m.began, m.runs = now, runsAt(t.Speeds, m.steps[m.i])
			b.cruise = m.steps[m.i]
			d.update(r, true, func(v *Drive) { v.Speed, v.Measuring.At = b.cruise, b.cruise })
		}
	}
	if m.finished {
		return ""
	}
	if m.pending != "" {
		rt, ok := d.opts.Interlocking.Get(m.pending)
		switch {
		case !ok || rt.State == "":
			m.pending = ""
		case rt.State != interlocking.StateSetting:
			c.follow(p, rt, m.entry)
			c.stop = c.end - endMargin
			m.pending = ""
			d.update(r, false, func(v *Drive) { v.Routes = append(v.Routes, rt.ID) })
		}
	}
	if m.pending == "" && c.end-endMargin-pos < b.stoppingDistance(t.Motion, float64(b.cruise))+lookahead && now.Sub(m.tried) >= time.Second {
		m.tried = now
		m.blocked = m.extend(ctx, d, r, c, p, net)
	}
	if t.Step == 0 && c.stop-pos < endMargin {
		if m.standing.IsZero() {
			m.standing = now
		} else if now.Sub(m.standing) > blockedFor {
			if m.blocked != "" {
				return "The way on stayed blocked: " + m.blocked
			}
			return "The way on stayed blocked."
		}
	} else {
		m.standing = time.Time{}
	}
	return ""
}

// finish ends the measuring: the train runs on to the end of the way set
// and stops there.
func (m *measurer) finish(ctx context.Context, d *Driver) {
	m.finished = true
	if m.pending != "" && m.owned[m.pending] {
		_, _ = d.opts.Interlocking.Cancel(ctx, m.pending, false)
	}
	m.pending = ""
}

// extend sets the route on from the end of the course as the turnouts
// lie. It returns why it couldn't, or "".
func (m *measurer) extend(ctx context.Context, d *Driver, r *run, c *course, p *planner, net *trackplan.Network) string {
	p.position = d.positions(m.plan)
	last := c.legs[len(c.legs)-1]
	block := net.Block(last)
	for _, rt := range d.opts.Interlocking.List() {
		if rt.From.Block != block {
			continue
		}
		entry, ok := p.enters(last, rt)
		moved := slices.ContainsFunc(rt.Turnouts, func(s trackplan.Setting) bool { return p.position(s.Junction) != s.Position })
		if !ok || moved {
			continue // not the way the turnouts lie
		}
		if rt.State != "" {
			if rt.Train != "" && rt.Train != r.view.Train {
				return fmt.Sprintf("the route from %s to %s has a train on it", m.names[rt.From.Block], m.names[rt.To.Block])
			}
		} else if _, err := d.opts.Interlocking.Set(ctx, rt.ID); err != nil {
			var refused *interlocking.RefusedError
			if errors.As(err, &refused) {
				return fmt.Sprintf("the route from %s to %s can't be set: %s", m.names[rt.From.Block], m.names[rt.To.Block], refused.Reason)
			}
			return err.Error()
		} else {
			m.owned[rt.ID] = true
		}
		m.pending, m.entry = rt.ID, entry
		return ""
	}
	return fmt.Sprintf("no route leads on from %s as the turnouts lie", m.names[block])
}
