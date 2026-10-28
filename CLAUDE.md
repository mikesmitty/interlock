# interlock

LCC (OpenLCB) layout controller: a Go server with an embedded web interface
that discovers nodes, configures them through their CDI, models them as
entities and runs Home Assistant style automations. The architecture and
phased roadmap are in `docs/design.md`; read it before structural changes.

## Build

Go (version in `go.mod`; the toolchain downloads it if needed) and Node 22.22+ (React Router needs it; CI uses 24).

```bash
make build      # web interface, then Go binary -> bin/interlock
make go         # Go binary only (embeds whatever is in web/dist)
make web        # build the web interface into web/dist
make test       # go test -race ./... and the web tests (node --test)
make lint       # golangci-lint + TypeScript type check + Prettier check
make fmt        # golangci-lint fmt (gofmt + goimports) + Prettier on web/ and docs/
make run        # run the server from source
make dev        # Vite dev server on :5173, proxying /api to :8080
make docs       # build the documentation site (VitePress) from docs/
make docs-dev   # serve it with live reload
```

`golangci-lint` v2 is expected on PATH (`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`).

Run `bin/interlock -data <dir>`; the data directory holds `interlock.yaml`
(created with defaults on first start), `interlock.db`, `entities.yaml`,
`automations.yaml`, `trains.yaml`, `trackplan.yaml` and its background image in `backgrounds/`, decoder CV values in `cvs/` and user `profiles/`. Default: `$INTERLOCK_DATA`, else `interlock` under the user
config directory. A staged restore waits in `restore.pending/` and is
applied at the next start; the replaced files go to `before-restore-*/`.

## Layout

- `cmd/interlock/` — main: flags, config, wiring, graceful shutdown.
- `cmd/simnode/` — simulated node that joins the hub port; use it to
  develop and test without hardware. As an outpost it moves its servos on
  command and walks a locomotive around its tracks (`-pace`).
- `cmd/simlayout/` — a simulated layout for an Interlock of its own: a
  command station, outposts and trains running on that Interlock's track
  plan (`internal/layoutsim`). Seeds its test layout into an empty plan.
- `openlcb/` — public OpenLCB protocol packages. **Must not import
  `internal/`**; other Go programs should be able to use them.
  - `openlcb` core types (NodeID, EventID, MTI, Message, SNIP, PIP, errors)
  - `openlcb/can` identifiers, GridConnect, alias generator and cache,
    frame assembly; pure functions, no goroutines
  - `openlcb/transport` Link interface, a stream link with a pluggable
    codec (GridConnect by default) over serial and TCP with reconnection,
    the TCP listener; `transport/olcbtcp` the OpenLCB TCP framing, a
    gateway that gives TCP nodes aliases on the CAN side, a reconnecting
    link and a listener; `transport/socketcan` the Linux CAN interface
    (frame codec everywhere, socket code behind a build tag)
  - `openlcb/hub` frame router between links and subscribers
  - `openlcb/node` the local node: one goroutine owns all protocol state;
    client operations are serialized onto it and observers run on it
  - `openlcb/memconfig` memory configuration datagrams: codec, client
    (one request in flight per node; reads always wait for the reply
    datagram, writes only when the acknowledgment says so), server
  - `openlcb/cdi` CDI parser and address resolver; `Element` is the XML
    tree, `Variables()` the flat address list, `Layout()` the expanded
    JSON form; values are strings on the wire and `Encode`/`Decode`
    translate them
  - `openlcb/detector` the DCC detector event protocol: a 16-bit range
    per detector whose low bytes report locomotives entering and leaving
  - `openlcb/traction` Train Control: command and reply encoding, the
    Train Search event, DCC train node IDs; pure functions
  - `openlcb/clock` Broadcast Time events: time, date, year, rate,
    set, start, stop, query; pure functions
  - `openlcb/sim` simulated nodes built from the embedded copies of the
    canbus-outpost and simple-dcc CDI files (keep them in sync with the
    firmware repos); `Outpost` adds the firmware's behavior (servos,
    detectors, Identify replies) over the plain node; `Station` behaves
    like simple-dcc (train search and train nodes, a programming track
    with a decoder on it, the emergency events, consists that pass
    the lead's commands on). The outpost also takes
    firmware upgrades (`sim.FirmwareImage` makes an image; a new image
    runs on trial and confirms itself or goes back)
- `internal/config` — `interlock.yaml` loading and validation.
- `internal/store` — sqlite (modernc, no cgo). Schema changes are new files
  in `internal/store/migrations/NNNN_name.sql`; never edit a released one.
- `internal/identity` — resolves Interlock's own node ID (config, else the
  saved generated 0A-prefix ID).
- `internal/registry` — nodes seen on the bus, discovery rounds,
  identification (PIP then SNIP), CDI reads cached by software version,
  online tracking, persistence through the store.
