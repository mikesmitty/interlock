package withrottle

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mikesmitty/interlock/internal/clock"
	"github.com/mikesmitty/interlock/internal/entity"
	"github.com/mikesmitty/interlock/internal/train"
)

// Queue sizes: lines waiting to be written, and changes waiting for the
// app's goroutine. An app that falls this far behind is dropped.
const (
	outQueue  = 1024
	taskQueue = 1024
)

// writeTimeout drops an app that stops reading.
const writeTimeout = 10 * time.Second

// loco is a locomotive in one of an app's throttles, with what the app
// was last told about it, so changes made elsewhere reach it and its own
// do not come back.
type loco struct {
	key string // L341 or S3
	// id is what the train manager knows it by: the roster entry chosen,
	// else the address.
	id string
	// address is the address's train ID, the same for every roster entry
	// that shares it.
	address   string
	speed     int
	reverse   bool
	on        map[int]bool
	functions int
	momentary map[int]bool
	// failed is set once the app was told a command failed, until one
	// works again.
	failed bool
	// held is whether the loco counts in the server's holds, so Interlock
	// gives the train up when the app lets it go.
	held bool
}

// labelsOnly is the throttle Engine Driver uses to collect a roster's
// function labels, adding each locomotive and dropping it again. Its
// locomotives are answered from the roster without taking the train,
// which would take it from any other throttle.
const labelsOnly = "6"

// client is one app's connection. Everything below the channels belongs
// to its run goroutine.
type client struct {
	s     *Server
	conn  net.Conn
	base  *slog.Logger
	log   *slog.Logger
	out   chan string
	tasks chan func(*client)
	done  chan struct{}
	once  sync.Once

	ctx  context.Context
	name string
	// device is the app's own ID; the server's mutex guards it.
	device string
	// replaced is set when the app connected again and this connection
	// is closed for it: its trains are let go but not stopped.
	replaced  atomic.Bool
	throttles map[string]map[string]*loco
	// watched is set while the app asks for its heartbeat to be watched.
	watched bool
	heard   time.Time
	lapsed  bool
	// What the app was last sent.
	roster        string
	consists      string
	turnoutNames  string
	turnoutStates map[string]int
	power         int
	clock         string
}

func newClient(s *Server, conn net.Conn) *client {
	log := s.log.With("app", conn.RemoteAddr().String())
	return &client{
		s:             s,
		conn:          conn,
		base:          log,
		log:           log,
		out:           make(chan string, outQueue),
		tasks:         make(chan func(*client), taskQueue),
		done:          make(chan struct{}),
		throttles:     make(map[string]map[string]*loco),
		turnoutStates: make(map[string]int),
		power:         -1,
	}
}

// close ends the connection; run then returns.
func (c *client) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

// queue runs fn on the app's goroutine. It never blocks.
func (c *client) queue(fn func(*client)) {
	select {
	case c.tasks <- fn:
	case <-c.done:
	default:
		c.log.Warn("app fell behind; closing its connection")
		c.close()
	}
}

// send queues a line for the app.
func (c *client) send(line string) {
	select {
	case c.out <- line:
	case <-c.done:
	default:
		c.log.Warn("app is not reading; closing its connection")
		c.close()
	}
}

// message shows the app a message.
func (c *client) message(text string) { c.send("HM" + clean(text)) }

func (c *client) run(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	c.ctx = ctx
	c.log.Info("throttle app connected")
	go c.write()
	lines := make(chan string)
	go c.read(lines)
	c.heard = time.Now()
	c.greet()
	tick := time.NewTicker(c.s.opts.Heartbeat / 4)
	defer tick.Stop()
	quit := false
	defer func() {
		c.close()
		c.releaseAll(!quit && c.watched && !c.replaced.Load(), parent.Err() != nil)
		c.log.Info("throttle app left")
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			c.heard = time.Now()
			c.lapsed = false
			if !c.handle(line) {
				quit = true
				return
			}
		case fn := <-c.tasks:
			fn(c)
		case now := <-tick.C:
			c.checkHeartbeat(now)
		}
	}
}

