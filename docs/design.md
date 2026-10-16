# Interlock design

Interlock is an LCC (OpenLCB) layout controller: a single Go binary with a web
interface that discovers the nodes on a layout, lets you configure them without
knowing what a memory space is, and runs Home Assistant style automations on
top of them. It aims to replace JMRI for LCC-first layouts.

This document records the architecture and the phased plan. It is a living
document; decisions marked **Decision** are settled, items marked **Open** are
not.

## Goals

- Friendlier than JMRI. Nodes, turnouts, blocks and trains appear by their
  names. Configuration is a form, not a memory map. Automations are built in
  the UI from triggers, conditions and actions.
- Full LCC coverage over time: CAN frame transfer, message network, events,
  datagrams, memory configuration, CDI, SNIP, PIP, firmware upgrade, train
  control, train search, broadcast time, OpenLCB TCP.
- First targets are the user's own hardware: `simple-dcc` (DCC command station
  with traction, service mode and POM) and `canbus-outpost` (servo switch
  machines, RailCom detectors, LCC firmware upgrade).
- Single static binary, no external services. Runs on a Raspberry Pi or a
  laptop. Frontend embedded with `go:embed`.

## Non-goals (for now)

- Decoder definition database and a DecoderPro clone. Raw CV read and write
  comes first; decoder files can come later.
- Panel editor (track diagram). Automations and entity dashboards come first.
- Being a general purpose home automation hub. Interlock talks LCC; it can
  expose itself to Home Assistant later (MQTT or a native integration).

## What the first two devices actually expose

These are facts from the firmware, and they drive the first milestones.

### simple-dcc (command station)

- Transport: GridConnect over USB CDC. Optional MCP2515 CAN as a second
  transport. Frames received on one transport are **not** forwarded to the
  other, so a controller on USB does not see CAN nodes behind it. Interlock
  therefore needs its own hub so it can join several links into one bus.
- Command station node with CDI (user identification, auto-claim, RailCom
  cutout, current limits, pin assignments, display, MCP2515).
- Train nodes created on demand through the Train Search protocol, or when a
  throttle verifies a node ID in the DCC proxy range `06.01.00.00.xx.xx`
  (long addresses set bit 0xC000). Each train node speaks Train Control:
  speed and direction, functions F0 to F68, emergency stop, query speed and
  function, controller assign and release, consist attach, detach and query,
  listener forwarding.
- POM: memory space 0xF8 on the train node, CV `n` at address `n - 1`, plus
  the OpenMRN indirect form at `0x7F000000` (CV index) and `0x7F000004` (CV
  value). Reads need RailCom and are not implemented yet.
- Service mode: the command station node produces the well-known
  "is programming track" event. CV reads and writes go to space 0xF8 on that
  node at `CV - 1`, answered with Datagram OK reply-pending and a delayed
  read or write reply. Error codes distinguish no ack, no loco, short and
  programming disabled.
- Not implemented yet: DCC accessory packets, traction reservation
  (`0x40`), remote consist members on other nodes.

### canbus-outpost (switch machines and RailCom detectors)

- Transport: CAN (can2040) and a USB GridConnect bridge. Frames seen on CAN
  are forwarded to USB and the other way, so Interlock on its USB port sees
  the whole CAN segment.
- CDI with four replicated `Servo` groups: throw, close and center command
  events, thrown and closed feedback events, positions, timing, frog output,
  fascia input, description. Four replicated `Track` groups: description,
  occupied and clear events, a reporter ID, orientation.
- RailCom reports in the OpenLCB DCC detector format: a 64K event range per
  track where the low 16 bits carry entry west, entry east, entry with
  unknown orientation, or exit, plus a 14 bit address code (long address,
  or `0x3800 | short`, or `0x3900 | consist`). Identify Producer on the range
  lists the locomotives present.
- Firmware upgrade per the Firmware Upgrade Standard: freeze, write space
  0xEF, unfreeze, reboot. A/B partitions with try-before-you-buy.
- Node ID set over the USB console (`nodeid`), not over LCC.

## Architecture

