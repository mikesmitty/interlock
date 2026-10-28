package trackplan

import "slices"

// Span is the part of a leg a train covers, From to To millimeters along
// it.
type Span struct {
	Leg  Leg     `json:"leg"`
	From float64 `json:"from"`
	To   float64 `json:"to"`
}

// Stretch is where a train is on the network: the spans it covers from one
// end to the other, each leg taken the way toward the last span. Moving
// its ends along the track, rather than placing the train afresh, keeps a
// train on the way it came even when a junction behind its leading end
// is thrown.
type Stretch []Span

// Length is how much track the stretch covers.
func (p Stretch) Length() float64 {
	total := 0.0
	for _, s := range p {
		total += s.To - s.From
	}
	return total
}

// Grow carries the last end on by up to d, leaving the first where it
// is, and returns how far it went and, if short, what stopped it.
func (p Stretch) Grow(n *Network, d float64, position func(string) Position) (Stretch, float64, error) {
	p = append(Stretch(nil), p...)
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
		p = append(p, Span{Leg: next})
	}
}

// Trim takes d off the first end.
func (p Stretch) Trim(d float64) Stretch {
	p = append(Stretch(nil), p...)
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

// Advance moves the whole stretch up to d toward its last end and returns
// how far it went and, if short, what stopped it.
func (p Stretch) Advance(n *Network, d float64, position func(string) Position) (Stretch, float64, error) {
	grown, moved, err := p.Grow(n, d, position)
	return grown.Trim(moved), moved, err
}

// Reversed is the same stretch with its ends swapped.
func (p Stretch) Reversed(n *Network) Stretch {
	out := make(Stretch, len(p))
	for i, s := range p {
		l := n.Length(s.Leg)
		out[len(p)-1-i] = Span{Leg: s.Leg.Reversed(), From: l - s.To, To: l - s.From}
	}
	return out
}

// Back is the spot d behind the last end: its leg and how far along it.
func (p Stretch) Back(d float64) (Leg, float64) {
	for i := len(p) - 1; i >= 0; i-- {
		s := p[i]
		have := s.To - s.From
		if d <= have || i == 0 {
			return s.Leg, max(s.To-d, s.From)
		}
		d -= have
	}
	return Leg{}, 0
}

// Valid reports whether every leg is still in the network, as long as it
// was.
func (p Stretch) Valid(n *Network) bool {
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

// Blocks lists the blocks the stretch runs through, from its last end to
// its first, each once; track outside any block is left out.
func (p Stretch) Blocks(n *Network) []string {
	const touch = 0.5
	out := []string{}
	for i := len(p) - 1; i >= 0; i-- {
		s := p[i]
		b := n.Block(s.Leg)
		if b == "" || s.To-s.From < touch || slices.Contains(out, b) {
			continue
		}
		out = append(out, b)
	}
	return out
}

// Overlaps reports whether two stretches share track: the same stretch of a
// track or junction route, or any part of one junction on two routes
// that can't both be used at once (see Network.Fouls).
func (p Stretch) Overlaps(n *Network, o Stretch) bool {
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
				if n.Fouls(a.Leg, b.Leg) {
					return true
				}
				continue
			}
			alo, ahi := n.forward(a)
			blo, bhi := n.forward(b)
			if alo < bhi-touch && blo < ahi-touch {
				return true
			}
		}
	}
	return false
}

// forward is a span's stretch measured from its route's first end.
func (n *Network) forward(s Span) (float64, float64) {
	if !s.Leg.Reverse {
		return s.From, s.To
	}
	l := n.Length(s.Leg)
	return l - s.To, l - s.From
}