// write sends queued lines, as many at once as are waiting.
func (c *client) write() {
	w := bufio.NewWriter(c.conn)
	for {
		select {
		case <-c.done:
			return
		case line := <-c.out:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			_, _ = w.WriteString(line + "\n")
			for more := true; more; {
				select {
				case line := <-c.out:
					_, _ = w.WriteString(line + "\n")
				default:
					more = false
				}
			}
			if err := w.Flush(); err != nil {
				c.close()
				return
			}
		}
	}
}

// read passes the app's lines on until the connection ends.
func (c *client) read(lines chan<- string) {
	defer close(lines)
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 4096), 64*1024)
	sc.Split(splitLines)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		select {
		case lines <- line:
		case <-c.done:
			return
		}
	}
}

// splitLines splits at either line ending, since apps differ.
func splitLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// greet sends what an app needs on connecting.
func (c *client) greet() {
	o := c.s.opts
	c.send("VN2.0")
	c.send("HTInterlock")
	description := strings.TrimSpace("Interlock " + o.Version)
	if o.Name != "Interlock" {
		description += " " + o.Name
	}
	c.send("Ht" + clean(description))
	c.roster = rosterList(o.Trains.List())
	c.send(c.roster)
	c.consistsChanged()
	c.power = powerState(o.Trains.Layout())
	c.send("PPA" + strconv.Itoa(c.power))
	if o.Turnouts != nil {
		c.send(turnoutTitles)
		c.sendTurnouts(o.Turnouts.List())
	}
	if o.WebPort > 0 {
		c.send("PW" + strconv.Itoa(o.WebPort))
	}
	if o.Clock != nil {
		c.clockChanged(o.Clock.State())
	}
	c.send("*" + strconv.Itoa(c.heartbeatSeconds()))
}

func (c *client) heartbeatSeconds() int {
	return max(1, int(c.s.opts.Heartbeat/time.Second))
}

// handle carries out one line from the app; it returns false when the
// app is leaving.
func (c *client) handle(line string) bool {
	switch {
	case line == "Q":
		return false
	case line[0] == '*':
		switch line[1:] {
		case "+":
			c.watched = true
		case "-":
			c.watched = false
		}
	case line[0] == 'N':
		// Some apps send their name with every heartbeat.
		if name := line[1:]; name != c.name {
			c.name = name
			c.log = c.base.With("name", name)
		}
		c.send("*" + strconv.Itoa(c.heartbeatSeconds()))
	case strings.HasPrefix(line, "HU"):
		if device := line[2:]; device != "" {
			c.s.replace(c, device)
		}
	case line[0] == 'M':
		c.multi(line)
	case strings.HasPrefix(line, "PPA"):
		c.setPower(line[3:])
	case strings.HasPrefix(line, "PTA"):
		c.turnout(line[3:])
	default:
		c.log.Debug("ignored a command", "line", line)
	}
	return true
}

// checkHeartbeat stops the app's trains when it was asked to be watched
// and has gone quiet, as when a phone drops off the network, and closes
// the connection if it stays quiet.
func (c *client) checkHeartbeat(now time.Time) {
	if !c.watched {
		return
	}
	quiet := now.Sub(c.heard)
	if quiet >= c.s.opts.Heartbeat*4 {
		c.log.Warn("throttle app stayed quiet; closing its connection")
		c.close()
		return
	}
	if c.lapsed || quiet < c.s.opts.Heartbeat*3/2 {
		return
	}
	c.lapsed = true
	c.log.Warn("throttle app went quiet; stopping its trains")
	for _, l := range c.all() {
		c.stop(l)
	}
}

