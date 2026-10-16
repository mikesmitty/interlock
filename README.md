# Interlock

An LCC (OpenLCB) layout controller for model railroads, built to be
friendlier than JMRI: nodes show up by name, configuration is a form instead
of a memory map, and automations are built in the browser from triggers,
conditions and actions, the way Home Assistant does it.

Interlock is a single Go binary with an embedded web interface. It speaks
LCC directly over GridConnect (USB serial or TCP), acts as a hub between
links, and is itself a node on the bus.

**Status:** early. The server skeleton runs; the bus layer is next. See
[docs/design.md](docs/design.md) for the architecture and roadmap.

## Building

Requires Go (see `go.mod`) and Node 22 or newer.

```bash
make build        # builds the web interface and bin/interlock
bin/interlock     # serves on :8080 using ~/.config/interlock (or $INTERLOCK_DATA)
```

`bin/interlock -data /path/to/dir` keeps everything in one directory:
`interlock.yaml` (written with defaults on first start), the sqlite
database and, later, automations and profiles.

## Developing

```bash
make test         # Go tests
make lint         # golangci-lint and the TypeScript type check
make run          # Go server from source on :8080
make dev          # Vite dev server with hot reload, proxying /api to :8080
```

## License

GPL-3.0; see [LICENSE](LICENSE).
