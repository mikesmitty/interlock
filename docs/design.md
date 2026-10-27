# Interlock design

Interlock is an LCC (OpenLCB) layout controller: a single Go binary with a web
interface that discovers the nodes on a layout, lets you configure them without
knowing what a memory space is, and runs Home Assistant style automations on
top of them. It is meant to be the whole control system for a layout built
around LCC.

This document records the architecture and the phased plan. It is a living
document; decisions marked **Decision** are settled, items marked **Open** are
not.

## Goals

- Easy to use. Nodes, turnouts, blocks and trains appear by their
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

- A decoder definition database of our own. Interlock reads the
  definitions the JMRI project maintains (Phase 7) rather than keeping a
  second set.
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
- The command station's own node ID is `0A` plus the last five bytes of
  the board ID. Firmware before simple-dcc `bd2cf79` used `06.01` plus four
  bytes, inside the DCC train node range, so Interlock counts only
  `06.01.00.00.xx.xx` as train nodes.
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
openlcb/memconfig/        memory configuration: datagram codec, client
                          (read, write, info, lock, freeze, reboot,
                          factory reset), server for the local node
openlcb/cdi/              CDI XML parser, element tree, byte layout
                          resolver (offset of every variable including
                          replicated groups), value codec, hints
openlcb/traction/         train control message encode/decode, float16,
                          consist flags, search event encoding
openlcb/detector/         DCC detector event decoding (entry, exit,
                          facing, address) over a 16-bit event range
openlcb/wellknown/        well-known event IDs and ranges
openlcb/sim/              simulated nodes for tests and UI development;
                          Outpost behaves like the canbus-outpost firmware
internal/registry/        discovered nodes, CDI reads, sqlite persistence
internal/nodeconfig/      values in and out of a node through its layout
internal/eventtable/      the named event table: every node's eventid
                          variables with their values, cached in sqlite
internal/profile/         YAML profiles that turn CDI groups into entities;
                          shipped ones embedded, user ones in the data dir
internal/entity/          entity kinds and the manager: state from the bus,
                          commands, overrides and manual entities
internal/automation/      automation engine: documents, triggers, runs, traces
internal/withrottle/      WiThrottle server for throttle apps over the train
                          manager, turnouts and clock; announced by mDNS
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

### Track plan

The track diagram is the layout's model as well as its picture: the
scheduling work in Phase 8 routes over it. It is drawn on a grid. Each
cell holds track, a piece of platform or a label. A cell's track is the
list of directions it leaves the cell by: the middles of the four sides
and the four corners. The piece follows from that list, so the file
needs no piece names or rotations. One direction is the end of a
track. Two are a straight or a 45 or 90 degree curve. Three are a
turnout (a straight with a 45 degree branch) or a wye. Four are two
straights crossing. Anything else is refused. Track joins a neighboring
cell when both lead into each other.

Blocks are named stretches of track. Track cells name their block, and
the block links its block entity (occupied or clear) and its reporter
entity (which locomotives). It also carries its length in millimeters,
since the drawing is schematic, and its station, if it is a platform
track. Turnout cells link their turnout entity: closed is the straight
way, thrown the branch.

The topology is worked out on every save. It finds track that leads
nowhere. It finds each block's ends, the places where track leaves the
block. For each pair of block ends joined by track outside any block, it
finds the link between them and the turnout positions it needs. A way
that would need one turnout in two positions is no link. The browser
applies the same piece rules while drawing, so the editor shows turnouts
and crossings forming as track is dragged across a line.

Saves name the revision they were based on, so two editors can't
silently overwrite each other.

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
(manual entities and overrides), `trackplan.yaml` (the track diagram),
`profiles/*.yaml` (user profiles).
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
  and the DCC error codes. UI: a CV read and write tool first; named
  settings from decoder definitions are Phase 7.

### Firmware upgrade

Standard flow: PIP check for firmware upgrade support, optional Freeze,
write the image to space 0xEF in order with `write` datagrams, Unfreeze,
then watch for the node to come back and compare SNIP software version.
The UI shows progress and the result. `canbus-outpost` is the first test
case, with its hash check and A/B trial.

