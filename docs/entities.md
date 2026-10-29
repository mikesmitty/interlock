# Entities

An entity is one thing on the layout with a state and, often, commands:
a turnout that is thrown or closed, a block that is occupied or clear, a
reporter that knows which locomotives are on its track, a sensor, a
switch or a button. Entities are what automations watch and act on, so
the Entities page is where the layout stops being event IDs and starts
being things.

## Where they come from

Nodes describe their events in their configuration: an outpost's servo
has throw and close command events and thrown and closed feedback
events, its tracks have occupied and clear events and a RailCom reporter
range. Interlock reads these into the named event table, and profiles
turn groups of them into entities.

A profile is a short YAML document saying which groups of a node's
configuration make which kind of entity, and which named event plays
which role. Interlock ships profiles for canbus-outpost and simple-dcc.
For other nodes it guesses from the names in the description and marks
the result as suggested; you confirm or correct it on the Entities page,
and your decision is kept in `entities.yaml` in the data directory. Your
own profiles go in `profiles/` under the data directory and override the
shipped ones.

Entities can also be made by hand on the page from any event IDs, for
nodes with no description at all.

## Turnouts on DCC accessory decoders

A turnout on a DCC accessory decoder needs only its number. On the
Entities page, choose **Build one by hand**, pick Turnout, set **Driven
by** to a DCC accessory decoder, and type the number the decoder is set
to, 1 to 2048, as your throttle or the decoder's instructions number it.
A command station that takes the standard LCC accessory events, such as
`simple-dcc`, turns each throw and close into an accessory packet.

The events carry the decoder's address in the form the DCC packet uses,
which is not the number people type: accessory 1 is address 4 in the
packet, and 2045 to 2048 come round to 0 to 3. Interlock works this out
for you. Other LCC software that numbers accessories the standard way
throws the same turnout for the same number.

The turnout's ID is `turnout.dcc.` and the number, such as
`turnout.dcc.12`, and `entities.yaml` keeps the number rather than the
events. To use another number, build a new turnout. An accessory
decoder doesn't say where its points are, so the turnout shows what it
was last told: Interlock asks the command station when it starts, and
`simple-dcc` remembers each output's last setting until it restarts. If
something reports the points' position, such as a sensor on an outpost,
add its events as the turnout's thrown and closed feedback.

## Live state

Interlock follows the bus. When a node reports an event, every entity
that event belongs to updates: a block goes occupied, a turnout reports
thrown. When Interlock starts, or a node comes back, it asks the nodes
which of their events are currently valid, so the page shows the
layout's real state rather than unknowns. A reporter lists the
locomotives on its track by address and shows when each arrived.

An entity whose node is offline is shown as unavailable; its last state
is kept.

## Controls

A turnout has a lever for throw and close (and center, where the node
has it), a switch has on and off, a button can be pressed. Each sends the
entity's command event from Interlock's node and the entity updates when
the node reports the result, so the lever shows what the layout did, not
what was asked.

## Kinds

| Kind     | States                                        | Commands             |
| -------- | --------------------------------------------- | -------------------- |
| turnout  | thrown, closed, moving, unknown               | throw, close, center |
| block    | occupied, clear, unknown                      |                      |
| reporter | occupied, clear, plus the locomotives present |                      |
| sensor   | active, inactive, unknown                     |                      |
| switch   | on, off, unknown                              | on, off              |
| button   | pressed, idle                                 | press                |

Entity IDs read like `turnout.0a0000000042.servo_1`: the kind, the node
and the group. Ones built by hand read like `sensor.manual.yard_door`,
or `turnout.dcc.12` for a DCC accessory decoder. They are what automations refer to; the editor picks them
from a list.
