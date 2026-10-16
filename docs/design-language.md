# Design language

The web interface borrows from the machines that gave Interlock its name:
the interlocking tower and the CTC panel. Those are dark enamel panels with
a white track diagram, indicator lamps for every state that matters, and
engraved labels you can read from across a room. Interlock uses that
vocabulary without imitating the hardware.

## Tokens

Color, dark (default; layout rooms are dim):

| Token | Value | Use |
|---|---|---|
| enamel | #26332E | page background: machine green-gray, not black |
| enamel-deep | #1B2622 | sunken areas: navigation rail, table headers |
| enamel-raised | #2F3D37 | panels and rows that sit above the page |
| ivory | #EFE9D8 | primary text, like engraved label fill |
| ivory-dim | #B8B3A4 | secondary text |
| brass | #D4A64A | the one accent: things you can act on, focus |
| lamp-green | #35B873 | lit: online, connected, closed, clear |
| lamp-red | #E0493D | lit: fault, offline after being online, occupied |
| lamp-amber | #E9A23B | lit: in between, checking, unknown |
| lamp-off | #3A463F | unlit |

Color, light ("drawing sheet", for daylight and printing): background
#EDEFEA, raised #F7F8F5, deep #E2E5DF, ink #1F2A26, dim #5B655F, brass
#9B7420. Lamp colors keep their hue with slightly deeper values.

Type: Barlow for everything. Barlow is the regular width for reading;
Barlow Semi Condensed carries dense data (tables, the monitor, identifiers)
and page titles. Both are bundled, so the interface looks the same offline
in a train room. Scale from a 16 px base at ratio 1.2: 13.3, 16, 19.2, 23,
27.6, 33. Body line height 1.5, headings 1.2. Weights 400, 500, 600. Figures
are tabular everywhere a column of numbers can appear. No all-caps labels,
no eyebrows, no monospace except raw frames in the monitor, which are code.

Layout: a narrow navigation rail on the left (bottom bar on phones), a
status strip across the top with one lamp per link and the local node's
state, and the page below. Content is left aligned; measure under 80
characters for prose. Tables get the width they need.

```
+------+---------------------------------------------------+
| rail | (o) outpost  (o) jmri    node 0A.CB...  alias 0xD4D |
|      +---------------------------------------------------+
| Nodes|  Page title                        [Refresh]        |
| Mon. |  (o) Sim Outpost   mikesmitty CAN Bus Outpost ...   |
| Sett.|  (o) Interlock     Interlock Layout Controller ...  |
|      |                                                     |
+------+---------------------------------------------------+
```

## Principles

1. State is shown with a lamp and a word, never a color alone.
2. Names first. The user's name for a node leads; manufacturer and model
   follow; node ID and alias are small and tabular.
3. Density where the data is (the monitor), air everywhere else.
4. Brass means "you can press this". Nothing decorative is brass.
5. Nothing moves on its own. Lamps change instantly; new monitor rows
   appear without animation; motion only answers a click.
6. Empty and error states say what to do next, in the interface's voice.

## What was rejected and why

- Near-black with an acid accent: the generic dark dashboard. Enamel green
  with brass is specific to the subject and easier on the eyes for hours.
- Cream paper with a serif display: the generic "editorial" look, and the
  terracotta accent that usually comes with it.
- Card grids with identical rounded corners and soft shadows: nodes and
  links are rows in a table, which is what the data is.
- Chips for protocol lists: a plain comma separated list reads better and
  prints better.