// all lists the app's locomotives across its throttles.
func (c *client) all() []*loco {
	var out []*loco
	for _, t := range c.throttles {
		for _, l := range t {
			out = append(out, l)
		}
	}
	return out
}

// stop brings a locomotive to an emergency stop.
func (c *client) stop(l *loco) {
	ctx, cancel := context.WithTimeout(c.ctx, commandTimeout)
	defer cancel()
	l.speed = 0
	if _, err := c.s.opts.Trains.EmergencyStop(ctx, l.id); err != nil {
		c.log.Warn("stopping a train failed", "train", l.id, "error", err)
	}
}

// releaseAll lets every locomotive go as the app leaves, stopping them
// first when it vanished while watched. When Interlock is shutting down
// the trains are only forgotten, since the node is leaving the bus.
func (c *client) releaseAll(stop, shutdown bool) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.ctx), commandTimeout)
	defer cancel()
	c.ctx = ctx
	for _, l := range c.all() {
		if shutdown {
			if l.held {
				c.s.unhold(l.address)
			}
			continue
		}
		if stop {
			c.stop(l)
		}
		c.letGo(l)
	}
	c.throttles = map[string]map[string]*loco{}
}

// --- throttles ---------------------------------------------------------------

// multi carries out a multi-throttle command: M, the throttle, then +
// (take), - (release), S (take after a steal prompt) or A (action).
func (c *client) multi(line string) {
	if len(line) < 3 {
		return
	}
	th, op := line[1:2], line[2]
	key, arg, _ := strings.Cut(line[3:], keySep)
	switch op {
	case '+', 'S':
		c.add(th, key, arg)
	case '-':
		c.drop(th, key)
	case 'A':
		c.act(th, key, arg)
	}
}

// add takes a locomotive into a throttle: by address, or by roster entry
// when arg is E and its name.
func (c *client) add(th, key, arg string) {
	address, long, ok := parseKey(key)
	if !ok {
		c.message(fmt.Sprintf("%s is not a locomotive address", key))
		return
	}
	t := c.throttles[th]
	if t == nil {
		t = make(map[string]*loco)
		c.throttles[th] = t
	}
	if l, ok := t[key]; ok {
		if tr, ok := c.s.opts.Trains.Get(l.id); ok {
			c.send("M" + th + "+" + key + keySep)
			c.sendLoco(th, l, tr)
		}
		return
	}
	addressID := train.IDFor(address, long)
	id := addressID
	if name, ok := strings.CutPrefix(arg, "E"); ok && name != "" {
		if rid := c.rosterID(name, address, long); rid != "" {
			id = rid
		}
	}
	var tr train.Train
	if th == labelsOnly {
		tr, _ = c.s.opts.Trains.Get(id)
	} else {
		ctx, cancel := context.WithTimeout(c.ctx, commandTimeout)
		defer cancel()
		var err error
		if tr, err = c.s.opts.Trains.Acquire(ctx, id); err != nil {
			c.message(fmt.Sprintf("Could not take %s: %s", key[1:], reason(err)))
			return
		}
	}
	l := &loco{key: key, id: id, address: addressID, momentary: momentary(tr), held: th != labelsOnly}
	t[key] = l
	if l.held {
		c.s.hold(addressID)
		c.log.Info("app took a train", "train", id, "throttle", th)
	}
	c.send("M" + th + "+" + key + keySep)
	c.sendLoco(th, l, tr)
}

// rosterID finds the roster entry an app chose by name.
func (c *client) rosterID(name string, address uint16, long bool) string {
	for _, t := range c.s.opts.Trains.List() {
		if t.Roster && clean(t.Name) == name && t.Address == address && (t.Long || t.Address > 127) == long {
			return t.ID
		}
	}
	return ""
}