### Frontend

**Decision:** React with TypeScript, built by Vite, embedded with
`go:embed`. Talks to the API over REST for configuration and the server-sent
event stream for live state. No server side rendering. The build runs
in CI; the repo also commits nothing from `web/dist` so Go-only
contributors run `make web` once.

Pages for version one: Nodes (list, detail with config form, CDI raw view,
firmware), Entities (grouped by kind with live state and controls),
Throttle, Programming (CV tool), Automations (list, editor, traces), Bus
monitor (live frame and message log with decoding, filter and record), and
Settings (links, node ID, profiles).

### API

REST under `/api/v1`, JSON. A server-sent event stream at `/api/v1/events`
carries node changes, link status, bus traffic and, later, entity state and
automation traces; topics are selectable per connection. Server-sent
events were chosen over WebSocket because the traffic is one way, browsers
reconnect on their own, and it needs no library on either side. The API is
the only way the frontend talks to the server, so the same API serves
scripts and a future CLI.

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

### Phase 7: decoder programming

Named decoder settings instead of CV numbers, from the decoder
definitions the JMRI project maintains (about 880 families and 3,800
models).

- **Fetched, not bundled.** The definitions are GPL-2.0-only and
  Interlock is GPL-3.0, so the binary carries none of them. Three
  sources, set in `interlock.yaml`: a folder on disk (a JMRI install, a
  checkout of the JMRI repository, or a zip of either), GitHub file by
  file as they are needed (at a pinned release tag, cached in
  `<data>/decoders/`), or the repository archive at that tag downloaded
  once with only its `xml/` folder kept.
- **Read on demand.** `decoderIndex.xml` is read once; a family's file
  and the files it includes are read when a locomotive needs them and
  resolved on the server into one compact definition. The browser lays
  it out, so a Raspberry Pi only serves data and talks to the bus.
- **Identify.** CV 8 and CV 7, then the product ID CVs of manufacturers
  that have them, matched against the index.
- **Settings pages.** Numbers, choices, bits, split values, addresses
  and speed tables in the panes the definition gives, with its
  qualifiers; indexed CVs through CV 31 and 32 and the other index
  schemes the files declare.
- **Read and write by page.** Read what the page shows, write what
  changed, with progress. Values the definition marks read-only stay
  read-only.
- **Values kept per locomotive.** A roster entry gains its decoders,
  each with its definition and the CV values last read or written, so
  opening a locomotive needs no read. One decoder is fitted at a time;
  swapping brings back each decoder's own address and labels.
- **Import.** JMRI roster files (decoder, CV values, function labels)
  import into the roster.

Milestone: identify a decoder on the programming track, change its
acceleration by name, and see the change after a fresh read.

### Later

Home Assistant integration, multi-user
auth, accessory decoder turnouts once `simple-dcc` sends accessory packets,
train reservation protocol, function description information (space 0xFA).

A DCC-EX native protocol server (the `<...>` commands a DCC-EX command
station takes). WiThrottle already covers driving, turnouts, power and
the clock; what the DCC-EX protocol adds is what Engine Driver offers in
its DCC-EX mode and what tools made for DCC-EX use: reading and writing
CVs on the programming track and on the main, reading a locomotive's
address, and a command line. The Programming page does the CV work in
the browser, Engine Driver's Web tab included, so this waits until a
tool needs it. DCC-EX's track manager and EXRAIL have no LCC
counterpart.

Consist support, the rest of it. LCC consists are done (see Status);
still to come:

- DCC advanced consists, kept in the decoders: the consist address is
  read from CV 19 but set with the Consist Control instruction, so
  `simple-dcc` has to send it for a write of CV 19 through a train
  node's space 0xF8 (and the short form for CV 19 and 20 together, for
  long consist addresses, once what CV 20 holds is confirmed). The
  model has the kind and address already; making one up refuses until
  then.
