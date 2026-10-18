# HTTP API

The web interface is the API's only built-in client, so everything it
does, you can do with `curl`. Requests and responses are JSON unless
noted; errors are `{"error": "..."}` with a 4xx or 5xx status. Node IDs
are dotted hex (`05.01.01.01.22.00`), event IDs likewise with eight
bytes, entity IDs read like `turnout.0a0000000042.servo_1`, train IDs are
DCC addresses (`1234`, `3` short, `3l` long).

## Live updates

```
GET /api/v1/events?topics=nodes,links,monitor,entities,automations,trains,clock
```

is a server-sent event stream; the default is every topic. Events:

| Event | Topic | Carries |
|---|---|---|
| `ready` | | `{"seq": N}`, the monitor's newest sequence number |
| `node` | nodes | a node that appeared, changed or went offline |
| `firmware` | nodes | a firmware upgrade's progress |
| `links` | links | every link's status |
| `frame` | monitor | one decoded frame |
| `entity` | entities | an entity that changed |
| `event_names` | entities | `{"node": id}` when a node's named events changed |
| `automation`, `trace` | automations | an automation's status; a run's trace as it moves |
| `train`, `layout`, `cv` | trains | a train; track power and emergency stop; a finished CV operation |
| `clock` | clock | the fast clock, on every change and fast minute |
| `lagged` | | the client fell behind and missed events |

## Controller

```
GET  /api/v1/health          version, operating system, node ID, alias, uptime, counts
GET  /api/v1/links           every link's status
GET  /api/v1/serial-ports    serial devices on this machine
GET  /api/v1/backup          a zip of the data directory (application/zip)
POST /api/v1/restore         multipart form, field "file": stage a backup and restart
POST /api/v1/restart         restart Interlock, rereading interlock.yaml
```

## Nodes

```
GET    /api/v1/nodes                         every node seen
POST   /api/v1/nodes/refresh                 ask every node to check in
GET    /api/v1/nodes/{id}
POST   /api/v1/nodes/{id}/refresh            identify the node again
DELETE /api/v1/nodes/{id}                    forget an offline node
GET    /api/v1/nodes/{id}/cdi                the configuration description, expanded; ?format=xml for the source
POST   /api/v1/nodes/{id}/cdi/refresh        read it from the node again
POST   /api/v1/nodes/{id}/config/read        {space, address, size} -> {values}
POST   /api/v1/nodes/{id}/config/write       {values} -> {written}
POST   /api/v1/nodes/{id}/config/reboot
POST   /api/v1/nodes/{id}/config/factory-reset   {confirm: node id}
POST   /api/v1/nodes/{id}/event-names/refresh
GET    /api/v1/event-names                   the named event table
GET    /api/v1/profiles                      node profiles, shipped and yours
```

Configuration values are keyed `space:address` and carried as strings in
the form the CDI gives them.

## Firmware

```
GET    /api/v1/firmware                      the latest upgrade of every node
GET    /api/v1/nodes/{id}/firmware           the node's latest upgrade
POST   /api/v1/nodes/{id}/firmware           multipart form, field "file" -> 202, the job
DELETE /api/v1/nodes/{id}/firmware           cancel the upgrade in progress
```

A job has a `phase` (checking, freezing, writing, unfreezing, restarting,
verifying, done, failed), `written` and `size` in bytes, a `message` in
plain words, and `from` and `to` software versions. See
[Nodes and firmware](./nodes.md).

## Entities

```
GET    /api/v1/entities                      every entity with its state
POST   /api/v1/entities                      make one by hand {kind, name, events}
POST   /api/v1/entities/refresh              ask the nodes for current states
GET    /api/v1/entities/kinds                kinds, their roles and states
GET    /api/v1/entities/{id}
PATCH  /api/v1/entities/{id}                 rename, confirm, hide, change events
DELETE /api/v1/entities/{id}
POST   /api/v1/entities/{id}/command         {command}
```

## Automations

```
GET    /api/v1/automations                   every automation with its status
POST   /api/v1/automations                   create from a document
GET    /api/v1/automations/{id}
PUT    /api/v1/automations/{id}              replace the document
PATCH  /api/v1/automations/{id}              {enabled}
DELETE /api/v1/automations/{id}
GET    /api/v1/automations/{id}/yaml         the document as YAML (text/yaml)
POST   /api/v1/automations/{id}/run          run it by hand; ?conditions=true to check them
POST   /api/v1/automations/{id}/stop         cancel its runs
GET    /api/v1/automations/{id}/traces
GET    /api/v1/automations/{id}/traces/{trace}
```

The document's fields are in [Automations](./automations.md).

## Trains