// sendLoco tells the app a locomotive's labels and state.
func (c *client) sendLoco(th string, l *loco, tr train.Train) {
	l.functions = functionCount(tr)
	if len(tr.Functions) > 0 {
		c.send("M" + th + "L" + l.key + keySep + functionLabels(tr.Functions, l.functions))
	}
	prefix := "M" + th + "A" + l.key + keySep
	l.on = onSet(tr.On)
	for fn := range l.functions {
		c.send(prefix + "F" + bit(l.on[fn]) + strconv.Itoa(fn))
	}
	l.speed = speedOf(tr)
	c.send(prefix + "V" + strconv.Itoa(l.speed))
	l.reverse = tr.Direction == train.Reverse
	c.send(prefix + "R" + bit(!l.reverse))
	c.send(prefix + "s1")
}

// drop releases a locomotive, or all of a throttle's with *.
func (c *client) drop(th, key string) {
	t := c.throttles[th]
	keys := []string{key}
	if key == "*" {
		keys = keys[:0]
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}
	for _, k := range keys {
		l, ok := t[k]
		if !ok {
			continue
		}
		delete(t, k)
		c.send("M" + th + "-" + k + keySep)
		c.letGo(l)
	}
}

// letGo gives a train up when no app's throttle holds it any longer.
// While the layout's emergency stop holds the trains it sets the train to
// 0 first, whether the app let it go or dropped off the network: released
// at a speed, it would start again on Resume with no throttle driving it.
// An app that connected again takes its trains over as they are.
func (c *client) letGo(l *loco) {
	if !l.held || !c.s.unhold(l.address) {
		return
	}
	ctx, cancel := context.WithTimeout(c.ctx, commandTimeout)
	defer cancel()
	trains := c.s.opts.Trains
	if trains.Layout().EmergencyStop && !c.replaced.Load() {
		if tr, ok := trains.Get(l.id); ok && tr.Speed != 0 {
			if _, err := trains.SetSpeed(ctx, l.id, 0, ""); err != nil {
				c.log.Warn("setting a held train to 0 failed", "train", l.id, "error", err)
			}
		}
	}
	if _, err := trains.Release(ctx, l.id); err != nil {
		c.log.Debug("release failed", "train", l.id, "error", err)
	}
}

// act carries out an action on a locomotive, or on all of a throttle's
// with *.
func (c *client) act(th, key, arg string) {
	if arg == "" {
		return
	}
	t := c.throttles[th]
	var locos []*loco
	if key == "*" {
		for _, l := range t {
			locos = append(locos, l)
		}
		sort.Slice(locos, func(i, j int) bool { return locos[i].key < locos[j].key })
	} else if l, ok := t[key]; ok {
		locos = append(locos, l)
	}
	for _, l := range locos {
		c.do(th, l, arg)
	}
}

