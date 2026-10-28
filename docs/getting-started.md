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
`trains.yaml`, `trackplan.yaml` and your `profiles/`. Without `-data` it uses
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

### A simulated layout

`cmd/simlayout` simulates a whole layout for trying the track plan and
trains together. Give it an Interlock of its own, with no links to real
boards:

```bash
bin/interlock -data ./simdata &
go run ./cmd/simlayout -api http://127.0.0.1:8080 -state ./simdata/simlayout.json
```

It joins the hub as a command station and four outposts (node IDs
0A.00.00.00.01.00, then 0A.00.00.00.01.10, .20, .30 and .40). If the
plan is empty, it saves its test layout as the plan. It adds its trains
to the roster with their lengths, and fits each with a decoder whose
CVs are on record, so [train tracking](./tracking.md) can follow them. The test layout is a double-track loop with two stations,
Park Av and Main St. A siding at Main St lets one train pass another, and
a crossover east of Park Av turns trains back. Its nine blocks and three
turnouts are already linked to the simulated outposts.

Its trains run along the plan at the speed their decoders are set to,
with the decoders' momentum (CVs 3 and 4) and speed curve (CVs 2, 5
and 6). The outposts' servos set the turnouts, taking their throw time to
move. The detectors see the trains the way canbus-outpost's RailCom
detectors do:

- They see only the car with the decoder, not the rest of the train.
- A train entering a block is reported within a few DCC packets.
- A train leaving is reported a second after the decoder was last seen.

A train stops where the track ends and where it would run into another
train. At a junction set against it, or still moving, it waits and goes
on once the junction is set its way. With `-state`, the trains keep their places when
the simulator restarts. `-trains` takes a YAML list of other trains: each
has a name, an address, a length, the distance from its leading end to
its decoder, a top speed in millimeters a second, and the block it
starts in.

The simulator follows the plan as it is saved, so blocks and turnouts
drawn in the editor work as soon as they're linked to a simulated
outpost's tracks and servos.

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
