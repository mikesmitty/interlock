# Signals

Signals show a train's crew what the [interlocking](./interlocking.md)
and [train tracking](./tracking.md) already know, in the manner of the
New York City subway. Put one on the [track plan](./track-plan.md) and
it works out what to show; there is nothing to program. It lights its
[signal heads](./entities.md#signal-heads) on the layout, and shows on
the Track page whether it has heads or not.

Signals don't stop trains. A red signal is only a red light: a train on
a throttle can run past it. Routes are what protect a train
[driven](./driving.md) by Interlock.

## Placing a signal

Choose the **Signal** tool (S) in the plan editor and click on track
near the end of a block. Select the signal to set it:

- **For trains running** is the way the trains it is for run. A new
  signal faces the nearer end of its track. Trains going the other way
  need a signal of their own.
- **Upper head** and **Lower head** are the heads it lights.

It governs the way out of the block it stands in, ahead of it, so where
in the block it stands changes only where it is drawn. A signal belongs
to its track: it moves when the track moves, is on the track's layer,
and goes when the track is removed.

## Home and automatic signals

The track ahead of a signal decides what kind it is. You don't choose.

- With a junction between it and the next block it is a **home signal**.
  It shows stop until a [route](./interlocking.md) is set past it, with
  the turnouts in place and nothing on the track, and goes back to stop
  as soon as a train takes the route. It needs two heads.
- With plain track ahead it is an **automatic signal**. It needs no route
  and shows stop only for what is ahead: a train in the next block or on
  the track before it, or a route set into that block or the other way.
  It needs one head.

## What they show

An automatic signal's head:

| Head   | Meaning                                    |
| ------ | ------------------------------------------ |
| Green  | Proceed. The next signal is clear.         |
| Yellow | Proceed, ready to stop at the next signal. |
| Red    | Stop.                                      |

On a home signal the upper head says how to proceed, as above, and the
lower head says which way:

| Upper over lower   | Meaning                                            |
| ------------------ | -------------------------------------------------- |
| Green over green   | Straight route. The next signal is clear.          |
| Yellow over green  | Straight route, ready to stop at the next signal.  |
| Green over yellow  | Diverging route. The next signal is clear.         |
| Yellow over yellow | Diverging route, ready to stop at the next signal. |
| Red over red       | Stop.                                              |

A route is diverging if it takes a turnout's branch or a crossover where
the train has a choice. A local joining from a branch has none there, so
that alone doesn't make a route diverging.

The next signal is the first one in the block ahead for trains running
the same way. A signal shows green only if there is one and it doesn't
show stop. Where there is none, as on the way into a terminal, it shows
yellow, because the train will have to stop.

A signal with nothing to govern shows red, and the Track page says why:
one on track in no block, or with the track ending ahead of it.

## Heads

The Signals table on the Track page lists what each signal shows and
why. Without heads a signal shows there and on the plan alone, so a plan
can be signaled before the hardware exists.

A head on a signal belongs to it. If anything else sets it, from the
Entities page or an automation, the signal puts it back. A head can be on
one signal only.

Heads need red, yellow and green lamps, which is how a canbus-outpost
head comes. A head without a lamp shows the next most restrictive aspect
it has: yellow for green, red for yellow. A head that is offline is set
when it comes back.

## What isn't used

Signals don't use a head's lunar, S, D or number lamps, which go with
the subway's speed-timed signals, or its flashing aspects. There is no
call-on, a third light that lets a train into an occupied block, because
the interlocking doesn't set routes into one. Those aspects stay on the
heads for automations to use on heads no signal drives.

## Trying it without hardware

The [simulated layout](./getting-started.md#a-simulated-layout) has nine
signals on its test layout, with their heads enabled on the simulated
outposts. Set a route on the Track page to see its home signal clear,
and run a train past it to see the signals follow.
