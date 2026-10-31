# Routes and interlocking

A route is a way from one block into the next: the track between them
and the turnout positions it needs. Setting a route moves its turnouts
and locks them, so nothing in Interlock can move them while a train is
using the route. As the train runs through, the track behind it is
released a piece at a time, and once the train is in the block the
route leads into, the route is released too.

[Driving](./driving.md) a train to a block sets the routes it needs,
and the timetable to come will set routes for its trains. You can also
set them yourself, on the Track page.

## What it needs

- **A track plan with blocks**, each linked to its block and reporter
  entities (see [Blocks](./track-plan.md#blocks)). Interlock works out
  the routes from the plan: every way from the end of one block to the
  end of another, over track that is in no block, each taken either way.
- **Turnouts between blocks, linked to their switch machines.** A route
  sets the turnouts and crossovers on the track between two blocks. A
  turnout inside a block isn't part of any route; set it yourself.
- **[Train tracking](./tracking.md)** following the trains, with the
  lengths of their locomotives and cars in the roster. Tracking says
  where each train's rear is, which is what releases the track behind
  it.

## Setting a route

Under the plan on the Track page, **Routes** lists the routes that are
set. To set one, choose the block the train is in under **From**, then
where it is to go under **To**, and press **Set route**. Where two ways
join the same two blocks, they are told apart by the turnouts each
throws.

A route set is lit along its track on the plan: amber while its
turnouts are moving, green once they are where it needs them. The
table says the same: **Waiting for** the turnouts still moving, **Set**,
or the train that is on it.

A route isn't set, and the page says why, when:

- a train is in the block it leads into, or that block's detector
  reports it occupied;
- another route is set into the same block;
- another route holds any of its track, or holds one of its turnouts the
  other way;
- a train is on one of its turnouts that would have to move, or on
  track that crosses it;
- a turnout it needs has no switch machine linked, or its node is
  offline.

A train counts as on a piece of track when tracking puts it within 30 mm
of it, to allow for the estimate being out.

## Locking

While a route holds a turnout, Interlock refuses to move it: a click on
the Track page, a command on the Entities page, an
[automation](./automations.md) and a throttle app all get "locked by the
route from … to …". Setting it the way the route has it is still
allowed.

Interlock can't stop other nodes on the bus, or a fascia button wired
to an outpost, from moving a turnout. If a locked turnout moves away,
the route goes back to **Waiting for** that turnout and Interlock leaves
it as it is. **Set again** moves it back.

## Released behind the train

Once a train is within 30 mm of a route's first turnout, the route is
the train's. As its rear clears each piece of the route by 30 mm, that
piece is released and its turnouts can be moved again. Once nothing of
the route is under the train and the train is in the block it leads
into, the route is released.

A train that backs out of a route the way it came leaves the route set.
Cancel it once the train is clear.

## Canceling a route

**Cancel** releases a route with no train on it at once. The turnouts
stay where they are; only the lock goes.

A route with a train on it is released behind the train as it goes, so
Cancel asks first. Canceling it anyway lets its turnouts be moved under
the train. Do that only when tracking is wrong about where the train
is.

## Over a restart

Routes set stay set when Interlock restarts. Each waits for its
turnouts to report where they are, which takes as long as identifying
the boards.

## A train taken off the layout

Tracking keeps a train where it last saw it, so a train lifted off the
track would keep its block and the routes into it from being set. Use
**Forget…** in the **Trains** table on the Track page. Tracking finds
it again when a detector next sees it run into a block.

## What it doesn't do yet

- **Stopping a train at a signal.** [Signals](./signals.md) show the
  routes set, but a train on a throttle can run past a red one.
- **Approach locking.** A route can be canceled while a train is about
  to run onto it.
- **A trip stop.** A train that runs into a block without a route isn't
  stopped.
- **Routes through more than one block.** Set each block to the next in
  turn.
- **A route back into the block it starts from,** such as around a
  reversing loop: the train is in the block it leads into, so it isn't
  set.

## Trying it without hardware

On the [simulated layout](./getting-started.md#a-simulated-layout), set
**Park Av outer → East inner**: the crossover east of Park Av throws,
the route lights amber and then green, and clicking the crossover is
refused. Drive Local 1 around to it. The route shows the train on it
until its last car has crossed, then clears, and the crossover can be
thrown again.
