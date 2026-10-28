# The track plan

The Track page shows the layout's track to scale, lit by what the layout
reports, like a panel in an interlocking tower. Blocks a train is in
light red, with the locomotive's name from RailCom beside them, and each
turnout and crossover shows the way it is set. Click one to throw or
close it. Below the plan, a table lists every block with its state in
words, the train in it and its length.

The plan is also the model of the layout that later work builds on:
which blocks connect to which, through which turnouts, how long each
block is and which blocks are a station's platform tracks. Interlock
works this out from the plan every time it is saved, measuring along
the track.

## The plan

The plan is drawn in millimeters on the layout, so curves have their
real radius and parallel tracks their real spacing. Track runs between
points:

- **Straights and curves** each join two points. A curve turns by an
  angle, which with the distance between its ends gives its radius.
- **Junctions** are where track branches or crosses: turnouts, wyes,
  crossings, and crossovers between two tracks side by side, single or
  double. A turnout's closed route is the straight way, thrown the
  branch.
- A point joins at most two pieces of track end to end. Track that ends
  without a buffer stop is marked in amber while editing.

## Bringing in a plan from RailModeller Pro

A layout planned in RailModeller Pro comes in as planned:

1. In RailModeller Pro, export the layout for JMRI's PanelPro. With its
   default settings a centimeter on the layout is one unit of the
   exported panel. Only the layers you can see are exported.
2. On the Track page, choose **Edit track plan**, then under **Import**
   choose the exported file. **Millimeters to a panel unit** is 10 for
   RailModeller's default; change it if you changed RailModeller's
   setting.
3. The track is added where it lies in the export, as one step you can
   undo, and comes in selected, so you can move and turn it into place
   (see **Selecting, moving and turning** below). Imports add to what is
   there, so a layout on two decks comes in a deck at a time, each onto
   a layer of the plan (see **Layers** below).

RailModeller's export leaves out its crossovers and diamond crossings;
the gaps they leave are marked in amber. Put crossovers back with the
Turnout tool, below. The import's notes say what else, if anything, it
left out.

## Drawing

Choose **Edit track plan**. The plan gets a grid and the tools appear
above it. Nothing reaches the layout until you save, and **Undo** (⌘Z or
Ctrl+Z) and **Redo** step back and forth through your changes.

- **Straight** (T): drag from the end of a track to carry it on
  straight, or from anywhere to lay a straight between two grid points.
  Let go on another track end to join the two.
- **Curve** (C): drag from the end of a track to curve it, at the radius
  chosen above the plan (Kato's R216 to R718, or any radius in
  millimeters), in steps of 45°, 15°, 7.5° or 1°. It bends the way you
  drag. Let go on another track end to join the two with the one curve
  that fits.
- **Turnout** (W): click the end of a track to put a piece there: a #4
  or #6 turnout to the left or right, a wye, or a single or double
  crossover to the track on one side. The piece carries on from the end
  you click, and its other ends join any track already in place there,
  so a crossover put on the end of one of two parallel tracks joins
  both.
- **Platform** (P): drag along where a platform goes, from one end to
  the other; set its width above the plan.
- **Label** (L): click to write a label there; change the text on the
  right.
- **Block** (B): drag over track to put it in the block chosen under
  Blocks. Hold Option or Alt while dragging to take track out of its
  block.
- **Erase** (E): click or drag over track, platforms and labels to
  remove them.
- **Select** (V): click track, a platform or a label to see what it is
  and change it: a curve's radius and length, a turnout's switch
  machine, a buffer stop at a track end. See **Selecting, moving and
  turning** below for working on many at once.

### Selecting, moving and turning

With the Select tool, drag a box to select everything wholly inside it,
Shift-click to add an element or take it away, or press ⌘A (Ctrl+A) to
select everything on the layers shown. **Select** beside a layer under
**Layers** selects everything on it, and imported track comes in
selected.

- Delete or Backspace removes the whole selection, as does **Remove
  all** in its panel.
- Drag the selection to move it, or nudge it with the arrow keys: a
  millimeter at a time, a grid space with Shift. **Move and turn** moves
  it by a distance you type and turns it by any angle, or a quarter or
  half turn, about its middle.
- With many selected, the panel puts them all in a block or on a layer
  at once.

Moving or turning a selection takes it away from track it was joined to
but doesn't include, which stays where it is, its ends now open.

**Grid in mm**, shown with the Straight, Platform and Label tools, is the
spacing they snap to. It starts at 33, the distance between the tracks
of Kato Unitrack N double track.

