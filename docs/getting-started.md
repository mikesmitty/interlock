# Getting started

Interlock is one program with the web interface built in. It runs on a
Raspberry Pi or a laptop next to the layout, speaks LCC to the nodes
through whatever connects them to the computer, and is itself a node on
the bus.

## Install

Download the build for your machine from the
[releases](https://github.com/mikesmitty/interlock/releases): Linux on
amd64, arm64 or arm (the Raspberry Pi family) and macOS. Put the file
somewhere on your path and make it executable.

To build it yourself you need Go (the version in `go.mod`) and Node 22.22 or
newer:

```bash
git clone https://github.com/mikesmitty/interlock
cd interlock
make build        # the web interface, then bin/interlock
```

## First start

```bash
interlock -data ~/interlock
```

The data directory holds everything Interlock keeps: `interlock.yaml`
(written with defaults and comments on first start), the database, and
the files the web interface edits: `entities.yaml`, `automations.yaml`,
`trains.yaml` and your `profiles/`. Without `-data` it uses
`$INTERLOCK_DATA`, else an `interlock` folder under your user
configuration directory.

Open <http://localhost:8080>. With no links yet the Nodes page shows only
Interlock's own node and the Settings page lists the serial ports it can
see.

## Connect the layout

Add a link to `interlock.yaml` and restart (the Settings page has a
Restart button):

```yaml
links:
  - name: outpost
    type: serial
    device: /dev/cu.usbmodem1234   # a canbus-outpost or simple-dcc USB port
```

[Connections](./connections.md) describes the link types: serial and
TCP GridConnect, OpenLCB TCP and SocketCAN. Interlock also serves a
GridConnect TCP port, so JMRI can connect to it during a transition.

When the link comes up the nodes on the bus appear within a few seconds:
Interlock asks every node to identify itself, reads what protocols it
speaks and its name, and reads its configuration description once per
software version.

## Try it without hardware

```bash
bin/interlock -data ./data &
go run ./cmd/simnode                                  # a simulated canbus-outpost
go run ./cmd/simnode -cdi cs -id 0A.00.00.00.00.43    # a simulated command station
```

The simulated outpost moves its servos when told to and walks a
locomotive around its four tracks, so the Entities page has something to
show; the simulated command station makes train nodes for the Throttle
page and has a decoder on its programming track. Both take firmware
upgrades and configuration writes like the real boards.

## Where to go next

- [Nodes and firmware](./nodes.md): configuring a node and installing
  new firmware on it.
- [Entities](./entities.md): turnouts, blocks, reporters and sensors.
- [Automations](./automations.md): when, only if, then.
- [Trains](./trains.md): the roster, the throttle, CV programming.
- [The fast clock](./clock.md) and [Backup and restore](./backup.md).

## Running it as a service

On Linux, a systemd unit keeps Interlock running and restarts it after a
restore:

```ini
[Unit]
Description=Interlock layout controller
After=network-online.target

[Service]
ExecStart=/usr/local/bin/interlock -data /var/lib/interlock
Restart=on-failure
User=interlock

[Install]
WantedBy=multi-user.target
```

Interlock restarts itself in place when asked to (after a restore, or
from the Settings page), so the unit's `Restart=` setting only matters if
it crashes.
