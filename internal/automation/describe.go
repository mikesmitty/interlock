package automation

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/traction"
)

// names turns IDs into the words a person uses for them.
type names struct {
	entity func(id string) string
	node   func(id openlcb.NodeID) string
	train  func(address uint16, long bool) string
}

func (n names) loco(address uint16, kind string) string {
	long := kind == "long" || address > traction.ShortAddressMax
	if n.train != nil {
		if s := n.train(address, long); s != "" {
			return s
		}
	}
	return "locomotive " + traction.AddressName(address, long)
}

func (n names) ent(id string) string {
	if n.entity != nil {
		if s := n.entity(id); s != "" {
			return s
		}
	}
	return id
}

func (n names) nod(id openlcb.NodeID) string {
	if n.node != nil {
		if s := n.node(id); s != "" {
			return s
		}
	}
	return id.String()
}

// describeTrigger reads like "When Track 2 becomes occupied".
func describeTrigger(t Trigger, n names) string {
	switch t.Type {
	case TriggerState:
		s := "When " + n.ent(t.Entity)
		switch {
		case t.From != "" && t.To != "":
			s += " changes from " + t.From + " to " + t.To
		case t.To != "":
			s += " becomes " + t.To
		case t.From != "":
			s += " leaves " + t.From
		default:
			s += " changes state"
		}
		if t.For > 0 {
			s += " for " + t.For.String()
		}
		return s
	case TriggerEvent:
		return "When event " + t.Event.String() + " is received"
	case TriggerLocomotive:
		who := "a locomotive"
		if t.Address != 0 {
			who = "locomotive " + locomotiveName(t.Address, t.AddressType)
		}
		switch t.Direction {
		case "entered":
			return "When " + who + " enters " + n.ent(t.Entity)
		case "left":
			return "When " + who + " leaves " + n.ent(t.Entity)
		default:
			return "When " + who + " enters or leaves " + n.ent(t.Entity)
		}
	case TriggerNode:
		who := "any node"
		if t.Node != 0 {
			who = n.nod(t.Node)
		}
		switch t.To {
		case "online":
			return "When " + who + " comes online"
		case "offline":
			return "When " + who + " goes offline"
		default:
			return "When " + who + " comes online or goes offline"
		}
	case TriggerTime:
		return "At " + t.At + onDays(t.Weekdays)
	case TriggerInterval:
		return "Every " + t.Every.String()
	case TriggerStart:
		return "When Interlock starts"
	}
	return t.Type
}

// locomotiveName reads like "1234", "3 (short)" or "consist 12".
func locomotiveName(address uint16, kind string) string {
	s := strconv.Itoa(int(address))
	switch kind {
	case "short":
		return s + " (short)"
	case "consist":
		return "consist " + s
	default:
		return s
	}
}

func onDays(days []string) string {
	if len(days) == 0 {
		return ""
	}
	titled := make([]string, len(days))
	for i, d := range days {
		d = strings.ToLower(d)
		if d != "" {
			d = strings.ToUpper(d[:1]) + d[1:]
		}
		titled[i] = d
	}
	return " on " + strings.Join(titled, ", ")
}

// describeCondition reads like "Turnout 1 is thrown".
func describeCondition(c Condition, n names) string {
	switch c.Type {
	case ConditionState:
		return n.ent(c.Entity) + " is " + c.State
	case ConditionTime:
		var s string
		switch {
		case c.After != "" && c.Before != "":
			s = "Between " + c.After + " and " + c.Before
		case c.After != "":
			s = "After " + c.After
		case c.Before != "":
			s = "Before " + c.Before
		default:
			s = "Only"
		}
		return s + onDays(c.Weekdays)
	case ConditionTrigger:
		return "Started by trigger " + c.Trigger
	case ConditionAnd:
		return "All of " + countOf(len(c.Conditions), "condition")
	case ConditionOr:
		return "Any of " + countOf(len(c.Conditions), "condition")
	case ConditionNot:
		if len(c.Conditions) == 1 {
			return "Not: " + describeCondition(c.Conditions[0], n)
		}
		return "Not"
	}
	return c.Type
}

// describeAction reads like "Close Turnout 1".
func describeAction(a Action, n names) string {
	switch a.Type {
	case ActionCommand:
		cmd := a.Command
		if cmd != "" {
			cmd = strings.ToUpper(cmd[:1]) + cmd[1:]
		}
		return cmd + " " + n.ent(a.Entity)
	case ActionTrain:
		return a.trainCommand().Describe(n.loco(a.Address, a.AddressType))
	case ActionEvent:
		return "Send event " + a.Event.String()
	case ActionDelay:
		return "Wait " + a.For.String()
	case ActionWait:
		parts := make([]string, len(a.Triggers))
		for i, t := range a.Triggers {
			parts[i] = strings.TrimPrefix(describeTrigger(t, n), "When ")
		}
		s := "Wait until " + strings.Join(parts, ", or ")
		if a.Timeout > 0 {
			s += " (up to " + a.Timeout.String() + ")"
		}
		return s
	case ActionIf:
		parts := make([]string, len(a.Conditions))
		for i, c := range a.Conditions {
			parts[i] = describeCondition(c, n)
		}
		return "If " + strings.Join(parts, " and ")
	case ActionChoose:
		return "Choose between " + countOf(len(a.Choices), "option")
	case ActionRepeat:
		switch {
		case a.Count > 0:
			return "Repeat " + countOf(a.Count, "time")
		case len(a.While) > 0:
			return "Repeat while " + describeCondition(a.While[0], n)
		default:
			return "Repeat until " + describeCondition(a.Until[0], n)
		}
	case ActionStop:
		if a.Reason != "" {
			return "Stop: " + a.Reason
		}
		return "Stop"
	case ActionLog:
		return "Log " + strconv.Quote(a.Message)
	}
	return a.Type
}

func countOf(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %ss", n, one)
}
