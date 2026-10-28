package layoutsim

import "github.com/mikesmitty/interlock/internal/trackplan"

// span is the part of a leg a train covers, From to To millimeters along
// it.
type span struct {
	Leg  trackplan.Leg `json:"leg"`
	From float64       `json:"from"`
	To   float64       `json:"to"`
}

// path is where a train is: the spans it covers from its B end to its A
// end, each leg taken the way the A end leads. Moving its ends along the
// path, rather than placing the train afresh, keeps a train on the way it
// came even when a junction behind its front is thrown.
type path []span

// length is how much track the path covers.
func (p path) length() float64 {
	total := 0.0
	for _, s := range p {
		total += s.To - s.From
	}
	return total
}

// grow carries the A end on by up to d, leaving the B end where it is,
// and returns how far it went and, if short, what stopped it.
func (p path) grow(n *trackplan.Network, d float64, position func(string) trackplan.Position) (path, float64, error) {
	p = append(path(nil), p...)
	moved := 0.0
	for {
		last := &p[len(p)-1]
		step := min(n.Length(last.Leg)-last.To, d-moved)
		last.To += step
		moved += step
		if d-moved <= 1e-9 {
			return p, d, nil
		}
		next, err := n.Next(last.Leg, position)
		if err != nil {
			return p, moved, err
		}
		p = append(p, span{Leg: next})
	}
}

// trim takes d off the B end.
func (p path) trim(d float64) path {
	for d > 0 && len(p) > 0 {
		first := &p[0]
		have := first.To - first.From
		if have > d || len(p) == 1 {
			first.From = min(first.From+d, first.To)
			break
		}
		d -= have
		p = p[1:]
	}
	return p
}

// advance moves the whole train up to d toward its A end and returns how
// far it went and, if short, what stopped it.
func (p path) advance(n *trackplan.Network, d float64, position func(string) trackplan.Position) (path, float64, error) {
	grown, moved, err := p.grow(n, d, position)
	return grown.trim(moved), moved, err
}

// reversed is the same path with its ends swapped.
func (p path) reversed(n *trackplan.Network) path {
	out := make(path, len(p))
	for i, s := range p {
		l := n.Length(s.Leg)
		out[len(p)-1-i] = span{Leg: s.Leg.Reversed(), From: l - s.To, To: l - s.From}
	}
	return out
}

// back is the spot d behind the A end: its leg and how far along it.
func (p path) back(d float64) (trackplan.Leg, float64) {
	for i := len(p) - 1; i >= 0; i-- {
		s := p[i]
		have := s.To - s.From
		if d <= have || i == 0 {
			return s.Leg, max(s.To-d, s.From)
		}
		d -= have
	}
	return trackplan.Leg{}, 0
}

// valid reports whether every leg is still in the network, as long as it
// was.
func (p path) valid(n *trackplan.Network) bool {
	if len(p) == 0 {
		return false
	}
	for _, s := range p {
		if !n.Valid(s.Leg) || s.To > n.Length(s.Leg)+0.5 {
			return false
		}
	}
	return true
}

// overlaps reports whether two paths share track: the same stretch of a
// track or junction route, or any part of one junction on two routes,
// which can't both be used at once.
func (p path) overlaps(n *trackplan.Network, o path) bool {
	const touch = 0.5
	for _, a := range p {
		if a.To-a.From < touch {
			continue
		}
		for _, b := range o {
			if b.To-b.From < touch || a.Leg.Piece != b.Leg.Piece || a.Leg.Junction != b.Leg.Junction {
				continue
			}
			if a.Leg.Route != b.Leg.Route {
				return true
			}
			alo, ahi := forward(n, a)
			blo, bhi := forward(n, b)
			if alo < bhi-touch && blo < ahi-touch {
				return true
			}
		}
	}
	return false
}

// forward is a span's stretch measured from its route's first end.
func forward(n *trackplan.Network, s span) (float64, float64) {
	if !s.Leg.Reverse {
		return s.From, s.To
	}
	l := n.Length(s.Leg)
	return l - s.To, l - s.From
}