- Writing a long address on the main as one instruction: the short form
  for CV 17 and 18 together, which also sets CV 29, through a two-byte
  write at CV 17 in the same space, once `simple-dcc` sends it.
  Interlock writes CV 17, 18 and 29 one at a time today.
- Consists in throttle apps: WiThrottle's consist lists, so an app
  shows the consist by name rather than its lead.
- Members on another command station, which wait on `simple-dcc`
  forwarding to train nodes it does not host.

## Decisions on the open questions

- **Node ID:** Interlock uses a locally assigned ID (0A prefix over 40
  random bits) generated on first start and kept in the database, until
  the user sets `node_id` in `interlock.yaml` from a real range. Each
  Interlock instance on a bus generates its own.
- **USB to CAN:** `simple-dcc` stays as it is. `canbus-outpost` boards are
  the USB to CAN adapters; Interlock's hub joins whatever links it has.
- **Unknown nodes:** CDI name heuristics suggest entities and the user
  confirms or corrects them in the UI. Shipped profiles need no confirmation.

### Phase 8: track plan and scheduling

Timetabled operation in the spirit of iTrain: trains that keep a
schedule and stop at stations precisely, on a layout modeled on the NYC
subway with Metro-North traffic to come. It builds up in layers, each
useful on its own:

1. **Track plan.** The diagram and its topology (above), drawn first so
   that drawing the layout shapes the model the rest uses.
2. **Train tracking.** Which train is in which block, which end leads,
   and where its tail is.
3. **Interlocking.** Routes that set and lock their turnouts and are
   released behind the train. Optionally a trip stop: an emergency stop
   for any train that enters a block it has no route into.
4. **Automatic driving.** A speed profile for each locomotive, from a
   calibration run over a measured block and refined in normal running,
   and braking curves that stop a train at a stop marker. Markers are set
   per platform, direction and train length, like the car markers on
   subway platforms.
5. **Timetable.** Service patterns (a list of stations, each a stop or
   passed), trips made from headway rules, runs chained through turnbacks
   at terminals, roster trains assigned to runs, timing points, and the
   dispatcher's cancel, short turn and swap. Times are on the fast clock;
   run times are measured on the layout.
6. **Views.** A time-distance graph of the timetable, a board of trains
   and how late they are, and platform countdown clocks.

What the hardware gives this, as of 2026-10-27:

- canbus-outpost detects occupancy only through RailCom. A track is
  occupied while a decoder in it answers on channel 1, so cars without a
  decoder can't be seen, and a train's tail has to be worked out from its
  length.
- An entry is reported after two matching address readings, a few DCC
  packets. An exit is reported only after a second with no reading. So
  entries are the timing references for braking, and exits only release
  track behind a train.
- Two decoders in one track's section garble each other. A multiple-unit
  train should have one decoder broadcasting its address (CV 28 bit 0).
- Channel 2 is counted but not decoded, so there is no speed feedback
  from RailCom. Speeds come from timing entries over known lengths.
- simple-dcc sends speed packets at high priority and stops at emergency
  priority over USB. Decoder momentum is the larger variable, so
  Interlock should do the braking itself.

Stopping precision is the riskiest part. Test it on the bench early,
since its result could change the model.

## Status

- Phase 0 done (2026-10-16): module, config, sqlite store with migrations,
  node identity, HTTP server with the API skeleton and embedded React
  frontend, Makefile, lint, CI and release-please.
- Phase 1 done (2026-10-16): `openlcb` core types, CAN layer (identifiers,
  GridConnect, alias generator and cache, message assembly), serial and
  TCP GridConnect links with reconnection, a GridConnect TCP listener, the
  hub, the local node (alias allocation and defense, Verify Node ID, PIP,
  SNIP, datagrams with acknowledgment and retry, client operations), the
  registry with discovery and identification, the monitor with decoding,
  the API with a server-sent event stream, and the Nodes, Monitor and
  Settings pages. `cmd/simnode` simulates a canbus-outpost over the hub
  port for development without hardware. Verified against the simulator;
  not yet against real hardware.
