# Train tracking

Interlock follows the trains on the track plan. It knows which block
each train is in, where in the block it is, which way it is going and
where the rest of the train reaches. The Track page draws each train on
its track, and the timetable and automatic driving to come build on
this.

## What it needs

- **Blocks linked to reporters.** RailCom detectors, such as a
  canbus-outpost's tracks, report the address of a decoder in their
  block. Link each block to its reporter on the Track page (see
  [Blocks](./track-plan.md#blocks)).
- **The train, made up in the [roster](./roster.md)**: its locomotives
  and cars in order from the front, each with its length. A detector
  sees only a locomotive's decoder, so the train's length and where that
  locomotive is in it say where the rest of the train is. Tracking takes
  the decoder to be at the middle of its locomotive. A locomotive in no
  train is followed on its own, at its own length, and one without a
  length is drawn as a dot at its decoder.
- **The decoder's momentum.** Read CVs 3 and 4 (and 2, 5 and 6 for its
  speed curve) on the [Programming](./decoders.md) page, so tracking
  slows and speeds the train up as its decoder does. Without them it
  changes speed at once, and a train that stops and turns about can be
  a few hundred millimeters off until its next block.

## How it follows a train

1. **Seen in a block.** When a detector first reports a decoder,
   tracking knows its block but not where in it, nor which way the
   train faces. The train's name shows in the block.
2. **Into the next block.** When the decoder is reported in the next
   block, tracking knows where it entered and which way it was going.
   It lays the train out behind or ahead of the decoder along the track
   it came by, following the turnouts as they are set. Going forward
   the front leads; in reverse the rear does. A locomotive facing
   backward in its train runs the train backward when its throttle goes
   forward.
3. **Between blocks.** The train runs on at the speed it is set to,
   through its decoder's speed curve and momentum, so its place moves
   smoothly. It never passes into the next detected block before a
   detector there sees it.
4. **At each entry** tracking puts the train back where its decoder
   really is. The time from one entry to the next over the track
   between them measures how fast the train goes at full speed. The
   first measure comes from the second block it enters, and each run
   refines it.

Detectors report a decoder leaving a block only a second after it left,
so the blocks a train is in come from where tracking has it, not from
the detectors.

A train keeps its place when Interlock restarts, and stays there while
no detector sees it. A train that stops between blocks, or on a
detector that drops out for a moment, is still known.

When a train is made up again in the roster, a car swapped or a
locomotive measured, tracking lays it out again around its decoder,
which stays where it was. A locomotive put into a train becomes that
train. In a train with more than one locomotive, tracking follows the
decoder a detector last reported and takes the speed that decoder is
set to. Two RailCom decoders in one block talk over each other, so let
only one of them send its address (CV 28 bit 0 off on the others).

## On the Track page

Each train tracking has placed is drawn on its track as a bar, pointed
at the end that leads, with a red dot where its decoder is. A train
whose place in its block isn't known yet is named in the block. The
**Trains** table lists each train with the blocks it is in, the way it
is going, its speed, and what would help follow it better. The
**Blocks** table names the trains in each block, marking a block only
the front or rear of a train reaches into.

## What it doesn't do yet

- The way a train faces comes from the direction its throttle set,
  forward unless one has. RailCom's orientation report isn't used yet.
- Distances are measured along the track as drawn. A block's
  **Measured length** isn't used for tracking yet.
- Trains driven from a throttle that doesn't go through Interlock move
  only when a detector sees them, from block to block.
- A train's place can't be set by hand. Run it into the next block.

## Trying it without hardware

The [simulated layout](./getting-started.md#a-simulated-layout) puts its
trains in the roster as a motor car and cars, made up into a train, and
fits each motor car with a decoder whose CVs are on record, so tracking
can be watched on its test layout.