```
                     +---------------------------------------------+
                     |                 interlock                   |
 USB GridConnect --->| transports --> hub --> openlcb node + client |
 TCP GridConnect --->|   (serial,   (frame   (alias, datagrams,    |
 OpenLCB TCP    ---->|    tcp,       router)  memconfig, traction) |
 SocketCAN      ---->|    socketcan)             |                 |
                     |                           v                 |
                     |   registry (nodes, CDI, SNIP, PIP, events)  |
                     |                           |                 |
                     |   entities (turnout, block, reporter, train,|
                     |             sensor, button, clock)          |
                     |                           |                 |
                     |   automations (triggers, conditions,        |
                     |                actions, traces)             |
                     |                           |                 |
                     |   store (sqlite) + config dir (yaml)        |
                     |                           |                 |
                     |   HTTP API + WebSocket    +-- embedded SPA  |
                     +---------------------------------------------+
```

### Package layout

**Decision:** the OpenLCB protocol code is a public, reusable Go module inside
this repo; the application is under `internal/`. No Go OpenLCB library
exists today, so the protocol packages should be usable by other Go
projects (simulators, bridges, test tools) without pulling in the app.

```
cmd/interlock/            main: flags, config, wiring, serve
openlcb/                  core types: NodeID, EventID, MTI, Message, errors
openlcb/can/              CAN frame model, GridConnect codec, alias
                          allocation, CAN <-> message assembly
openlcb/transport/        Link interface; serial, tcp (GridConnect),
                          olcbtcp (OpenLCB TCP), socketcan (linux)
openlcb/hub/              frame router across links, loop prevention
openlcb/node/             a local node: alias state machine, PIP, SNIP,
                          event registry, datagram engine, memconfig server
openlcb/client/           outbound protocol clients: verify, snip, pip,
                          memconfig (read, write, info, lock, freeze),
                          event identify, traction, train search, firmware
openlcb/cdi/              CDI XML parser, element tree, byte layout
                          resolver (offset of every variable including
                          replicated groups), value codec, hints
openlcb/traction/         train control message encode/decode, float16,
                          consist flags, search event encoding
openlcb/detector/         DCC detector event range encode/decode
openlcb/wellknown/        well-known event IDs and ranges
openlcb/sim/              simulated nodes for tests and UI development
internal/registry/        discovered nodes and their cached capability data
internal/entity/          entity model and the profile system
internal/automation/      automation engine
internal/store/           sqlite access, migrations
internal/config/          yaml config dir loading and writing
internal/api/             HTTP handlers, WebSocket event stream
internal/server/          http server, embed of web/dist
web/                      frontend (TypeScript SPA)
docs/                     this document and friends
```

### Transports and hub

A `Link` sends and receives CAN frames (or, for OpenLCB TCP, whole
messages). Interlock always runs a hub: every frame received on one link is
repeated to all other links, so separate physical segments behave as one
bus, and alias allocation works across them because CID and RID frames are
repeated too. Interlock's own node attaches to the hub like a link.

**Decision:** first transports are serial GridConnect (both current devices)
and TCP GridConnect (JMRI, OpenMRN hubs, other tools). OpenLCB TCP and
SocketCAN follow. The hub also serves TCP GridConnect so JMRI or a second
Interlock can attach for debugging during the transition.

### The local node

Interlock is itself an LCC node. It needs a node ID from a real range (the
user plans to request one from OpenLCB); until configured it uses a locally
assigned `0A` prefix ID derived from the machine, matching what
`canbus-outpost` does. The local node:

- answers Verify Node ID, PIP, SNIP, event identify, and serves a small CDI
  of its own, so other tools see it as a well-behaved node;
- is the controller node for throttles (Train Control controller assign);
- produces and consumes events for virtual entities (buttons, sensors,
  automation outputs);
- can run a Broadcast Time clock later.

### Discovery and the registry

On connect and on demand: Verify Node ID Global, then for every node that
answers: PIP, SNIP, and if the node supports CDI, read space 0xFF and parse
it. Then Identify Events Global so the registry learns which events each
node produces and consumes and their current state. All of this is cached
in sqlite keyed by node ID so a restart is instant and offline nodes still
show up as "last seen".

CDI reads are slow on CAN (64 bytes per datagram, hundreds of datagrams per
node). The registry caches by node ID plus SNIP software version, and reads
again only when the version changes or the user asks.

### Capability index from CDI

CDI tells us the type, size, name and position of every configuration
variable, including every `eventid`. From that alone Interlock builds:

- a config form for the node, grouped as the CDI groups it (replaces JMRI's
  tree of raw fields, with maps rendered as selects, defaults, min and max,
  and the newer CDI features such as float and action elements where
  present);
- a named event table: every `eventid` variable becomes "node name / group
  / repname / variable name", so an automation picks "Yard / Servo 2 /
  Throw Command Event" from a list instead of typing 16 hex digits;
