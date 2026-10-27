# The track plan

The Track page shows the layout's track as a diagram, lit by what the
layout reports, like a panel in an interlocking tower. Blocks a train is
in light red, with the locomotive's name from RailCom beside them, and
each turnout shows the way it is set. Click a turnout to throw or close
it. Below the diagram, a table lists every block with its state in
words, the train in it and its length.

The plan is also the model of the layout that later work builds on:
which blocks connect to which, through which turnouts, how long each
block is and which blocks are a station's platform tracks. Interlock
works this out from the drawing every time the plan is saved.

## Drawing

Choose **Edit track plan**. The diagram becomes a grid and the tools
appear above it. Nothing reaches the layout until you save, and **Undo**
(⌘Z or Ctrl+Z) and **Redo** step back and forth through your changes.

- **Track** (T): drag from cell to cell to lay track. Track runs through
  the middle of a cell's sides or through its corners, so it goes
  straight, diagonal, or bends by 45 or 90 degrees. Drag off the middle
  of a line to make a turnout there; drag straight across a line to make
  a crossing. A line ends where you stop dragging, as the end of a track.
- **Platform** (P): drag to draw a platform beside the track.
- **Label** (L): click an empty cell to write a station name or a note
  there; change the text on the right.
- **Block** (B): drag over track to put it in the block chosen under
  Blocks. Hold Option or Alt while dragging to take track out of its
  block.
- **Erase** (E): drag over cells to clear them. Track that led into a
  cleared cell stops at its edge.
- **Select** (V): click a cell to see what it holds. For a turnout you
  can choose its switch machine; for track, its block. Delete or
  Backspace clears the selected cell.

Track that leads into a cell with no track is marked with an amber dot,
and the **To fix** panel counts these places. It also lists blocks with
no track drawn in them and blocks drawn in pieces that don't join.

If someone else saves the plan while you are editing, saving tells you,
and you can save yours over theirs or load theirs instead. An edit in
progress is kept while you look at another page, but not when the
browser reloads.

## Tracing a plan from planning software

To draw the layout from a plan made in track planning software, export
the plan as an image and put it under the grid. PNG, JPEG, WebP, GIF and
SVG images up to 25 MB work.

1. Under **Background** in the editor, choose **Choose an image**. The
   image goes under the grid, at the top left of what is drawn so far.
   The **Background** tool (G) is selected.
2. Drag the image with the Background tool to line it up with the grid.
3. Set its **Width in cells** so that a known length on the plan, such
   as the distance between two parallel tracks, comes out as whole
   cells. Its height follows.
4. Set its **Opacity** low enough that your own track stands out. On the
   dark theme, a plan drawn dark on white is easier to trace with **Swap
   light and dark** on.
5. Draw the track over it.

The image is saved with the plan. It shows only while editing, unless
**Show it while operating too** is on. **Remove it** takes it away
again; saving then deletes the image from the server.

## Blocks

A block is a stretch of track a train is detected in. Add one under
**Blocks**: choose its detector, a canbus-outpost track for example, and
Interlock names the block after it and links the reporter on the same
track, which names the locomotives RailCom sees there. Then paint the
block's track with the Block tool.

For each block you can also set:

- its **length** in millimeters, measured on the layout. The diagram is
  a schematic, so lengths come from here, not from the drawing.
- its **station**, which makes the block one of that station's platform
  tracks. Add stations under **Stations**.

Once the plan is saved, a block's panel lists where each of its ends
leads: the next block, and the turnout positions on the way.

## The file

The plan is `trackplan.yaml` in the data directory, with the background
image in its `backgrounds` folder; backups include both. Each tile is one cell, with x growing rightward and y downward:

```yaml
tiles:
  - {x: 5, "y": 2, track: [e, w], block: track_1}
  - {x: 6, "y": 2, track: [e, ne, w], turnout: turnout.0aff9024a634.servo_1}
  - {x: 8, "y": 3, platform: [e, w]}
  - {x: 8, "y": 4, label: Times Sq–42 St}
blocks:
  - id: track_1
    name: Track 1
    occupancy: block.0aff9024a634.track_1
    reporter: reporter.0aff9024a634.track_1
    length_mm: 610
    station: times_sq
stations:
  - id: times_sq
    name: Times Sq–42 St
```

`track` lists the directions the track leaves the cell by: `n`, `ne`,
`e`, `se`, `s`, `sw`, `w` and `nw`. The piece follows from them. One
direction is the end of a track. Two opposite ones are a straight, and
two 135 or 90 degrees apart are a curve. Three are a turnout: a straight
with a branch 45 degrees off it, or a wye. Four are a crossing of two
straights. For a turnout, closed is the straight way and thrown the
branch. For a wye, closed is the left leg as seen from the points and
thrown the right.

Interlock checks the file when it starts and won't start with a plan it
can't read. Restart Interlock after editing the file yourself.