- `internal/nodeconfig` — reads and writes configuration values keyed by
  `space:address` through the node's layout; validates a whole batch
  before writing any of it.
- `internal/eventtable` — the named event table: every eventid variable
  of every node with its value, read from the segments that hold events
  (so neighboring descriptions and enables are available too), cached in
  the store, re-read when a node returns or its configuration is written.
- `internal/profile` — profiles are YAML data that turn a node's groups
  into entities; shipped ones live in `profiles/` (canbus-outpost,
  simple-dcc, generic guesser), user ones in `<data>/profiles/`.
- `internal/entity` — kinds (turnout, block, reporter, sensor, switch,
  button), the manager that builds entities from profiles over the event
  table, follows Event Reports and Identified replies, sends commands,
  and keeps overrides and hand-built entities in `<data>/entities.yaml`.
- `internal/trackplan` — the track plan in `<data>/trackplan.yaml`, to
  scale in millimeters: points, track between two of them (straight or
  turning by an angle), junctions (turnout, wye, crossing, crossover,
  double crossover; their ends in a fixed order and fixed routes),
  platforms, stop markers (a spot on a track, for trains running one way,
  up to a length), labels, layers, blocks linked to their block and
  reporter entities, stations. `Topology` works out open ends, kinks, each
  block's ends and length, and the links between block ends with their
  length and the junction positions on the way, which is what
  scheduling will route over. `ImportJMRI` reads a JMRI Layout Editor
  panel as RailModeller Pro exports one. Plans on the old grid of cells
  (`piece.go`, `legacy.go`) are converted on open. Saves name the
  revision they were based on. A background image to trace over is kept
  in `<data>/backgrounds/` under a name made from its contents; saves
  delete the ones not named. The browser computes the same geometry in
  `web/src/trackgeom.ts`; keep them in step.
- `internal/tracking` — train tracking: follows each decoder the
  reporters see through the plan as the train its locomotive is made up
  into (a formation; a locomotive in no train runs on its own, keyed by
  address), places the train behind or ahead of it from the train's
  length and the locomotive's place in it once it runs from one block
  into the next, lays it out again around the decoder when the roster
  changes the train, moves it on between reports at its decoder's
  speed curve and momentum (`train/motion.go`, CVs from the CV store)
  times a top speed measured between entries, or the speeds measured
  at each step held over a run (`motion.go`), never into a detected
  block unseen, and keeps places over a restart in the store. One
  goroutine owns the state (`layout.go`); handlers only queue. Its
  tests run the simulated layout and check against true positions.
  `Forget` drops a train taken off the layout.
- `internal/interlocking` — routes: each topology link taken one way
  (`route.go`, ID `from~to`, its legs from `Network.Way`), set when
  nothing is in the way, its turnouts moved and locked (the entity
  manager's `Guard` refuses commands that would move them, from any
  client), released leg by leg behind the train by tracking's stretch
  with a clearance (`Network.Fouls` says which legs share track), and
  kept over a restart. One goroutine owns the state (`state.go`).
- `internal/driving` — drives trains: a drive finds the way to a block
  over the interlocking's routes (`course.go`), sets them, and each tick
  sets the train's speed from tracking's estimate (`Tracker.Estimate`)
  to brake it to a stand at the block's stop marker (`control.go`, by
  the train's `tracking.Motion`); a measuring run (`measure.go`) runs a
  train around as the turnouts lie until tracking has measured its
  speed at each step asked. One goroutine per drive; a throttle's speed
  takes the train over. Its tests drive the simulated layout in
  simulated time with the real interlocking.
- `internal/layoutsim` — the simulated layout: trains as stretches
  along the plan's `Network` (`trackplan/network.go`, legs through track
  and junction routes; `trackplan/stretch.go`, the track a train covers)
  moved at their decoders' speed with momentum,
  turnouts from simulated servos, detectors with RailCom's timing (entry
  after a few packets, exit a second late, the decoder only), and the
  test layout. The simulated outposts' node IDs are 0x10 apart because
  each takes the four after its own for its reporter ranges.
- `internal/replace` — moves what names one node over to another, for a
  replaced board or a new node ID: entity IDs (`entity.MoveID`), events
  matched by their place in the two boards' configuration (same model
  only) and node IDs. The track plan, the automation engine and the
  entity manager each rewrite their own references through the
  `Rewrite` method and the types in `internal/refs`; a new kind of
  stored reference needs one too.
- `internal/automation` — automations as declarative documents (JSON in
  the API, `<data>/automations.yaml` on disk): triggers, conditions and
  actions with Home Assistant semantics, run modes, and a trace per run
  stored in sqlite. `docs/automations.md` lists the fields. The engine's
  handlers never block; the entity manager calls them from the node's
  goroutine.
