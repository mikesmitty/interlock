# Getting started

Interlock is one program with the web interface built in. It runs on a
Raspberry Pi or a laptop next to the layout, speaks LCC to the nodes
through whatever connects them to the computer, and is itself a node on
the bus.

## Install

Download the build for your machine from the
[releases](https://github.com/mikesmitty/interlock/releases): Linux on
amd64, arm64 or arm (the Raspberry Pi family), macOS on Apple silicon or
Intel, and Windows on x64. On Linux and macOS, unpack the archive and put
`interlock` somewhere on your path. On Windows, unzip it and run
`interlock.exe` from a terminal.

If macOS says it cannot check `interlock` for malicious software, the
build you have was not notarized by Apple and the browser marked it as
downloaded. Clear that mark and run it again:

```bash
xattr -d com.apple.quarantine interlock
```

Downloading with `curl` instead of a browser leaves no mark to clear. If
Windows shows "Windows protected your PC", the build was not signed:
choose **More info**, then **Run anyway**.

To build it yourself you need Go (the version in `go.mod`) and Node 22.22 or
newer:

```bash
git clone https://github.com/mikesmitty/interlock
cd interlock
make build        # the web interface, then bin/interlock
```

On Windows, `make` comes with Git Bash or MSYS2. Without it, run the two
steps by hand:

```powershell
npm --prefix web ci
npm --prefix web run build
go build -o bin\interlock.exe ./cmd/interlock
```

## First start

```bash
interlock -data ~/interlock
```

On Windows, `interlock.exe -data $HOME\interlock` does the same in
PowerShell.

The data directory holds everything Interlock keeps: `interlock.yaml`
(written with defaults and comments on first start), the database, and
the files the web interface edits: `entities.yaml`, `automations.yaml`,
`trains.yaml` and your `profiles/`. Without `-data` it uses
`$INTERLOCK_DATA`, else an `interlock` folder under your user
configuration directory: `~/.config` on Linux, `~/Library/Application
Support` on macOS and `%AppData%` on Windows.

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

The device is the board's serial port: `/dev/cu.usbmodem…` on macOS, a
name like `COM3` on Windows, and on Linux the board's name under
`/dev/serial/by-id/`, such as
`/dev/serial/by-id/usb-simple-dcc_DCC_Command_Station_E6625C05E7A1B42F-if00`.
Use that name rather than `/dev/ttyACM0`: the numbers follow the order
boards appear in and can change when one is plugged in again, while the
by-id name stays with the board. The Settings page lists the ports it
can see, on Linux by their by-id names.

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
- [Trains](./trains.md): the throttle, CV programming.
- [The roster](./roster.md): locomotives, their decoders, importing a
  JMRI roster.
- [Decoder settings](./decoders.md): a decoder's settings by name.
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

On Windows, a Task Scheduler task that runs `interlock.exe` at logon does
the same job.