func (c *client) do(th string, l *loco, arg string) {
	ctx, cancel := context.WithTimeout(c.ctx, commandTimeout)
	defer cancel()
	trains := c.s.opts.Trains
	prefix := "M" + th + "A" + l.key + keySep
	var err error
	switch arg[0] {
	case 'V':
		n, perr := strconv.Atoi(arg[1:])
		switch {
		case perr != nil:
			return
		case n < 0:
			l.speed = 0
			_, err = trains.EmergencyStop(ctx, l.id)
		default:
			l.speed = min(n, train.MaxSpeed)
			_, err = trains.SetSpeed(ctx, l.id, float32(l.speed), "")
		}
	case 'R':
		before := l.reverse
		l.reverse = arg[1:] == "0"
		direction := train.Forward
		if l.reverse {
			direction = train.Reverse
		}
		var speed float32
		if tr, ok := trains.Get(l.id); ok {
			speed = tr.Speed
		}
		if _, err = trains.SetSpeed(ctx, l.id, speed, direction); err != nil {
			l.reverse = before
		}
		// Apps show the direction they are told, as other servers do.
		c.send(prefix + "R" + bit(!l.reverse))
	case 'X':
		l.speed = 0
		_, err = trains.EmergencyStop(ctx, l.id)
	case 'I':
		l.speed = 0
		_, err = trains.SetSpeed(ctx, l.id, 0, "")
	case 'F', 'f', 'm':
		if len(arg) < 3 {
			return
		}
		fn, perr := strconv.Atoi(arg[2:])
		if perr != nil || fn < 0 || fn > train.MaxFunction {
			return
		}
		set := arg[1] == '1'
		switch arg[0] {
		case 'm':
			l.momentary[fn] = set
			return
		case 'F':
			// A button: pressed and released. A momentary function is on
			// while it is held; a latching one changes on each press.
			if !l.momentary[fn] {
				if !set {
					return
				}
				set = !l.on[fn]
			}
		}
		before := l.on[fn]
		l.on[fn] = set
		if _, err = trains.SetFunction(ctx, l.id, fn, set); err != nil {
			l.on[fn] = before
		}
		// Apps leave a press to the server and show the state it sends.
		if fn < l.functions {
			c.send(prefix + "F" + bit(l.on[fn]) + strconv.Itoa(fn))
		}
	case 'Q':
		// The app is done with this locomotive: stop it and let it go.
		l.speed = 0
		_, err = trains.SetSpeed(ctx, l.id, 0, "")
		c.drop(th, l.key)
	case 'q':
		switch arg[1:] {
		case "V":
			c.send(prefix + "V" + strconv.Itoa(l.speed))
		case "R":
			c.send(prefix + "R" + bit(!l.reverse))
		case "s":
			c.send(prefix + "s1")
		case "m":
			for fn := range l.functions {
				c.send(prefix + "m" + bit(l.momentary[fn]) + strconv.Itoa(fn))
			}
		}
		return
	case 's':
		// Interlock drives every train in 128 steps.
		c.send(prefix + "s1")
		return
	default:
		return
	}
	if err != nil {
		if !l.failed {
			c.message(fmt.Sprintf("%s did not take the command: %s", l.key[1:], reason(err)))
		}
		l.failed = true
		c.log.Debug("command failed", "train", l.id, "action", arg, "error", err)
		return
	}
	l.failed = false
}

// trainChanged passes on what changed about a train the app holds.
func (c *client) trainChanged(t train.Train) {
	trains := c.s.opts.Trains
	if roster := rosterList(trains.List()); roster != c.roster {
		c.roster = roster
		c.send(roster)
		// The consists list their locomotives' addresses.
		c.consistsChanged()
	}
	address := train.IDFor(t.Address, t.Long)
	for th, locos := range c.throttles {
		for _, l := range locos {
			if l.address != address {
				continue
			}
			// The state now, not the one that came with the change, so a
			// run of the app's own commands does not echo back.
			cur, ok := trains.Get(l.id)
			if !ok {
				continue
			}
			prefix := "M" + th + "A" + l.key + keySep
			if speed := speedOf(cur); speed != l.speed {
				l.speed = speed
				c.send(prefix + "V" + strconv.Itoa(speed))
			}
			if reverse := cur.Direction == train.Reverse; reverse != l.reverse {
				l.reverse = reverse
				c.send(prefix + "R" + bit(!reverse))
			}
			on := onSet(cur.On)
			for fn := range l.functions {
				if on[fn] != l.on[fn] {
					c.send(prefix + "F" + bit(on[fn]) + strconv.Itoa(fn))
				}
			}
			l.on = on
		}
	}
}

// consistsChanged sends the consist list again when it changed.
func (c *client) consistsChanged() {
	trains := c.s.opts.Trains
	lines := consistList(trains.Consists(), trains.List())
	if all := strings.Join(lines, "\n"); all != c.consists {
		c.consists = all
		for _, line := range lines {
			c.send(line)
		}
	}
}

// --- the layout ----------------------------------------------------------------

func (c *client) setPower(arg string) {
	if arg != "0" && arg != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(c.ctx, commandTimeout)
	defer cancel()
	if _, err := c.s.opts.Trains.SetPower(ctx, arg == "1"); err != nil {
		c.message("Could not switch track power: " + reason(err))
	}
}

