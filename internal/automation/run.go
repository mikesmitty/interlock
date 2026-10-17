package automation

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// maxIterations stops a repeat that never ends.
const maxIterations = 1000

// run is one execution of an automation.
type run struct {
	e              *Engine
	doc            Automation
	skipConditions bool
	ctx            context.Context
	cancel         context.CancelFunc

	mu     sync.Mutex
	trace  Trace
	reason string // why the run was canceled
}

func newRun(e *Engine, doc Automation, info TriggerInfo, skipConditions bool) *run {
	ctx, cancel := context.WithCancel(context.Background())
	return &run{
		e: e, doc: doc, skipConditions: skipConditions, ctx: ctx, cancel: cancel,
		trace: Trace{ID: newTraceID(), Automation: doc.ID, Name: doc.Name, Started: time.Now(), Result: ResultRunning, Trigger: info, Steps: []Step{}},
	}
}

// snapshot copies the trace as it stands.
func (r *run) snapshot() Trace {
	r.mu.Lock()
	defer r.mu.Unlock()
	tr := r.trace
	tr.Steps = make([]Step, len(r.trace.Steps))
	copy(tr.Steps, r.trace.Steps)
	return tr
}

// cancelWith stops the run; why ends up in the trace.
func (r *run) cancelWith(why string) {
	r.mu.Lock()
	if r.reason == "" {
		r.reason = why
	}
	r.mu.Unlock()
	r.cancel()
}

// flow says how a list of actions ended.
type flow struct {
	kind   flowKind
	reason string
	err    error
}

type flowKind int

const (
	flowContinue flowKind = iota
	flowStop
	flowFailed
	flowCanceled
)

var proceed = flow{}

func (r *run) execute() {
	defer r.cancel()
	n := r.e.names()
	if !r.skipConditions {
		for i, c := range r.doc.Conditions {
			ok, err := r.condition(c, "conditions["+strconv.Itoa(i)+"]", n)
			if err != nil {
				r.end(ResultError, err.Error())
				return
			}
			if !ok {
				r.end(ResultNotRun, "")
				return
			}
		}
	}
	f := r.actions(r.doc.Actions, "actions", n)
	switch f.kind {
	case flowStop:
		r.end(ResultStopped, f.reason)
	case flowFailed:
		msg := f.reason
		if f.err != nil {
			msg = f.err.Error()
		}
		r.end(ResultError, msg)
	case flowCanceled:
		r.mu.Lock()
		why := r.reason
		r.mu.Unlock()
		if why == "" {
			why = "Canceled"
		}
		r.end(ResultCanceled, why)
	default:
		r.end(ResultFinished, "")
	}
}

func (r *run) end(result, msg string) {
	r.mu.Lock()
	r.trace.Result = result
	r.trace.Error = msg
	r.trace.Finished = time.Now()
	r.mu.Unlock()
	level := r.e.log.Info
	if result == ResultError {
		level = r.e.log.Warn
	}
	level("automation run ended", "automation", r.doc.ID, "trace", r.trace.ID, "result", result, "error", msg)
}

// --- steps ----------------------------------------------------------------

// begin adds a running step and returns its index.
func (r *run) begin(path, typ, description string) int {
	r.mu.Lock()
	r.trace.Steps = append(r.trace.Steps, Step{Path: path, Type: typ, Description: description, Started: time.Now(), Result: StepRunning})
	idx := len(r.trace.Steps) - 1
	tr := r.snapshotLocked()
	r.mu.Unlock()
	r.e.publishTrace(tr)
	return idx
}

// finish closes a step.
func (r *run) finish(idx int, result, detail string, err error) {
	r.mu.Lock()
	s := &r.trace.Steps[idx]
	s.Result = result
	s.Detail = detail
	s.Finished = time.Now()
	if err != nil {
		s.Error = err.Error()
	}
	tr := r.snapshotLocked()
	r.mu.Unlock()
	r.e.publishTrace(tr)
}

func (r *run) snapshotLocked() Trace {
	tr := r.trace
	tr.Steps = make([]Step, len(r.trace.Steps))
	copy(tr.Steps, r.trace.Steps)
	return tr
}

// record adds a step that is already over.
func (r *run) record(path, typ, description, result, detail string, err error) {
	idx := r.begin(path, typ, description)
	r.finish(idx, result, detail, err)
}

// --- conditions -----------------------------------------------------------

// condition evaluates one condition, recording it and anything nested.
func (r *run) condition(c Condition, path string, n names) (bool, error) {
	idx := r.begin(path, "condition:"+c.Type, describeCondition(c, n))
	ok, detail, err := r.evalCondition(c, path, n)
	switch {
	case err != nil:
		r.finish(idx, StepError, detail, err)
	case ok:
		r.finish(idx, StepOK, detail, nil)
	default:
		r.finish(idx, StepFalse, detail, nil)
	}
	return ok, err
}