- Phase 2 done (2026-10-16): `openlcb/memconfig` (datagram codec, client
  with per-node serialization and tolerant reply handling, server with
  byte-slice spaces), `openlcb/cdi` (parser for CDI 1.4 including hints,
  action and blob elements, address resolver for replicated groups,
  value codec, expanded layout for the API), `openlcb/sim` (nodes built
  from the real canbus-outpost and simple-dcc CDI files), registry
  persistence in sqlite (nodes and CDI cached by software version,
  restored at start as offline), `internal/nodeconfig` (values keyed by
  space:address, validated against the layout before anything is
  written, Update Complete after each batch), configuration endpoints,
  and the configuration form on the node page with replicated groups as
  tabs, a save bar for pending changes, restart and factory reset.
  Verified against the simulator; not yet against real hardware.
- Deferred from Phase 2: an "identify" action. The standards have no
  general one; canbus-outpost exposes its identify LED as an ordinary
  configuration variable, which the form already covers. A profile can
  surface it as a button later.
- Phase 3 done (2026-10-16): `openlcb.EventRange` and `openlcb/detector`
  (DCC detector event decoding as canbus-outpost implements the working
  note), `internal/eventtable` (every eventid variable of every node with
  its value, read from the segments that hold events, cached in sqlite,
  re-read when a node returns or is written to, with producer and
  consumer directions from Identify Events replies), `internal/profile`
  (YAML profiles: group, role bindings by exact name or `~regexp`, name
  templates, enable variables, literal event IDs; shipped profiles for
  canbus-outpost and simple-dcc plus a generic guesser marked
  `suggest`), `internal/entity` (turnout, block, reporter, sensor,
  switch, button; state from Event Reports and Identified replies, one
  Identify Events per node when it appears, commands that send events,
  overrides and hand-built entities in `entities.yaml`), entity and
  event-name endpoints with an `entities` SSE topic, the Entities page
  (levers, suggestions to confirm, inline editor, build by hand), the
  entities section on the node page and event names in the monitor.
  `sim.Outpost` gives the simulator the firmware's servo, detector and
  Identify behavior and `cmd/simnode` walks a locomotive around its
  tracks. Milestone met against the simulator: a lever throws a turnout
  and its feedback lights the lamp, a block goes occupied and its
  reporter names the locomotive. Not yet against real hardware.
- Deferred from Phase 3: Interlock's own node does not yet answer
  Identify Events for the events its entities send and listen for;
  entity state history and RailCom sightings are not yet stored; the
  generic guesser only tries pairs that name a turnout, block, reporter
  or sensor.
- Phase 4 done (2026-10-16): `internal/automation` (the document model
  with JSON and YAML forms and validation that names the field at fault;
  triggers for entity state with from, to and for, locomotive entry and
  exit by address, event, node online and offline, time of day with
  weekdays, interval and start; conditions for state, time window,
  trigger, and, or, not; actions for command, event, delay, wait with
  timeout, if, choose, repeat by count, while or until, stop and log;
  single, restart, queued and parallel modes; traces of every run with a
  step per condition and action kept in sqlite; `automations.yaml`
  written by the API), automation endpoints with `automation` and `trace`
  SSE events, the Automations page (list with on and off levers, run and
  stop), the when / only if / then editor with nested blocks and a YAML
  readout, and the trace viewer that follows a run live.
  `docs/automations.md` documents the fields. Milestone met against the
  simulator: the milestone automation built in the editor closed the
  turnout three seconds after the block went occupied, with the trace
  showing each step. Not yet against real hardware.
- Deferred from Phase 4: fast clock triggers and conditions (no clock
  yet), numeric comparisons and variables (nothing has numbers or reads
  variables yet), templates and the expression language, event triggers
  by name rather than ID, and the Interlock node answering Identify
  Events for the events its automations send.
