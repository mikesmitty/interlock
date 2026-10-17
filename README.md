# Interlock

An LCC (OpenLCB) layout controller for model railroads, built to be
friendlier than JMRI: nodes show up by name, configuration is a form instead
of a memory map, and automations are built in the browser from triggers,
conditions and actions, the way Home Assistant does it.

Interlock is a single Go binary with an embedded web interface. It speaks
LCC directly over GridConnect (USB serial or TCP), acts as a hub between
links, and is itself a node on the bus.

**Status:** early. Interlock joins the bus, finds and identifies nodes,
reads their configuration descriptions, shows every message decoded, and
lets you edit a node's configuration in a form generated from its CDI,
with replicated groups as tabs and only changed values written. It turns
the events a node's configuration names into entities (turnouts, blocks,
RailCom reporters, sensors, switches) through YAML profiles, shipped for
canbus-outpost and simple-dcc and guessed for other nodes, and shows them
on an Entities page with live state and levers. Automations with Home
Assistant style triggers, conditions and actions are built in a when /
only if / then editor, kept in `automations.yaml`, and every run leaves
a trace you can read step by step (`docs/automations.md`). A Throttle
page drives locomotives by DCC address through the command station's
train nodes, with a roster in `trains.yaml`, and a Programming page reads
and writes CVs on the programming track and on the main
(`docs/trains.md`). Firmware upgrades are still to come; see
`docs/design.md` for the plan.

## Trying it without hardware

```bash
make build
bin/interlock -data ./data &      # web interface on :8080, hub port on :12021
go run ./cmd/simnode              # a simulated canbus-outpost joins the hub
go run ./cmd/simnode -cdi cs -id 0A.00.00.00.00.43   # and a simulated command station
```

Open http://localhost:8080 to see the node appear, then watch the monitor.

## Building

Requires Go (see `go.mod`) and Node 22 or newer.

```bash
make build        # builds the web interface and bin/interlock
bin/interlock     # serves on :8080 using ~/.config/interlock (or $INTERLOCK_DATA)
```

`bin/interlock -data /path/to/dir` keeps everything in one directory:
`interlock.yaml` (written with defaults on first start), the sqlite
database and, later, automations and profiles. Links to the layout go in
that file:

```yaml
links:
  - name: outpost
    type: serial
    device: /dev/cu.usbmodem1234   # a canbus-outpost or simple-dcc USB port
  - name: jmri
    type: tcp
    address: 192.168.1.10:12021    # any GridConnect TCP server
```

Interlock also serves GridConnect on port 12021, so JMRI or a second
Interlock can connect to it.

## Developing

```bash
make test         # Go tests
make lint         # golangci-lint and the TypeScript type check
make run          # Go server from source on :8080
make dev          # Vite dev server with hot reload, proxying /api to :8080
```

## License

GPL-3.0; see [LICENSE](LICENSE).
