import { test } from "node:test";
import assert from "node:assert/strict";
import type { TrackDirection, TrackTile } from "./api.ts";
import { backgroundBounds, cellKey, classify, drawStroke, erase, extendStroke, indexTiles, lineCells, makeID, normalizeDegrees, openEnds, straightTo, type Tiles } from "./track.ts";

test("classify names the pieces as the server does", () => {
  const cases: [TrackDirection[], string | undefined][] = [
    [["e"], "bumper"],
    [["w", "e"], "straight"],
    [["sw", "ne"], "straight"],
    [["w", "ne"], "curve"],
    [["w", "s"], "sharp_curve"],
    [["w", "e", "ne"], "turnout_left"],
    [["e", "se", "w"], "turnout_right"],
    [["w", "ne", "se"], "wye"],
    [["n", "e", "s", "w"], "crossing"],
    [["w", "e", "ne", "sw"], "crossing_45"],
    [["e", "ne"], undefined],
    [["w", "e", "n"], undefined],
    [["w", "e", "ne", "se"], undefined],
    [["e", "e"], undefined],
    [[], undefined],
  ];
  for (const [exits, piece] of cases) {
    assert.equal(classify(exits)?.piece, piece, exits.join(","));
  }
  const right = classify(["w", "e", "se"]);
  assert.deepEqual([right?.points, right?.closed, right?.thrown], ["w", "e", "se"]);
  const leftWest = classify(["e", "w", "sw"]);
  assert.deepEqual([leftWest?.piece, leftWest?.points, leftWest?.thrown], ["turnout_left", "e", "sw"]);
});

const cells = (...xy: [number, number][]) => xy.map(([x, y]) => ({ x, y }));

function draw(tiles: Tiles, ...xy: [number, number][]): Tiles {
  const result = drawStroke(tiles, cells(...xy), "track");
  assert.equal(result.error, undefined, `${xy.join(" ")}: ${result.error}`);
  return result.tiles;
}

const at = (tiles: Tiles, x: number, y: number) => tiles.get(cellKey(x, y));

test("drawing a line lays straight track ending in bumpers", () => {
  const tiles = draw(new Map(), [0, 0], [1, 0], [2, 0]);
  assert.deepEqual(at(tiles, 0, 0)?.track, ["e"]);
  assert.deepEqual(at(tiles, 1, 0)?.track, ["e", "w"]);
  assert.deepEqual(at(tiles, 2, 0)?.track, ["w"]);
  assert.deepEqual(openEnds(tiles), []);
});

test("branching off a line makes a turnout, crossing it a crossing", () => {
  let tiles = draw(new Map(), [0, 0], [1, 0], [2, 0]);
  tiles = draw(tiles, [1, 0], [2, 1]);
  assert.equal(classify(at(tiles, 1, 0)?.track ?? [])?.piece, "turnout_right");
  tiles = draw(tiles, [5, -1], [5, 0], [5, 1]);
  tiles = draw(tiles, [4, 0], [5, 0], [6, 0]);
  assert.equal(classify(at(tiles, 5, 0)?.track ?? [])?.piece, "crossing");
});

test("a stroke stops where it can't go on and says why", () => {
  const tiles = draw(new Map(), [0, 0], [1, 0], [2, 0]);
  const stub = drawStroke(tiles, cells([1, 2], [1, 1], [1, 0]), "track");
  assert.equal(stub.error, "Those three tracks don't make a turnout.");
  assert.deepEqual(at(stub.tiles, 1, 1)?.track, ["s"], "the stroke stops in the cell before the line");
  assert.deepEqual(at(stub.tiles, 1, 0)?.track, ["e", "w"]);
  const labeled = new Map(tiles).set(cellKey(3, 0), { x: 3, y: 0, label: "Times Sq" });
  assert.equal(drawStroke(labeled, cells([2, 0], [3, 0]), "track").error, "A label is in the way.");
  assert.equal(drawStroke(tiles, cells([1, 0], [1, 1]), "platform").error, "Track is in the way.");
  const sharp = drawStroke(new Map(), cells([0, 0], [1, 0], [2, -1], [1, -1]), "track");
  assert.equal(sharp.error, "Track can't turn that sharply.");
  assert.equal(at(sharp.tiles, 2, -1), undefined, "the cell that would turn too sharply is left out");
  assert.deepEqual(at(sharp.tiles, 1, 0)?.track, ["w"]);
});

