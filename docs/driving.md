# Driving trains

Interlock can drive a train on its own: you choose the block it is to go
to, and Interlock sets the routes there, runs the train at the speed you
choose, and brakes it to a stand with its leading end at the block's
stop marker. It brakes the train itself, from where
[train tracking](./tracking.md) has it and the speeds tracking has
measured for it, so a train stops at the marker however fast it was
going.

The timetable to come will drive trains this way. For now you send them
yourself, from the Track page.

## What it needs

- **[Routes](./interlocking.md)** between the blocks the train runs
  through, so a track plan with blocks linked to their detectors and
  turnouts linked to their switch machines.
- **The train placed by tracking**, which knows where a train is once it
  has run from one block into the next, and **its speed measured**,
  which tracking does from the time it takes from one block to the next.
  Drive a train by hand into the next block and on into another, or
  [measure its speeds](#measuring-a-train-s-speeds).
- **Its decoder's momentum on record.** A decoder with momentum (CVs 3
  and 4) slows on its own after it is told to stop, and Interlock has to
  allow for it. Read CVs 3 and 4 from the decoder on its Decoder
  settings page. Momentum makes the stop no less exact; Interlock just
  starts braking sooner.
- **One locomotive in the train**, or a consist on the command station.
  A train made up of two locomotives with their own addresses isn't
  driven yet.
- **Stop markers** where trains are to stop, drawn on the
  [track plan](./track-plan.md#stop-markers). A block without one is
  driven to its far end.

## Driving a train to a block

In the **Trains** table on the Track page, choose **Drive…** beside the
train, then the block it is to go to and the speed step to run at, and
press **Drive**.

Interlock finds the way there over the routes it sets, through as many
blocks as it takes, the way the train faces. It backs the train only
when the block can't be reached going forward. If a route on the way
can't be set, nothing is set and the page says why: a train in the way,
a route set against it, a turnout offline.

The table then says what the train is doing: setting its routes,
running, braking for the stop, and how far it has to go. On the plan its
routes are lit and its stop marker turns green. Once it stands, the
table says where: at the marker, or so many millimeters short of it or
past it, by tracking's estimate.

The routes are released behind the train as it goes, as with any route.

### Where it stops

A train stops with its leading end at a stop marker in the block, one
for trains running the way it runs. Where the block has markers for
trains of several lengths, as subway platforms have car markers, it
stops at the one for the shortest trains it fits: a 456 mm train stops
at a marker for trains up to 460 mm rather than one for trains up to
700 mm. A marker for any length is used when no marker for a length
fits; a train longer than every marker stops at the one for the longest
trains.

A block with no marker for the train's way is driven to its far end: the
train stops 50 mm short of where the track it may use ends.

### How it brakes

The train runs at the step you chose while there is room to stop. As the
marker nears, Interlock slows it at 40 mm/s² (or as fast as its
decoder's momentum lets it, if that is slower), down to about 20 mm/s
for the last few centimeters, then stops it where its momentum brings it
to a stand at the marker.

Where the train is between detectors comes from tracking's estimate, and
each time the train enters a detected block tracking puts it back where
its decoder really is. Entering the block with the marker gives the
estimate a fresh start just before the stop, so the stop is only as far
out as the estimate can drift over the length of that block. On the
simulated layout, trains stop within 2 mm of the marker.

## Stopping a drive

**Stop** beside a train being driven brakes it to a stand as it would
for a stop, and ends the drive. Routes it set that the train hasn't
reached are canceled.

A drive also ends, leaving the train where it is, when:

- a throttle sets the train's speed. The throttle has taken the train
  over, and Interlock leaves it to the throttle;
- the train is stopped by its own emergency stop;
- the layout's emergency stop begins, or track power goes off. The
  train is set to 0 as well: the command station keeps its speed through
  the hold, and would start it again on Resume, or when power comes
  back, with nothing to brake it;
- a route ahead of it is canceled, or a turnout on its way moves. The
  train is stopped at once;
- tracking loses the train, or has it somewhere off its way. The train
  is stopped at once;
- its routes' turnouts don't move within 20 seconds.

A train that was moving when the drive began is braked to stop short of
any route still waiting for its turnouts, and goes on once they are set.

Interlock stops the trains it is driving when it shuts down. It does so
as the bus connection closes, so on a slow bus the stop may not get
through; stop the trains before stopping Interlock.

## Measuring a train's speeds

A decoder's speed curve says roughly how fast a train goes at each step,
but motors differ, and Interlock brakes better knowing what the train
really does. Tracking measures it as the train runs: each time a train
holds one step from one block into the next, the time it takes over the
track between them gives its speed at that step. Running trains by hand
or on drives measures them as you go.

**Measure speeds…** in the Trains table does it all at once. Interlock
runs the train on around the layout at six steps up to the fastest you
choose, slowest first, holding each until tracking has measured it over
two runs from one block into the next. It goes the way the turnouts lie,
setting the route ahead of the train as it goes, so set the turnouts for
a loop first. It stops the train at the end of the way set once every
step is measured.

A loop of three or more detected blocks works best. A step that isn't
measured in five minutes is given up, and the run stops if the way on
stays blocked for a minute. Clear the layout, and watch the train while
it runs.

The **Speed** column shows how many steps are measured; hover over it to
see them. Between steps measured, Interlock goes by the two either side;
beyond them, by the decoder's speed curve. Changing the decoder's speed
curve (CVs 2, 5 and 6, or its speed table) puts the speeds measured out
of date; measure them again.

## What it doesn't do yet

- **A train between blocks** can't be sent anywhere. Drive it into a
  block first.
- **A train of two locomotives** with their own addresses.
- **Reversing at the end of a drive.** A drive takes a train one way, to
  one block; a drive back is a second drive.
- **Signals.** A train driven by hand isn't stopped by Interlock if it
  runs into a block without a route.

## Trying it without hardware

On the [simulated layout](./getting-started.md#a-simulated-layout), drive
Local 1 by hand from Main St around to Park Av, so tracking places it
and measures its speed. Then choose **Drive…** beside it, pick **Main St
siding** and press **Drive**. Main St's east turnout throws, Local 1 runs
around, and it stops with its front at the marker at the far end of the
siding's platform.