- Phase 5 done (2026-10-16): `openlcb/traction` (Train Control command
  and reply encoding, the Train Search event, DCC train node IDs), the
  node's traction request path (replies matched by command, a handler for
  commands addressed to Interlock, Identify Producer collection for
  searches), `openlcb/sim.Station` (a simulated simple-dcc: train nodes
  made on search, Train Control, a programming track with a decoder on it
  answering through reply pending, the emergency events), the memconfig
  server's deferred spaces, `internal/train` (the roster in `trains.yaml`,
  search with allocate, controller assign, speed, direction, functions,
  emergency stop, read-back, the layout's emergency events, notices from
  trains another throttle took), `internal/programmer` (service mode and
  POM through space 0xF8 with the DCC error codes in plain words), the
  `train` automation action, train and programming endpoints with
  `train`, `layout` and `cv` SSE events, the Throttle page (roster, cab
  with slider, direction lever, functions, emergency controls) and the
  Programming page (CV read and write with bits, POM, history).
  `docs/trains.md` documents it. Milestone met against the simulator: a
  locomotive driven from the browser and CV 8 read on the programming
  track. Not yet against real hardware.
- Decided in Phase 5: trains are their own domain rather than entities.
  An entity has one state word and parameterless commands; a train has a
  speed, a direction and functions, and is addressed by DCC address, not
  by the node a command station happens to make for it. (Phase 7 gave
  roster entries IDs of their own; the train on the bus is still the
  address's.) Automations reach
  trains through the `train` action; a train trigger and condition can
  come later on the same manager.
- Deferred from Phase 5: consist building and listener registration (the
  firmware does not forward to listeners on other nodes yet), reading CVs
  on the main (needs RailCom), function descriptions from space 0xFA,
  decoder definitions, and train entities for state triggers.
- Phase 6 done (2026-10-17): firmware upgrade (`internal/firmware`:
  Freeze, the image in 64 byte writes with retries, Unfreeze, a watch for
  the restart and for a board that tries new firmware going back; UF2
  files unpacked; the memconfig server's firmware space; `sim.Outpost`
  takes upgrades and runs new images on trial as the board does; a
  Firmware section on the node page with progress and plain results),
  the fast clock (`openlcb/clock` codec, `internal/clock` server
  broadcasting time, date, year and rate and taking set, start, stop and
  query from the bus, kept in the store; clock triggers and conditions
  in automations; the Clock page and a readout in the status strip),
  backup and restore (`internal/backup`: a zip of the data directory
  with a database snapshot, restores staged and applied at the next
  start with the old files kept; a Restart button; the server restarts
  in place), the OpenLCB TCP link and server
  (`openlcb/transport/olcbtcp`: framing, a gateway allocating and
  defending aliases for TCP nodes on the CAN side, reconnecting client
  link and listener), the SocketCAN link (`openlcb/transport/socketcan`,
  Linux only, through a codec on the shared stream link), and the
  documentation site (VitePress over `docs/`, published to GitHub Pages
  by `.github/workflows/docs.yml`). Verified against the simulator: a
  firmware upgrade installed and one rolled back from the browser, the
  clock run at 12x, a backup downloaded and restored. Not yet against
  real hardware.
- Decided in Phase 6: the node resends a rejected datagram on its own
  only for the transport's temporary codes (0x2000, buffer unavailable,
  out of order); other temporary codes such as the firmware checksum
  go back to the protocol above, which knows what to resend. A restore
  is applied at start rather than live, so nothing is replaced while
  open. The fast clock is Interlock's own default fast clock; following
  another node's clock instead is a later option.
- Deferred from Phase 6: firmware upgrade over streams (the standard's
  other path; both boards use datagrams), following an external clock,
  fragmented OpenLCB TCP messages, SocketCAN on a machine to test it,
  and the documentation site's screenshots.
