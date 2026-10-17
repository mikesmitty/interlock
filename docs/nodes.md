# Nodes and firmware

The Nodes page lists every node Interlock has seen on the bus: its name
(the one you gave it, else its model), maker and model, node ID and
alias, with a lamp for whether it is online now. Nodes that went offline
stay in the list, so a board that is unplugged is still there to look at;
"Forget" removes one for good.

Interlock finds nodes three ways: they announce themselves when they
start, they answer the check-in Interlock sends every so often
(`discovery.interval` in `interlock.yaml`), and anything that sends a
message is noted. A node's page shows what it said about itself, the
entities built from it, its configuration, its firmware and the recent
traffic to and from it.

## Configuration

A node that offers a configuration description (CDI) gets a form. The
form follows the description: segments become sections, replicated
groups become tabs, event IDs get a field that knows what an event ID
looks like, and numbers get their ranges and choices. Interlock reads the
description once per software version and keeps it in the database.

The form reads the node's current values when it opens. Change what you
like and save; only the values you changed are written, and the node is
told the update is complete so it can apply them. A read-only field is
shown as such. "Read values again" discards your edits and reads the
node afresh.

Two buttons at the bottom talk to the node directly: "Restart node" asks
it to reboot, and "Factory reset" asks it to return to its defaults,
which it confirms with the node ID so a stray click cannot do it.

Events a node's configuration names feed the named event table, which is
what entities are built from; see [Entities](./entities.md).

## Firmware

A node that offers firmware upgrades (its protocols list "Firmware
Upgrade") has a Firmware section on its page showing the software
version it runs. Choose a firmware file built for the board (`.bin`, or
the `.uf2` the Pico tools produce, which Interlock unpacks) and install
it. Interlock then:

1. asks the node to prepare (the Freeze command of the Firmware Upgrade
   standard);
2. sends the image 64 bytes at a time, in order, retrying a write the
   node asks to see again;
3. asks the node to check what it received (Unfreeze);
4. waits for the node to restart and reads the version it runs now;
5. watches it for a while longer, since a board that tries new firmware
   before keeping it goes back to the old version if the new one does
   not come up.

Progress shows on the page, and the result stays there: the version
installed, or why it failed in plain words (the file was not for that
board, the node did not come back, the new firmware did not confirm
itself and the board went back). One upgrade runs at a time, and a
running one can be canceled while the image is being sent; the node then
keeps the firmware it had.

canbus-outpost takes upgrades this way: it writes the image to the flash
partition it is not running from, starts it on trial, and confirms it a
few seconds after the node is up. Until the trial is over it refuses
another upgrade, and Interlock says so.

## Reading the bus

The Monitor page shows every frame on the bus, decoded: who sent it, to
whom, what it means. It follows live traffic, can be paused, and can
send a raw frame for testing. A node's page shows the same for that node
alone.