test("a turnout that loses its branch drops its link", () => {
  let tiles = draw(new Map(), [0, 0], [1, 0], [2, 0]);
  tiles = draw(tiles, [1, 0], [2, 1]);
  tiles = new Map(tiles).set(cellKey(1, 0), { ...at(tiles, 1, 0)!, turnout: "turnout.t1", block: "a" });
  tiles = erase(tiles, { x: 2, y: 1 });
  assert.deepEqual(at(tiles, 1, 0), { x: 1, y: 0, track: ["e", "w"], block: "a" });
});

test("erasing a cell shortens the track around it", () => {
  const tiles = erase(draw(new Map(), [0, 0], [1, 0], [2, 0], [3, 0]), { x: 1, y: 0 });
  assert.equal(at(tiles, 0, 0), undefined, "the bumper that led only into the cell goes too");
  assert.deepEqual(at(tiles, 2, 0)?.track, ["e"]);
  assert.deepEqual(openEnds(tiles), []);
});

/** The stroke a drag through these cells makes. */
const drag = (tiles: Tiles, ...xy: [number, number][]) => extendStroke(tiles, cells(xy[0]), cells(...xy.slice(1)), "track").map((c) => [c.x, c.y]);

test("going back over a stroke takes it back", () => {
  assert.deepEqual(drag(new Map(), [0, 0], [1, 0], [2, 0], [3, 0], [2, 0], [1, 0], [2, 0]), [
    [0, 0],
    [1, 0],
    [2, 0],
  ]);
  // Straying a row down and stepping straight back up.
  assert.deepEqual(drag(new Map(), [2, 0], [3, 0], [4, 1], [3, 0], [4, 0], [5, 0]), [
    [2, 0],
    [3, 0],
    [4, 0],
    [5, 0],
  ]);
  // Back to the cell two steps ago, which could only make a triangle.
  assert.deepEqual(drag(new Map(), [0, 0], [1, 0], [2, 1], [1, 1], [1, 0]), [
    [0, 0],
    [1, 0],
  ]);
});

test("a stray the stroke turns back through too sharply is left out", () => {
  // Down a row at x 4, then up again into the row at x 4.
  const stroke = drag(new Map(), [2, 0], [3, 0], [4, 1], [4, 0], [5, 0]);
  assert.deepEqual(stroke, [
    [2, 0],
    [3, 0],
    [4, 0],
    [5, 0],
  ]);
  const tiles = drawStroke(new Map(), cells(...(stroke as [number, number][])), "track");
  assert.equal(tiles.error, undefined);
  // A cell that already holds track stays, as it may make a turnout.
  const line = draw(new Map(), [4, -1], [4, 0], [4, 1]);
  assert.deepEqual(drag(line, [3, 2], [4, 1], [4, 0]), [
    [3, 2],
    [4, 1],
    [4, 0],
  ]);
});

test("a stroke can still close a loop and cross itself", () => {
  const loop: [number, number][] = [
    [0, 0],
    [1, 0],
    [2, 1],
    [2, 2],
    [1, 3],
    [0, 3],
    [-1, 2],
    [-1, 1],
    [0, 0],
  ];
  assert.deepEqual(drag(new Map(), ...loop), loop);
  const tiles = draw(new Map(), ...loop);
  assert.deepEqual(openEnds(tiles), []);
  assert.deepEqual(at(tiles, 0, 0)?.track, ["e", "sw"], "the loop's first cell joins its last");
  const eight: [number, number][] = [
    [0, 0],
    [1, 0],
    [2, 0],
    [3, 0],
    [4, 1],
    [4, 2],
    [3, 3],
    [2, 3],
    [2, 2],
    [2, 1],
    [2, 0],
    [2, -1],
  ];
  assert.deepEqual(drag(new Map(), ...eight), eight);
  assert.equal(classify(at(draw(new Map(), ...eight), 2, 0)?.track ?? [])?.piece, "crossing");
});

