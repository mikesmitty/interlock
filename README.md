# Interlock

An LCC (OpenLCB) layout controller for model railroads, built to be
easy to use: nodes show up by name, configuration is a form instead
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
train nodes, and a Programming page reads and writes CVs on the
programming track and on the main (`docs/trains.md`); phone throttle
apps such as Engine Driver find Interlock on the network and drive
through it over WiThrottle. The Programming page also sets a decoder's
settings by name from the JMRI project's decoder definitions, fetched on
demand (`docs/decoders.md`). The roster keeps each locomotive's decoders
with their CV values, so a decoder's settings open without reading it
first, and imports a JMRI roster (`docs/roster.md`). A node's page installs new firmware on it and
follows the board through its restart. Interlock runs the layout's fast
clock, which automations can use. Settings backs everything up to one
file and restores it. Links can be serial or TCP GridConnect, OpenLCB
TCP or SocketCAN. The documentation site is built from `docs/`
(`make docs`); nothing has been tried on real hardware yet, and
`docs/design.md` has the plan.

## Trying it without hardware

```bash
make build
bin/interlock -data ./data &      # web interface on :8080, hub port on :12021
go run ./cmd/simnode              # a simulated canbus-outpost joins the hub
go run ./cmd/simnode -cdi cs -id 0A.00.00.00.00.43   # and a simulated command station
go run ./cmd/simnode -make-firmware outpost-2.0.0.bin -firmware-version 2.0.0   # an image to install on it
```

`go run ./cmd/simlayout` instead simulates a whole layout, trains running
on the track plan included, for an Interlock of its own (see
[Getting started](docs/getting-started.md#a-simulated-layout)), and
[train tracking](docs/tracking.md) follows them.

Open http://localhost:8080 to see the node appear, then watch the monitor.

## Building

Requires Go (see `go.mod`) and Node 22.22 or newer.

```bash
make build        # builds the web interface and bin/interlock
bin/interlock     # serves on :8080 using your user config directory (or $INTERLOCK_DATA)
```

`bin/interlock -data /path/to/dir` keeps everything in one directory:
`interlock.yaml` (written with defaults on first start), the sqlite
database, the entities, automations, roster and your profiles. Links to
the layout go in that file:

```yaml
links:
  - name: outpost
    type: serial
    device: /dev/cu.usbmodem1234   # a canbus-outpost or simple-dcc USB port; COM3 on Windows
  - name: jmri
    type: tcp
    address: 192.168.1.10:12021    # any GridConnect TCP server
  - name: can0
    type: socketcan                # a Linux CAN interface
    interface: can0
```

Interlock also serves GridConnect on port 12021, so JMRI or a second
Interlock can connect to it, and can serve OpenLCB TCP (`hub.openlcb_tcp`).
`docs/connections.md` has the details.

## Developing

```bash
make test         # Go tests
make lint         # golangci-lint, the TypeScript type check and Prettier's check
make fmt          # gofmt and goimports, and Prettier on the web interface
make run          # Go server from source on :8080
make dev          # Vite dev server with hot reload, proxying /api to :8080
make docs         # the documentation site, from docs/
```

## License

GPL-3.0; see [LICENSE](LICENSE).