The **To fix** panel counts the track ends that lead nowhere, and once
the plan is saved it lists joints where track changes direction (a
kink), blocks with no track and blocks in pieces that don't join.

If someone else saves the plan while you are editing, saving tells you,
and you can save yours over theirs or load theirs instead. An edit in
progress is kept while you look at another page, but not when the
browser reloads.

## Layers

Layers divide a plan you want to see a part at a time, such as the
decks of a layout on two levels. Add them under **Layers**. New track,
platforms and labels go on the layer chosen above the plan, and an
element's layer can be changed when it is selected. The boxes beside
each layer show and hide it, on that browser only; while operating, the
same boxes appear above the plan. Deleting a layer keeps what is on it.

## Blocks

A block is a stretch of track a train is detected in. Add one under
**Blocks**: choose its detector, a canbus-outpost track for example, and
Interlock names the block after it and links the reporter on the same
track, which names the locomotives RailCom sees there. Then paint the
block's track with the Block tool.

A block's length is measured along its track, taking turnouts in it the
closed way. If it differs on the layout, enter the **Measured length**
and that is used instead. A block can also be one of a **station**'s
platform tracks; add stations under **Stations**.

Once the plan is saved, a block's panel lists where each of its ends
leads: the next block, and the turnout positions on the way.

## Tracing a plan from an image

To draw from a plan that can't be imported, export it as an image and
put it under the plan. PNG, JPEG, WebP, GIF and SVG images up to 25 MB
work.

1. Under **Background** in the editor, choose **Choose an image**. The
   image goes under the plan and the **Background** tool (G) is
   selected.
2. Drag the image with the Background tool to line it up. If it is drawn
   at an angle, or with another side up, turn it: **Rotation** takes any
   angle clockwise, and the ↺ and ↻ buttons turn it a quarter turn at a
   time.
3. Set its **Width in mm** so that a known length on it, such as a
   straight piece of track, comes out right.
4. Set its **Opacity** low enough that your own track stands out. On the
   dark theme, a plan drawn dark on white is easier to trace with **Swap
   light and dark** on.

The image is saved with the plan. It shows only while editing, unless
**Show it while operating too** is on. **Remove it** takes it away
again; saving then deletes the image from the server.

## The file

The plan is `trackplan.yaml` in the data directory, with the background
image in its `backgrounds` folder; backups include both. Positions are in
millimeters, x rightward and y downward. Here a #4 turnout's branch
curves back alongside the line it left, 33 mm over:

```yaml
points:
  - {id: p1, x: 0, "y": 0, bumper: true}
  - {id: p2, x: 248, "y": 0}
  - {id: p3, x: 374, "y": 0}
  - {id: p4, x: 372.49, "y": 16.39}
  - {id: p5, x: 496.98, "y": 32.8}
tracks:
  - {id: t1, a: p1, b: p2, block: track_1}
  - {id: t2, a: p4, b: p5, angle: -15}
junctions:
  - {id: j1, kind: turnout, ends: [p2, p3, p4], at: {x: 311.32, "y": 0}, switch: turnout.0aff9024a634.servo_1}
platforms:
  - {id: f1, x: 124, "y": -36, length: 250, width: 39}
labels:
  - {id: l1, x: 0, "y": -80, text: Times Sq–42 St}
blocks:
  - id: track_1
    name: Track 1
    occupancy: block.0aff9024a634.track_1
    reporter: reporter.0aff9024a634.track_1
    station: times_sq
stations:
  - id: times_sq
    name: Times Sq–42 St
grid: 33
```

A track's `angle` is how far it turns going from `a` to `b`, in
degrees: positive clockwise on the plan, negative counterclockwise,
absent for straight track. A junction lists its `ends` in the order its
kind gives:

| kind | ends |
| --- | --- |
| `turnout` | the points, the straight way (closed), the branch (thrown) |
| `wye` | the points, the left leg (closed), the right leg (thrown) |
| `crossing` | one track's two ends, then the other's |
| `crossover` | track a's two ends, then track b's, a's first end beside b's first; thrown, it leads from a's first end to b's second |
| `double_crossover` | as `crossover`; thrown, it also leads from b's first end to a's second |

A turnout's or wye's `at` is where its routes part, where the lines along
its ends meet.

Plans drawn before Interlock drew to scale were a grid of cells. Such a
plan is converted when Interlock starts, a cell to 33 mm, and the plan
as it was is kept beside it as `trackplan-grid.yaml`.

Interlock checks the file when it starts and won't start with a plan it
can't read. Restart Interlock after editing the file yourself.