- `internal/train` — the throttle side: the roster in `<data>/trains.yaml`,
  finding train nodes by Train Search, controller assign, speed and
  function commands, the layout's emergency events, consists (`consist.go`:
  made up on the command station by Train Control's consist commands, the
  members' state following the lead), cars and formations
  (`formation.go`: trains made up of locomotives and cars from the front,
  each vehicle in one at most; the web interface calls formations
  trains, since here a train is a DCC address). Roster entries are
  locomotives with IDs of their own and may share an address; the state
  on the bus belongs to the address (`train` in the manager) and every
  entry there shows it. Each entry has decoders, one fitted, whose
  address and labels swap with it; `CVStore` keeps each decoder's CV
  values (imported ones apart until a read confirms them), the
  settings page's changes not written yet and decoder profiles (named
  copies of its values to load again as changes; not the node profiles
  of `internal/profile`) in `<data>/cvs/`, and in
  memory each decoder's session (what was read and written since work on
  it began; Identify ends them all). Work on the programming track needs
  a session begun by `CheckDecoder` (`check.go`: model, address, saved
  values, and CVs that tell alike decoders apart). The JMRI
  import is planned and applied here.
- `internal/jmri` — reads JMRI roster files (roster.xml, the locomotive
  files, zips of them) into imports for the roster.
- `internal/programmer` — CV reads and writes: service mode through space
  0xF8 of the programming track's node, POM through a train node's.
- `internal/decoder` — JMRI's DCC decoder definitions (GPL-2.0-only, so
  fetched, never bundled): sources (GitHub file by file, the repository
  archive, a folder or zip), the index, Identify and matching, the
  definition of one model (variables walked as JMRI walks them, value
  types reduced to CV parts and masks, panes merged and pruned, ESU's
  function map laid out in `esumap.go`), CV name
  plans (index CVs, TCS four-CV access) and a session that skips repeated
  index writes. `docs/decoders.md` is the user guide; the browser does
  the value math in `web/src/decoder/values.ts`.
- `internal/firmware` — firmware upgrades by the Firmware Upgrade
  standard: Freeze, 64 byte writes to space 0xEF with retries, Unfreeze,
  then a watch for the restart and for a board that goes back to its old
  version. UF2 files are unpacked. One job at a time, progress published.
- `internal/withrottle` — the WiThrottle server throttle apps (Engine
  Driver, WiThrottle, WiTcontroller) connect to: roster, driving through
  the train manager, turnouts, power, the fast clock; announced by mDNS.
  One goroutine per app owns its state; the managers' changes reach it
  queued.
- `internal/clock` — the fast clock: a Broadcast Time server on
  Interlock's node, state in the store, minute callbacks for automations.
- `internal/backup` — backups as one zip with a database snapshot;
  restores staged in the data directory and applied at the next start.
- `internal/yamlstrict` — unknown-field check for YAML documents with
  custom unmarshalers.
- `internal/atomicfile` — write-then-rename for the YAML files the UI
  edits; on Windows the rename retries while another program holds the
  file open. Use it for any file Interlock rewrites.
- `internal/monitor` — rolling log of frames with decoding for display.
- `internal/api` — HTTP API under `/api/v1`; JSON errors for 404 and 405;
  server-sent events at `/api/v1/events`.
- `internal/server` — HTTP server, SPA handler, request logging.
- `web/` — React + TypeScript + Vite frontend; `web/embed.go` embeds
  `web/dist` (only `.gitkeep` is committed; an unbuilt UI serves a notice).
  Visual language is in `docs/design-language.md`; follow it for new pages.
  Fonts are bundled so the UI works offline. Pure logic gets
  `*.test.ts` tests run by Node's own runner (`npm test`); such modules
  use only type imports so Node can run them without a build.
- `docs/` — the documentation, also built into a site with VitePress
  (`docs/.vitepress/config.mts` holds the sidebar; add new pages there)
  and published to GitHub Pages by `.github/workflows/docs.yml`.

## Conventions

- Conventional commits (`feat(scope):`, `fix(scope):`, ...). Releases are
  cut by release-please; never bump versions by hand
  (`internal/version/version.go` carries the marker). The release calls
  `.github/workflows/binaries.yml`, which builds Linux, macOS and Windows
  and signs the macOS and Windows builds once their credentials exist;
  `docs/releasing.md` lists them.
- US English spelling everywhere; `misspell` enforces it in Go.
- Follow `~/.claude/languages/go.md`: `log/slog`, `context.Context` on
  anything that blocks, wrapped errors, table-driven tests, small
  interfaces.
- Protocol behavior comes from the standards and from the real devices:
  `~/git/simple-dcc` (command station) and `~/git/canbus-outpost`
  (switch machines, RailCom), with `~/git/openmrn` as the C++ reference.
  Check what the firmware actually does before assuming.
- Keep the API the only path between frontend and server; no server side
  rendering.
