# interlock

LCC (OpenLCB) layout controller: a Go server with an embedded web interface
that discovers nodes, configures them through their CDI, models them as
entities and runs Home Assistant style automations. The architecture and
phased roadmap are in `docs/design.md`; read it before structural changes.

## Build

Go (version in `go.mod`; the toolchain downloads it if needed) and Node 22+.

```bash
make build      # web interface, then Go binary -> bin/interlock
make go         # Go binary only (embeds whatever is in web/dist)
make web        # build the web interface into web/dist
make test       # go test -race ./...
make lint       # golangci-lint + TypeScript type check
make fmt        # golangci-lint fmt (gofmt + goimports)
make run        # run the server from source
make dev        # Vite dev server on :5173, proxying /api to :8080
```

`golangci-lint` v2 is expected on PATH (`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`).

Run `bin/interlock -data <dir>`; the data directory holds `interlock.yaml`
(created with defaults on first start), `interlock.db` and later automations
and profiles. Default: `$INTERLOCK_DATA`, else `interlock` under the user
config directory.

## Layout

- `cmd/interlock/` — main: flags, config, wiring, graceful shutdown.
- `openlcb/` — public OpenLCB protocol packages. **Must not import
  `internal/`**; other Go programs should be able to use them.
- `internal/config` — `interlock.yaml` loading and validation.
- `internal/store` — sqlite (modernc, no cgo). Schema changes are new files
  in `internal/store/migrations/NNNN_name.sql`; never edit a released one.
- `internal/identity` — resolves Interlock's own node ID (config, else the
  saved generated 0A-prefix ID).
- `internal/api` — HTTP API under `/api/v1`; JSON errors for 404 and 405.
- `internal/server` — HTTP server, SPA handler, request logging.
- `web/` — React + TypeScript + Vite frontend; `web/embed.go` embeds
  `web/dist` (only `.gitkeep` is committed; an unbuilt UI serves a notice).

## Conventions

- Conventional commits (`feat(scope):`, `fix(scope):`, ...). Releases are
  cut by release-please; never bump versions by hand
  (`internal/version/version.go` carries the marker).
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
