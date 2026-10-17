# Trains

Interlock drives locomotives the way an LCC throttle does: it asks the
command station for a train node with the Train Search protocol, takes the
controller role, and sends Train Control commands for speed, direction and
functions. The Throttle page is the cab; `trains.yaml` in the data
directory is the roster; automations can drive trains with the `train`
action; and the Programming page reads and writes CVs.

## The roster

A roster entry gives a locomotive a name and remembers its function
labels. The web interface writes the file; it can also be edited by hand
(restart Interlock afterwards):

```yaml
trains:
  - id: "4014"
    name: Big Boy
    address: 4014
    long: true
    functions:
      0: Headlight
      1: Bell
      2: Whistle
    momentary:
      - 2
```

| Field | Meaning |
|---|---|
| `id` | The address as text: `"1234"`, `"3"`, or `"3l"` for a long address of 127 or less. Filled in from the address. |
| `name` | What the throttle shows. Defaults to the address. |
| `address` | DCC address, 1 to 10239. |
| `long` | Address kind. Addresses above 127 are always long; below that, short unless set. |
| `functions` | Labels for the function keys, by number. |
| `momentary` | Functions that act only while held, such as a horn. |

A locomotive need not be in the roster to be driven: the throttle page
accepts any address, and an automation names trains by address. Such a
train shows up in the list marked as not in the roster, and can be added
with its state kept.

## Driving

The first command to a train does three things: it sends a Train Search
event for the address with the allocate flag, so a command station that
has no node for the locomotive yet creates one; it sends Controller Assign
to the node that answered; and it reads the train's speed and functions
back so the cab shows what the train is doing. After that each command is
one Train Control message. Release gives the controller role up and keeps
the last known state.

Speed runs from 0 to 126, which DCC command stations map to the 128 speed
steps one for one. An emergency stop is its own command; the train stays
stopped until a speed is set. If another throttle takes the train, the
train tells Interlock, which lets it go and shows who has it; the next
command takes it back.

The Throttle page also carries the layout's emergency controls. "Stop all
trains" produces the well-known Emergency Stop event and "Power off" the
Emergency Off event; command stations such as simple-dcc answer them by
stopping every locomotive or cutting track power. Interlock watches for
the same events from anyone else, so a stop pressed on a hardware
throttle shows here too.

## In automations

The `train` action drives a locomotive by address:

```yaml
actions:
  - type: train
    address: 1234
    command: speed
    speed: 30
    direction: forward
  - type: train
    address: 3
    address_type: short
    command: function
    function: 0
    state: on
```

| Field | Meaning |
|---|---|
| `address`, `address_type` | Which locomotive; `address_type` is `long` or `short` and only matters up to 127. |
| `command` | `speed`, `stop`, `estop` or `function`. |
| `speed`, `direction` | For `speed`: 0 to 126, and `forward` or `reverse`; leave the direction out to keep the current one. |
| `function`, `state` | For `function`: F0 to F68 and `on` or `off`. |

Together with the locomotive trigger this is enough for "stop the train
that just entered the yard" or "sound the horn at the crossing".

## Programming

Service mode, on the programming track, reads and writes one CV at a time
through memory space 0xF8 of the command station that produces the "is
programming track" event; CV n is at address n-1. The command station
acknowledges with reply pending and answers once the decoder has, which
takes a few seconds for a read. The DCC error codes come back in plain
words: no decoder answered, the decoder did not confirm, short circuit,
no programming track.

Programming on the main writes a CV through space 0xF8 of the train's
node, which the command station turns into a POM packet for that address.
Reading on the main needs RailCom and waits for the firmware.

[Decoder settings](./decoders.md) covers changing settings by name from
the decoder's definition rather than by CV number.

## Without hardware

`go run ./cmd/simnode -cdi cs -id 0A.00.00.00.00.43` simulates a command
station: it creates train nodes when asked, drives them, and has a decoder
with address 1234 on its programming track (`-address` changes it). Run
it next to the simulated outpost to see a train trigger an automation.
