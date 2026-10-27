# Connections

Interlock reaches the layout through links listed in `interlock.yaml`.
Every link joins the same hub: a frame that arrives on one link is
repeated to every other, so separate cables behave as one bus, and
Interlock's own node sits on that bus like any other. Restart Interlock
after editing the file (the Settings page has a button for it).

```yaml
links:
  - name: outpost
    type: serial
    device: /dev/cu.usbmodem1234   # a canbus-outpost or simple-dcc USB port
  - name: jmri
    type: tcp
    address: 192.168.1.10:12021    # any GridConnect TCP server
  - name: tcp-hub
    type: olcbtcp
    address: 192.168.1.20:12022    # an OpenLCB TCP hub
  - name: can0
    type: socketcan
    interface: can0                # a Linux CAN interface
```

Each CAN segment should reach Interlock through exactly one link. Two
links onto the same wire would echo every frame back and forth.

## Link types

**serial**: GridConnect text over a serial port. This is what
canbus-outpost and simple-dcc offer over USB, and what most USB to CAN
adapters for LCC do. `baud` may be set; USB devices ignore it. The
device is `/dev/cu.usbmodem…` on macOS and a port name like `COM3` on
Windows; the Settings page lists the serial ports it can see.

On Linux, name the device by its link under `/dev/serial/by-id/`. udev
makes it from the board's maker, product and serial number, so it stays
the same across restarts and replugs, while `/dev/ttyACM0`,
`/dev/ttyACM1` and so on are numbered in the order boards appear and
can swap. simple-dcc shows up as
`usb-simple-dcc_DCC_Command_Station_<serial>-if00` and canbus-outpost as
`usb-Raspberry_Pi_Pico_<serial>-if00`. The Settings page lists ports by
these names where they exist.

**tcp**: GridConnect over TCP, as JMRI's and OpenMRN's hubs serve it.
Interlock connects out to `address` and reconnects with backoff when the
connection drops.

**olcbtcp**: the OpenLCB TCP transfer protocol, which carries whole
messages with full node IDs rather than CAN frames. Interlock connects to
the hub at `address` and acts as a gateway: for every node on the TCP side
it allocates an alias on the CAN side, defends it, answers alias
enquiries for it and withdraws it when the connection ends, so the two
sides see one bus. Messages between two TCP nodes stay on the TCP side.

**socketcan**: a Linux CAN interface (`can0`, a USB adapter with a kernel
driver, or `vcan0` for testing) through the kernel's SocketCAN. Bring the
interface up first, for example `ip link set can0 up type can bitrate
125000`. On other operating systems this type reports that it is not
available.

## Servers

Interlock also serves two kinds of connection, under `hub:` in the file:

```yaml
hub:
  listen: ":12021"        # GridConnect TCP, for JMRI or another Interlock
  openlcb_tcp: ":12022"   # OpenLCB TCP, for nodes that speak it; "" is off
```

A tool that connects to either port appears on the Settings page as a
link and joins the bus like the others. The OpenLCB TCP server gives each
client its own gateway, as the `olcbtcp` link type does for an outgoing
connection.

Throttle apps such as Engine Driver connect to a third server, the
WiThrottle one on port 12090 (`withrottle.listen`); they drive trains
through Interlock rather than joining the bus. [Trains](./trains.md#throttle-apps)
has the details.

## Watching the links

The status strip shows a lamp per link, and the Settings page shows each
link's connection state, when it connected, how many frames went each
way, how many were dropped because the link could not keep up, and how
many times it reconnected. The Monitor page shows the traffic itself.