func (r *run) evalCondition(c Condition, path string, n names) (ok bool, detail string, err error) {
	switch c.Type {
	case ConditionState:
		ent, found := r.e.opts.Entities.Get(c.Entity)
		if !found {
			return false, "", fmt.Errorf("there is no entity %s", c.Entity)
		}
		if ent.State == c.State {
			return true, "was " + ent.State, nil
		}
		return false, "was " + ent.State, nil
	case ConditionTime:
		now := time.Now()
		if inWindow(now, c.After, c.Before, c.Weekdays) {
			return true, "it was " + now.Format("15:04:05 Mon"), nil
		}
		return false, "it was " + now.Format("15:04:05 Mon"), nil
	case ConditionClock:
		if r.e.opts.Clock == nil {
			return false, "", errors.New("there is no fast clock")
		}
		fast := r.e.opts.Clock.Now()
		return inWindow(fast, c.After, c.Before, c.Weekdays), "the fast clock read " + fast.Format("15:04 Mon"), nil
	case ConditionTrigger:
		r.mu.Lock()
		id := r.trace.Trigger.ID
		r.mu.Unlock()
		if id == c.Trigger {
			return true, "", nil
		}
		return false, "the trigger was " + firstNonEmpty(id, strconv.Itoa(r.trace.Trigger.Index+1)), nil
	case ConditionAnd:
		for i, sub := range c.Conditions {
			ok, err := r.condition(sub, path+".conditions["+strconv.Itoa(i)+"]", n)
			if err != nil || !ok {
				return false, "", err
			}
		}
		return true, "", nil
	case ConditionOr:
		for i, sub := range c.Conditions {
			ok, err := r.condition(sub, path+".conditions["+strconv.Itoa(i)+"]", n)
			if err != nil {
				return false, "", err
			}
			if ok {
				return true, "", nil
			}
		}
		return false, "", nil
	case ConditionNot:
		ok, err := r.condition(c.Conditions[0], path+".conditions[0]", n)
		return !ok && err == nil, "", err
	}
	return false, "", fmt.Errorf("unknown condition %q", c.Type)
}