test("track drawn across a gap in a block joins the block", () => {
  let tiles = draw(new Map(), [0, 0], [1, 0], [2, 0], [3, 0], [4, 0], [5, 0]);
  tiles = new Map([...tiles].map(([k, t]) => [k, { ...t, block: "a" }]));
  tiles = erase(erase(tiles, { x: 2, y: 0 }), { x: 3, y: 0 });
  assert.deepEqual(at(tiles, 1, 0)?.track, ["w"]);
  const joined = draw(tiles, [1, 0], [2, 0], [3, 0], [4, 0]);
  assert.deepEqual(at(joined, 2, 0), { x: 2, y: 0, track: ["e", "w"], block: "a" });
  assert.deepEqual(at(joined, 1, 0)?.track, ["e", "w"], "the ends of the gap are plain track again, not turnouts");
  const spur = draw(tiles, [1, 0], [2, 1], [3, 1]);
  assert.equal(at(spur, 2, 1)?.block, undefined, "track leading off a block stays out of it");
});

test("straightTo keeps to the nearest of the eight directions", () => {
  const o = { x: 0, y: 0 };
  assert.deepEqual(straightTo(o, { x: 9, y: 1 }), { x: 9, y: 0 });
  assert.deepEqual(straightTo(o, { x: 5, y: 4 }), { x: 5, y: 5 }, "nearer the diagonal than the row");
  assert.deepEqual(straightTo(o, { x: -1, y: -8 }), { x: 0, y: -8 });
  assert.deepEqual(straightTo(o, { x: -6, y: 5 }), { x: -6, y: 6 });
  assert.deepEqual(straightTo(o, o), o);
});

test("openEnds finds track that leads nowhere", () => {
  const tiles: TrackTile[] = [
    { x: 0, y: 0, track: ["w", "e"] },
    { x: 1, y: 0, track: ["w"] },
    { x: 0, y: 1, track: ["e"] },
    { x: 1, y: 1, track: ["n", "s"] },
  ];
  assert.deepEqual(
    openEnds(indexTiles(tiles)).map((o) => `${o.x},${o.y} ${o.dir}`),
    ["0,0 w", "0,1 e", "1,1 n", "1,1 s"],
  );
});

test("lineCells steps from cell to neighboring cell", () => {
  assert.deepEqual(lineCells({ x: 0, y: 0 }, { x: 3, y: 1 }), [
    { x: 0, y: 0 },
    { x: 1, y: 0 },
    { x: 2, y: 1 },
    { x: 3, y: 1 },
  ]);
  assert.deepEqual(lineCells({ x: 2, y: 2 }, { x: 2, y: 2 }), [{ x: 2, y: 2 }]);
});

test("makeID turns names into IDs that aren't taken", () => {
  assert.equal(makeID("Times Sq–42 St", new Set(), "station"), "times_sq_42_st");
  assert.equal(makeID("Track 1", new Set(["track_1"]), "block"), "track_1_2");
  assert.equal(makeID("—", new Set(["block"]), "block"), "block_2");
});

test("backgroundBounds covers a background as it is turned", () => {
  const bg = { image: "background-0123456789abcdef.png", x: 0, y: 0, width: 4, height: 2, opacity: 1 };
  assert.deepEqual(backgroundBounds(bg), { minX: 0, minY: 0, maxX: 3, maxY: 1 });
  assert.deepEqual(backgroundBounds({ ...bg, rotation: 90 }), { minX: 1, minY: -1, maxX: 2, maxY: 2 });
  assert.deepEqual(backgroundBounds({ ...bg, rotation: 180 }), { minX: 0, minY: 0, maxX: 3, maxY: 1 });
  const tilted = backgroundBounds({ ...bg, rotation: 45 })!;
  assert.ok(tilted.minY < 0 && tilted.maxY > 1, "a tilted image reaches past its unturned box");
  assert.equal(backgroundBounds(undefined), undefined);
});

test("normalizeDegrees brings angles into a turn", () => {
  assert.equal(normalizeDegrees(-90), 270);
  assert.equal(normalizeDegrees(360), 0);
  assert.equal(normalizeDegrees(450), 90);
});
