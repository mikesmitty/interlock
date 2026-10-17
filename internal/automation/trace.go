package automation

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
)

// Trace results.
const (
	ResultRunning  = "running"
	ResultFinished = "finished"
	// ResultNotRun: the conditions did not hold.
	ResultNotRun   = "not_run"
	ResultStopped  = "stopped"
	ResultError    = "error"
	ResultCanceled = "canceled"
	// ResultSkipped: the automation was already running (single mode) or
	// at its limit.
	ResultSkipped = "skipped"
)

// Step results.
const (
	StepRunning  = "running"
	StepOK       = "ok"
	StepFalse    = "false" // a condition that did not hold
	StepSkipped  = "skipped"
	StepError    = "error"
	StepTimeout  = "timeout"
	StepCanceled = "canceled"
)

// Trace records one run of an automation: what started it, how each
// condition came out and what every action did, with timing.
type Trace struct {
	ID         string `json:"id"`
	Automation string `json:"automation"`
	// Name is the automation's name when it ran.
	Name     string    `json:"name"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	Result   string    `json:"result"`
	// Error says why the run failed or was stopped.
	Error   string      `json:"error,omitempty"`
	Trigger TriggerInfo `json:"trigger"`
	Steps   []Step      `json:"steps"`
}

// TriggerInfo says what started a run.
type TriggerInfo struct {
	// Index is the trigger's position in the automation, or -1 for a run
	// by hand.
	Index int    `json:"index"`
	ID    string `json:"id,omitempty"`
	Type  string `json:"type"`
	// Description reads like "Track 2 became occupied".
	Description string          `json:"description"`
	Entity      string          `json:"entity,omitempty"`
	EntityName  string          `json:"entity_name,omitempty"`
	From        string          `json:"from,omitempty"`
	To          string          `json:"to,omitempty"`
	Event       openlcb.EventID `json:"event,omitempty"`
	Locomotive  string          `json:"locomotive,omitempty"`
	Node        openlcb.NodeID  `json:"node,omitempty"`
	NodeName    string          `json:"node_name,omitempty"`
	At          time.Time       `json:"at"`
}

// Step is one condition or action in a trace.
type Step struct {
	// Path locates the step in the document: "conditions[0]",
	// "actions[1].then[0]", "actions[2].repeat[1].actions[0]".
	Path string `json:"path"`
	Type string `json:"type"`
	// Description reads like "Close Turnout 1" or "Turnout 1 is thrown".
	Description string    `json:"description"`
	Started     time.Time `json:"started"`
	Finished    time.Time `json:"finished,omitzero"`
	Result      string    `json:"result"`
	// Detail says what happened: "was closed", "waited 3s", "timed out".
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Done reports whether the run has ended.
func (t Trace) Done() bool { return t.Result != ResultRunning }

func newTraceID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b[:])
}