// conditions evaluates a list, all of which must hold.
func (r *run) conditions(conds []Condition, path string, n names) (bool, error) {
	for i, c := range conds {
		ok, err := r.condition(c, path+"["+strconv.Itoa(i)+"]", n)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- actions ---------------------------------------------------------------

func (r *run) actions(actions []Action, path string, n names) flow {
	for i, a := range actions {
		if r.ctx.Err() != nil {
			return flow{kind: flowCanceled}
		}
		if f := r.action(a, path+"["+strconv.Itoa(i)+"]", n); f.kind != flowContinue {
			return f
		}
	}
	return proceed
}

func (r *run) action(a Action, path string, n names) flow {
	desc := describeAction(a, n)
	if a.Disabled {
		r.record(path, a.Type, desc, StepSkipped, "turned off", nil)
		return proceed
	}
	idx := r.begin(path, a.Type, desc)
	switch a.Type {
	case ActionCommand:
		if _, err := r.e.opts.Entities.Command(r.ctx, a.Entity, a.Command); err != nil {
			return r.fail(idx, err)
		}
		r.finish(idx, StepOK, "", nil)
	case ActionTrain:
		if r.e.opts.Trains == nil {
			return r.fail(idx, errors.New("trains are not available"))
		}
		tr, err := r.e.opts.Trains.Command(r.ctx, a.trainCommand())
		if err != nil {
			return r.fail(idx, err)
		}
		detail := fmt.Sprintf("speed %s %s", strconv.FormatFloat(float64(tr.Speed), 'f', -1, 32), tr.Direction)
		if tr.EStop {
			detail = "emergency stopped"
		}
		r.finish(idx, StepOK, detail, nil)
	case ActionEvent:
		if err := r.e.opts.Bus.SendEvent(r.ctx, a.Event); err != nil {
			return r.fail(idx, err)
		}
		r.finish(idx, StepOK, "", nil)
	case ActionDelay:
		timer := time.NewTimer(a.For.Duration())
		defer timer.Stop()
		select {
		case <-r.ctx.Done():
			r.finish(idx, StepCanceled, "", nil)
			return flow{kind: flowCanceled}
		case <-timer.C:
			r.finish(idx, StepOK, "waited "+a.For.String(), nil)
		}
	case ActionWait:
		return r.wait(a, idx, n)
	case ActionIf:
		ok, err := r.conditions(a.Conditions, path+".conditions", n)
		if err != nil {
			return r.fail(idx, err)
		}
		branch, name := a.Else, "else"
		if ok {
			branch, name = a.Then, "then"
		}
		if len(branch) == 0 {
			r.finish(idx, StepOK, "nothing to do for "+name, nil)
			return proceed
		}
		r.finish(idx, StepOK, "took "+name, nil)
		return r.actions(branch, path+"."+name, n)
	case ActionChoose:
		for i, ch := range a.Choices {
			cp := path + ".choices[" + strconv.Itoa(i) + "]"
			ok, err := r.conditions(ch.Conditions, cp+".conditions", n)
			if err != nil {
				return r.fail(idx, err)
			}
			if ok {
				r.finish(idx, StepOK, "chose "+firstNonEmpty(ch.Name, "option "+strconv.Itoa(i+1)), nil)
				return r.actions(ch.Actions, cp+".actions", n)
			}
		}
		if len(a.Default) == 0 {
			r.finish(idx, StepOK, "nothing matched", nil)
			return proceed
		}
		r.finish(idx, StepOK, "nothing matched; took the default", nil)
		return r.actions(a.Default, path+".default", n)
	case ActionRepeat:
		return r.repeat(a, path, idx, n)
	case ActionStop:
		if a.Error {
			r.finish(idx, StepError, a.Reason, nil)
			return flow{kind: flowFailed, reason: firstNonEmpty(a.Reason, "Stopped with an error")}
		}
		r.finish(idx, StepOK, a.Reason, nil)
		return flow{kind: flowStop, reason: a.Reason}
	case ActionLog:
		r.e.log.Info("automation log", "automation", r.doc.ID, "trace", r.trace.ID, "message", a.Message)
		r.finish(idx, StepOK, a.Message, nil)
	default:
		return r.fail(idx, fmt.Errorf("unknown action %q", a.Type))
	}
	return proceed
}

func (r *run) fail(idx int, err error) flow {
	if errors.Is(err, context.Canceled) || r.ctx.Err() != nil {
		r.finish(idx, StepCanceled, "", nil)
		return flow{kind: flowCanceled}
	}
	r.finish(idx, StepError, "", err)
	return flow{kind: flowFailed, err: err}
}

func (r *run) wait(a Action, idx int, _ names) flow {
	id, w := r.e.addWaiter(a.Triggers)
	defer r.e.removeWaiter(id)
	var timeout <-chan time.Time
	if a.Timeout > 0 {
		timer := time.NewTimer(a.Timeout.Duration())
		defer timer.Stop()
		timeout = timer.C
	}
	started := time.Now()
	select {
	case <-r.ctx.Done():
		r.finish(idx, StepCanceled, "", nil)
		return flow{kind: flowCanceled}
	case info := <-w.ch:
		r.finish(idx, StepOK, info.Description+" after "+Duration(time.Since(started).Round(time.Millisecond)).String(), nil)
		return proceed
	case <-timeout:
		if a.StopOnTimeout {
			r.finish(idx, StepTimeout, "nothing happened in "+a.Timeout.String()+"; stopping", nil)
			return flow{kind: flowStop, reason: "The wait timed out"}
		}
		r.finish(idx, StepTimeout, "nothing happened in "+a.Timeout.String()+"; going on", nil)
		return proceed
	}
}

func (r *run) repeat(a Action, path string, idx int, n names) flow {
	ran := 0
	for i := 0; ; i++ {
		if r.ctx.Err() != nil {
			r.finish(idx, StepCanceled, "", nil)
			return flow{kind: flowCanceled}
		}
		if a.Count > 0 && i >= a.Count {
			break
		}
		if i >= maxIterations {
			return r.fail(idx, fmt.Errorf("repeat ran %d times without ending", maxIterations))
		}
		ip := path + ".repeat[" + strconv.Itoa(i) + "]"
		if len(a.While) > 0 {
			ok, err := r.conditions(a.While, ip+".while", n)
			if err != nil {
				return r.fail(idx, err)
			}
			if !ok {
				break
			}
		}
		label := "Iteration " + strconv.Itoa(i+1)
		if a.Count > 0 {
			label += " of " + strconv.Itoa(a.Count)
		}
		r.record(ip, "iteration", label, StepOK, "", nil)
		if f := r.actions(a.Actions, ip+".actions", n); f.kind != flowContinue {
			r.finish(idx, stepFor(f), "", f.err)
			return f
		}
		ran++
		if len(a.Until) > 0 {
			ok, err := r.conditions(a.Until, ip+".until", n)
			if err != nil {
				return r.fail(idx, err)
			}
			if ok {
				break
			}
		}
	}
	r.finish(idx, StepOK, "ran "+countOf(ran, "time"), nil)
	return proceed
}

// stepFor is the step result matching how a nested list ended.
func stepFor(f flow) string {
	switch f.kind {
	case flowCanceled:
		return StepCanceled
	case flowFailed:
		return StepError
	default:
		return StepOK
	}
}