```
GET    /api/v1/trains                        {trains, layout}
POST   /api/v1/trains                        add to the roster {name, address, long, functions, momentary, ...}
POST   /api/v1/trains/search                 {address, long} -> what answered on the bus
GET    /api/v1/trains/layout                 {power_off, power_known, emergency_stop}
POST   /api/v1/trains/layout                 {power} or {emergency_stop}
GET    /api/v1/trains/{id}                   a roster ID, or an address ("1234", "3l")
PATCH  /api/v1/trains/{id}                   any roster field but the decoders
DELETE /api/v1/trains/{id}                   with its decoders' CV values
POST   /api/v1/trains/{id}/decoders          {name, file, family, model, identity, install} -> {train, decoder}
PATCH  /api/v1/trains/{id}/decoders/{dec}    {name, file, family, model, identity}
DELETE /api/v1/trains/{id}/decoders/{dec}    with its CV values
POST   /api/v1/trains/{id}/install           {decoder} fit one ("" for none); address and labels swap
GET    /api/v1/trains/{id}/decoders/{dec}/cvs      {cvs, imported, updated, changes, session, profiles}
PATCH  /api/v1/trains/{id}/decoders/{dec}/cvs      {cvs} save these values
PUT    /api/v1/trains/{id}/decoders/{dec}/cvs      {cvs} replace them all
PUT    /api/v1/trains/{id}/decoders/{dec}/changes  {changes} keep the changes not written yet
POST   /api/v1/trains/{id}/decoders/{dec}/profiles {name, replace} keep a copy of its values -> {name, kept, cvs}
DELETE /api/v1/trains/{id}/decoders/{dec}/profiles/{name}  forget one
POST   /api/v1/trains/{id}/decoders/{dec}/check    is the decoder on the programming track this one?
POST   /api/v1/trains/{id}/decoders/{dec}/session  begin the session though the check did not match
DELETE /api/v1/trains/{id}/decoders/{dec}/session  end the session on it
POST   /api/v1/trains/import                 multipart: file (JMRI roster files or a zip), plan
POST   /api/v1/trains/{id}/acquire           take control
POST   /api/v1/trains/{id}/release
POST   /api/v1/trains/{id}/speed             {speed, direction}
POST   /api/v1/trains/{id}/estop
POST   /api/v1/trains/{id}/function          {function, on}
POST   /api/v1/trains/{id}/refresh           read speed and functions back
POST   /api/v1/trains/{id}/cv                write a CV on the main {cv, value}
GET    /api/v1/programming                   {track, history}
POST   /api/v1/programming/find              look for the programming track
POST   /api/v1/programming/read              {cv}
POST   /api/v1/programming/write             {cv, value}
```

CV operations answer 200 with `ok` and, when it failed, `error` in plain
words; 400 is for bad input. An import without `plan` changes nothing and
answers with the locomotives found and a suggested plan, one step per
locomotive (`new`, `decoder` with `into`, or `skip`); sending the files
again with a plan carries it out. Changes are kept by CV as the bits
they set: `{"29": {"value": 32, "bits": 32}}` sets bit 5 of CV 29. A
change is dropped once a value read from or written to the decoder
holds it. `imported` holds values from an imported roster that the
decoder has not been read for since; saving a CV's value moves it to
`cvs`, and replacing them all clears `imported`. `session`, when there
is one, is `{started, last, check, read, written}`: the CVs read from and
written to the decoder through `/programming/decoder-cv` since it
began. It ends two hours after `last`, when `/programming/identify`
finds a decoder, when another decoder is checked, when it is deleted,
or when Interlock restarts.

A decoder profile copies `cvs` and `imported` under a name; keeping one
by a name already kept answers 409 unless `replace` is set. Loading a
decoder profile happens in the browser, as changes. Decoder profiles are
not the node profiles of `/api/v1/profiles`.

`check` reads the decoder on the programming track and compares it with
the roster decoder: `{match, differences, told, alike, others, read,
identity, manufacturer, model, address, session}`. Each difference is
`{what, cv, roster, track}`, `what` being `manufacturer`, `product`,
`address` or `cv`; `alike` lists roster decoders of the same model at
the same address that nothing saved tells apart, `others` the ones it
could be instead, and `told` the CVs read to tell them apart. A match
begins the session with `check: "matched"` and the values in `read`
saved as read; otherwise nothing changes, and `POST .../session` begins
it with `check: "accepted"`. Reads and writes for a roster decoder on
the programming track answer 409 until its session has a check. See
[Trains](./trains.md) and [The roster](./roster.md).

## Decoder definitions

```
GET    /api/v1/decoders                      where the definitions come from {source, ref, path, archive}
GET    /api/v1/decoders/index                every manufacturer, family and model
GET    /api/v1/decoders/definition           ?file=&model= one model's settings and pages
POST   /api/v1/programming/identify          {identity, manufacturer, candidates, address, roster}
POST   /api/v1/programming/decoder-cv        {file, model, cv, write, value, train, locomotive, decoder} -> {cv, value, ok, error}
```

The index and definitions are large and sent gzipped when the client
accepts it. `decoder-cv` takes a CV by the name the definition uses
(`29`, `16.0.259`, `T2CV.1.05`) and does the index writes it needs; with
`train` it writes on the main; with a roster `locomotive` and one of its
`decoder`s, the value is saved for that decoder. It refuses CVs the
definition marks read-only, write-only or for information only.
`identify` also reads the decoder's address and lists the roster
decoders it could be; when the first checks out, its session begins and
it is marked `checked`. An ESU decoder's function map comes as an
`esu_map` node: its rows and blocks of items, from which each item's CV
and bits follow (see `internal/decoder/esumap.go`); its CVs are in the
definition's list. See [Decoder settings](./decoders.md).

## Clock

```
GET    /api/v1/clock                         time, date, weekday, rate, running, updated
POST   /api/v1/clock                         {running, rate, time, date}, any subset
```

See [The fast clock](./clock.md).

## Monitor

```
GET    /api/v1/monitor?since=N&limit=M       decoded frames after sequence N (limit 0 for all)
POST   /api/v1/monitor/send                  {frame: ":X195B4123N0102030405060708;"} send a raw frame
```