- producer and consumer direction from the Identify Events replies;
- the current configured event ID value, read from space 0xFD.

On top of that, **profiles** turn groups of named events into entities. A
profile matches a node by SNIP manufacturer and model (and optionally
software version) and says, for example, "each `Servo` group with a Throw
Command Event, Close Command Event, Thrown Feedback Event and Closed
Feedback Event is a turnout; use thrown/closed feedback as state". Profiles
for `simple-dcc` and `canbus-outpost` ship with Interlock. A generic
heuristic profile tries the same name matching on unknown nodes and flags
its guesses so the user can confirm or fix them in the UI. Users can also
build entities by hand from any events.

**Decision:** profiles are data (YAML), not Go code, so adding support for
a new commercial node is a file, not a release.

### Entities

Entities are the layer automations and the UI work with. Each has an ID, a
friendly name, a kind, a state, attributes and a set of commands. The first
kinds:

| Kind | State | Commands | Source |
|---|---|---|---|
| turnout | thrown, closed, moving, unknown | throw, close, center | event pairs |
| block | occupied, clear, unknown | none | event pair |
| reporter | list of locomotives with orientation | none | DCC detector range |
| sensor | on, off | none | event pair, generic |
| button | none | press | single event |
| train | speed, direction, functions, controller | set speed, function, estop | train node |
| command station | track power, faults, service mode busy | power on, off | CS node events and CDI |
| node | online, offline, last seen | reboot, identify, factory reset | registry |

Every entity change and every raw event report goes on an internal bus;
the WebSocket stream and the automation engine both subscribe to it.

### Automations

Modeled on Home Assistant automations because that model is understood by
the user and by a large audience.

- **Triggers:** entity state changed (with from and to), event received
  (by ID or by name), train search or reporter entry and exit (a specific
  locomotive enters a block), time of day or fast clock time, interval,
  node came online or went offline, Interlock started.
- **Conditions:** entity state, time window, fast clock window, numeric
  comparison on attributes, and, or, not.
- **Actions:** entity command (throw turnout, set speed), send raw event,
  delay, wait for a trigger with timeout, choose and if-then-else, repeat,
  stop, log, set a variable.
- **Modes:** single, restart, queued, parallel.
- **Traces:** each run records the trigger, evaluated conditions and each
  action with timing, viewable in the UI, like HA traces. This is the main
  debugging tool and should exist from the first version.

The engine interprets a declarative document (JSON in the API, YAML on
disk). No template language in version one; a small expression language
(`expr-lang/expr`) can be added for conditions later without changing the
document shape. Scripting beyond that (Lua) is a later option.

### Storage

**Decision:** declarative configuration lives in a config directory as YAML
files edited by both the UI and the user, so it diffs and backs up well:
`interlock.yaml` (server, node ID, links), `automations.yaml`, `entities.yaml`
(manual entities and overrides), `profiles/*.yaml` (user profiles).
Runtime data lives in a sqlite database in the same directory: node cache
(SNIP, PIP, CDI XML, event tables), event log, entity history, automation
traces, RailCom sightings. `modernc.org/sqlite` keeps the build cgo free.

### Train control and CV programming

- Throttle: Train Search with allocate to find or create a train node, then
  Controller Assign, then speed and function commands. Listener and consist
  support in the UI after that.
- POM: write CV through space 0xF8 on the train node (direct form). Read
  waits on RailCom support in `simple-dcc`.
- Service mode: find the node producing the "is programming track" event,
  then read and write space 0xF8 on it at `CV - 1`, honoring reply-pending
  and the DCC error codes. UI: a CV read and write tool first; decoder
  definition support is a later phase.

### Firmware upgrade

Standard flow: PIP check for firmware upgrade support, optional Freeze,
write the image to space 0xEF in order with `write` datagrams, Unfreeze,
then watch for the node to come back and compare SNIP software version.
The UI shows progress and the result. `canbus-outpost` is the first test
case, with its hash check and A/B trial.

### Frontend

**Decision:** React with TypeScript, built by Vite, embedded with
`go:embed`. Talks to the API over REST for configuration and a WebSocket for
the live entity and event stream. No server side rendering. The build runs
in CI; the repo also commits nothing from `web/dist` so Go-only
contributors run `make web` once.

Pages for version one: Nodes (list, detail with config form, CDI raw view,
firmware), Entities (grouped by kind with live state and controls),
Throttle, Programming (CV tool), Automations (list, editor, traces), Bus
monitor (live frame and message log with decoding, filter and record), and
Settings (links, node ID, profiles).