func (c *client) layoutChanged(l train.Layout) {
	if p := powerState(l); p != c.power {
		c.power = p
		c.send("PPA" + strconv.Itoa(p))
	}
}

// turnout carries out a turnout command: T throw, C close, 2 toggle.
func (c *client) turnout(arg string) {
	if c.s.opts.Turnouts == nil || len(arg) < 2 {
		return
	}
	id := arg[1:]
	var ent entity.Entity
	found := false
	for _, e := range c.s.opts.Turnouts.List() {
		if e.ID == id && listed(e) {
			ent, found = e, true
			break
		}
	}
	if !found {
		c.message("There is no turnout " + id)
		return
	}
	var command string
	switch arg[0] {
	case 'T':
		command = "throw"
	case 'C':
		command = "close"
	case '2':
		command = "throw"
		if turnoutState(ent) == turnoutThrown {
			command = "close"
		}
	default:
		return
	}
	ctx, cancel := context.WithTimeout(c.ctx, commandTimeout)
	defer cancel()
	if _, err := c.s.opts.Turnouts.Command(ctx, id, command); err != nil {
		c.message(fmt.Sprintf("Could not %s %s: %s", command, ent.Name, reason(err)))
	}
}

// sendTurnouts sends the turnout list and notes what it held.
func (c *client) sendTurnouts(ents []entity.Entity) {
	c.turnoutNames = turnoutNames(ents)
	c.turnoutStates = make(map[string]int)
	for _, e := range ents {
		if listed(e) {
			c.turnoutStates[e.ID] = turnoutState(e)
		}
	}
	if list := turnoutList(ents); list != "" {
		c.send(list)
	}
}

// turnoutNames is the turnouts listed, without their states, to tell
// when the list itself changed.
func turnoutNames(ents []entity.Entity) string {
	var b strings.Builder
	for _, e := range ents {
		if listed(e) {
			b.WriteString(e.ID + fieldSep + e.Name + listSep)
		}
	}
	return b.String()
}

func (c *client) turnoutChanged(e entity.Entity) {
	ents := c.s.opts.Turnouts.List()
	if turnoutNames(ents) != c.turnoutNames {
		c.sendTurnouts(ents)
		return
	}
	if !listed(e) {
		return
	}
	if state := turnoutState(e); state != c.turnoutStates[e.ID] {
		c.turnoutStates[e.ID] = state
		c.send("PTA" + strconv.Itoa(state) + clean(e.ID))
	}
}

// clockChanged sends the fast time and rate. The time is in seconds since
// 1970 with the fast clock's hours read as UTC, which is how the apps
// show it.
func (c *client) clockChanged(st clock.State) {
	at := st.Fast()
	if at.IsZero() {
		return
	}
	rate := 0.0
	if st.Running {
		rate = st.Rate
	}
	line := "PFT" + strconv.FormatInt(at.Unix(), 10) + keySep + strconv.FormatFloat(rate, 'f', 1, 64)
	if line != c.clock {
		c.clock = line
		c.send(line)
	}
}

// --- helpers -------------------------------------------------------------------

// speedOf is a train's speed in the protocol's steps; a train that is
// moving, however slowly, never shows as stopped.
func speedOf(t train.Train) int {
	n := int(math.Round(float64(t.Speed)))
	if n == 0 && t.Speed > 0 {
		n = 1
	}
	return n
}

func bit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func onSet(on []int) map[int]bool {
	out := make(map[int]bool, len(on))
	for _, fn := range on {
		out[fn] = true
	}
	return out
}

// reason is an error as the app shows it.
func reason(err error) string {
	s := err.Error()
	for _, p := range []string{"train: ", "entity: "} {
		s = strings.ReplaceAll(s, p, "")
	}
	return s
}