- Phase 7 begun (2026-10-17): `internal/decoder` reads JMRI's
  definitions from GitHub, the archive or a folder; Identify with each
  manufacturer's product ID rules and ranked matches; a model's
  definition resolved for the browser; CVs read and written by
  definition name with index writes planned and repeated ones skipped.
  The browser does the value math (`web/src/decoder/values.ts`, tested
  with Node's runner) and lays out the panes. The simulated command
  station can hold an ESU LokSound 5 with paged CVs (`-decoder esu`).
  Verified against the simulator: identified, the LokSound 5 definition
  fetched from GitHub, acceleration changed by name and read back.
- Phase 7 roster (2026-10-17): roster entries are locomotives with IDs
  of their own, so several may share an address (one train on the bus,
  state shared); each has decoders, one fitted, whose address and
  function labels swap in and out with it. Each decoder's CV values live
  in `<data>/cvs/<decoder>.yaml`, saved as they are read or written and
  backed up with the rest. Identify also reads the address and names the
  roster decoder on the track. `internal/jmri` reads JMRI roster files
  and zips; the import previews a plan (entries sharing an address and
  road number become one locomotive with several decoders) and carries
  it out. Verified with a real six-locomotive JMRI roster against the
  simulator.
- Phase 7 (2026-10-17): ESU's own function map (rows of conditions and
  outputs, logical functions and sounds, laid out by the server and
  edited a row at a time), changes not written yet kept with a roster
  decoder's values and kept through reads, and a definition's files
  fetched several at a time on first use (a cold LokSound 5 in about
  0.15 s instead of 0.75 s). Verified against the simulator.
- Still to do in Phase 7: reading on the main, which needs RailCom
  support in `simple-dcc`.
- Throttle apps (2026-10-18): `internal/withrottle` serves the WiThrottle
  protocol on port 12090 for Engine Driver, WiThrottle and WiTcontroller:
  the roster with function labels and momentary functions, driving
  through the train manager (one controller for every app; the train is
  given up when the last app releases it), turnouts, track power, the
  fast clock and the web interface's port. A watched app that goes quiet
  has its trains emergency stopped; one that reconnects with the same
  device ID replaces its old connection. Announced by mDNS
  (`_withrottle._tcp`). Verified with a scripted client against the
  simulator and with Engine Driver on a phone.
- Track power state (2026-10-18): simple-dcc says in its Consumer
  Identified replies for Emergency Off and Clear Emergency Off whether
  main track power is on, and announces them at boot (power off). The
  train manager asks with Identify Consumer when nodes arrive, follows
  each station's reply, and shows the power as unknown while none has
  said or two disagree; the simulator does the same as the firmware.
- Consists (2026-10-18): LCC consists in `trains.yaml`, the lead first,
  each member with its facing and whether it follows the lead's F0 and
  other functions. Making one up attaches the members to the lead's
  train node with Train Control's consist commands; a failed attach
  detaches the others again. The train manager keeps the members' state
  following the lead, refuses speed for a member, puts a made-up consist
  together again before its lead is next driven when the lead's node
  left the bus or Interlock restarted, and breaks one up only when the
  lead is stopped. The simulator passes the lead's commands on as the
  firmware does, up to four members.
- Phase 8 track plan (2026-10-27): `internal/trackplan` (directions,
  pieces from the directions a cell's track leaves by, plan validation,
  topology with open ends, block ends and links through turnout
  positions, `trackplan.yaml` with revisions), its API and server-sent
  event, and the Track page. Operating, the page lights blocks by their
  detectors, names the trains RailCom reports and sets turnouts with a
  click. Editing, it has drag drawing that forms turnouts and crossings,
  platforms, labels, block painting, blocks linked to their detector and
  reporter, stations, undo, and a guarded save. Verified against the
  simulator.
- Next: the rest of Phase 7, hardware verification against
  canbus-outpost and simple-dcc, and Phase 8's train tracking.

## References

- OpenLCB standards and technical notes: https://openlcb.org/openlcb-and-lcc-documents/
- OpenMRN (reference C++ implementation, checked out at `~/git/openmrn`)
- `~/git/simple-dcc` and `~/git/canbus-outpost` firmware and their CDI files
- DCC detector event format: openlcb/documents working note and PR 196