### API

REST under `/api/v1`, JSON. WebSocket at `/api/v1/stream` carrying entity
state changes, decoded messages when the monitor is open, automation trace
updates and node status. The API is the only way the frontend talks to the
server, so the same API serves scripts and a future CLI.

### Testing

- Table-driven unit tests for every codec (GridConnect, CAN assembly, CDI
  layout, traction float16, detector events) using vectors from the
  standards and from frames captured from the real devices.
- `openlcb/sim` simulates nodes from a CDI and a behavior script. Simulated
  `simple-dcc` and `canbus-outpost` nodes, built from their actual CDI XML,
  run the whole stack in tests and let the UI be developed without
  hardware.
- Integration tests run the server against the simulator over an in-memory
  link and drive the API.
- Hardware test checklist in `docs/hardware-testing.md` for things only the
  bench can confirm (service mode timing, firmware upgrade, RailCom).

## Phases

Each phase ends with something usable. Order follows the user's priorities:
command station and the outpost first.

### Phase 0: skeleton

Go module, `cmd/interlock`, config loading, logging with `slog`, sqlite
store with migrations, HTTP server with a placeholder page, Makefile,
golangci-lint, GitHub Actions for test and lint, release-please. CLAUDE.md
describing the build and conventions.

### Phase 1: bus

`openlcb` core types and GridConnect codec. Serial and TCP GridConnect
links. Hub. Local node with alias allocation, Verify Node ID, PIP, SNIP,
Identify Events, and a datagram engine. Bus monitor page with decoded
messages. Milestone: plug in `canbus-outpost` and `simple-dcc`, see their
nodes and every message on the bus in the browser.

### Phase 2: registry and configuration

Memory configuration client. CDI parser and layout resolver. Discovery and
caching. Nodes page with a generated config form, write with Update
Complete, reboot, factory reset, identify. Milestone: configure a servo on
`canbus-outpost` and the current limits on `simple-dcc` from Interlock,
with the result visible in JMRI as a cross check.

### Phase 3: entities and profiles

Named event table from CDI. Profile loader and the two shipped profiles.
Entity model, turnout and block and reporter and sensor kinds. Entities
page with live state and controls. DCC detector decoding into reporter
state. Milestone: throw a turnout and watch a block go occupied and the
reporter name the locomotive.

### Phase 4: automations

Engine with the trigger, condition and action set above, modes, traces.
Editor UI and trace viewer. YAML persistence. Milestone: "when Track 2
becomes occupied and Turnout 1 is thrown, close Turnout 1 after 3 seconds"
built in the UI, with a trace of it running.

### Phase 5: trains

Train search, controller assign, speed and functions. Throttle page.
Train entities and automation actions (set speed, stop a train entering a
block). Service mode and POM CV tool. Milestone: run a locomotive from
the browser and read a CV on the programming track.

### Phase 6: firmware and polish

Firmware upgrade flow and UI against `canbus-outpost`. OpenLCB TCP link.
SocketCAN link. Broadcast time clock. Backup and restore of the config
directory. Documentation site.

### Later

Decoder definitions, panel editor, Home Assistant integration, multi-user
auth, accessory decoder turnouts once `simple-dcc` sends accessory packets,
train reservation protocol, function description information (space 0xFA).

## Decisions on the open questions

- **Node ID:** Interlock uses a locally assigned ID (0A prefix over 40
  random bits) generated on first start and kept in the database, until
  the user sets `node_id` in `interlock.yaml` from a real range. Each
  Interlock instance on a bus generates its own.
- **USB to CAN:** `simple-dcc` stays as it is. `canbus-outpost` boards are
  the USB to CAN adapters; Interlock's hub joins whatever links it has.
- **Unknown nodes:** CDI name heuristics suggest entities and the user
  confirms or corrects them in the UI. Shipped profiles need no confirmation.

## Status

- Phase 0 done (2026-10-16): module, config, sqlite store with migrations,
  node identity, HTTP server with the API skeleton and embedded React
  frontend, Makefile, lint, CI and release-please.
- Next: Phase 1, the bus.

## References

- OpenLCB standards and technical notes: https://openlcb.org/openlcb-and-lcc-documents/
- OpenMRN (reference C++ implementation, checked out at `~/git/openmrn`)
- `~/git/simple-dcc` and `~/git/canbus-outpost` firmware and their CDI files
- DCC detector event format: openlcb/documents working note and PR 196
