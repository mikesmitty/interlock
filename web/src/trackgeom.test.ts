import { test } from "node:test";
import assert from "node:assert/strict";
import type { TrackPlan } from "./api.ts";
import {
  arcAt,
  arcFrom,
  arcInfo,
  backgroundBounds,
  indexPlan,
  idMaker,
  junctionCenter,
  junctionOutward,
  makeID,
  mergeTrack,
  normalizeDegrees,
  openEnds,
  outward,
  PIECES,
  placePiece,
  routePath,
  tangentAngle,
  trackPath,
  turnDiff,
} from "./trackgeom.ts";

const near = (a: number, b: number, msg?: string, within = 1e-6) => assert.ok(Math.abs(a - b) < within, `${msg ?? ""} ${a} ≉ ${b}`);

test("arcInfo measures straight and curved track as the server does", () => {
  const quarter = arcInfo({ x: 0, y: 0 }, { x: 216, y: 216 }, 90);
  near(quarter.radius, 216);
  near(quarter.length, (216 * Math.PI) / 2);
  near(quarter.startHeading, 0);
  near(quarter.endHeading, 90);
  const mid = arcAt({ x: 0, y: 0 }, { x: 216, y: 216 }, 90, 0.5);
  near(mid.x, 216 * Math.sin(Math.PI / 4));
  near(mid.y, 216 - 216 * Math.cos(Math.PI / 4));
  assert.equal(arcInfo({ x: 0, y: 0 }, { x: 3, y: 4 }).length, 5);
  assert.match(trackPath({ x: 0, y: 0 }, { x: 216, y: 216 }, 90), /^M0 0A216[.\d]* 216[.\d]* 0 0 1 216 216$/);
  assert.match(trackPath({ x: 0, y: 0 }, { x: 216, y: -216 }, -90), / 0 0 0 216 -216$/);
  assert.equal(turnDiff(10, 350), 20);
  assert.equal(normalizeDegrees(-90), 270);
});

test("arcFrom lays a curve that sets off and ends as arcInfo says", () => {
  for (const sweep of [45, -15, 90, -180]) {
    const start = { x: 100, y: 50 };
    const end = arcFrom(start, 30, 249, sweep);
    const info = arcInfo(start, end, sweep);
    near(info.radius, 249, `radius for ${sweep}`);
    near(turnDiff(info.startHeading, 30), 0, `start heading for ${sweep}`);
    near(turnDiff(info.endHeading, 30 + sweep), 0, `end heading for ${sweep}`);
  }
  near(tangentAngle({ x: 0, y: 0 }, 0, { x: 216, y: 216 }), 90);
  near(tangentAngle({ x: 0, y: 0 }, 0, { x: 216, y: -216 }), -90);
});

test("a placed turnout's branch leaves along its curve", () => {
  const right = PIECES.find((p) => p.id === "t4r")!;
  const placed = placePiece(right, { x: 1000, y: 0 }, 180);
  near(placed.ends[1].x, 1000 - 126);
  assert.ok(placed.ends[2].y < 0, "heading west, right is up the plan");
  const center = junctionCenter({ at: placed.at }, placed.ends);
  // Placed to a thousandth of a millimeter, so true to a hundredth of a degree.
  near(turnDiff(junctionOutward("turnout", placed.ends, center, 2), 180 + 15), 0, "branch", 0.01);
  near(turnDiff(junctionOutward("turnout", placed.ends, center, 0), 0), 0, "points", 0.01);
  assert.match(routePath("turnout", placed.ends, center, { from: 0, to: 2 }), /^M1000 0Q/);
  const cross = placePiece(
    PIECES.find((p) => p.id === "xd")!,
    { x: 0, y: 0 },
    0,
  );
  assert.deepEqual(cross.ends[2], { x: 0, y: 33 });
  assert.match(routePath("double_crossover", cross.ends, junctionCenter({}, cross.ends), { from: 0, to: 3 }), /^M0 0C155 0 155 33 310 33$/);
});

const line: TrackPlan = {
  points: [
    { id: "p1", x: 0, y: 0, bumper: true },
    { id: "p2", x: 100, y: 0 },
    { id: "p3", x: 200, y: 100 },
  ],
  tracks: [
    { id: "t1", a: "p1", b: "p2" },
    { id: "t2", a: "p2", b: "p3", angle: 90 },
  ],
  junctions: [],
  platforms: [{ id: "f7", x: 0, y: 0, length: 10, width: 4 }],
  labels: [],
  layers: [],
  blocks: [],
  stations: [],
  grid: 33,
};

test("open ends and the way track leaves them", () => {
  const index = indexPlan(line);
  assert.deepEqual(
    openEnds(index).map((p) => p.id),
    ["p3"],
  );
  near(outward(index, index.at.get("p3")![0])!, 90);
  near(normalizeDegrees(outward(index, index.at.get("p1")![0])!), 180);
});

test("idMaker and mergeTrack give imported track IDs of its own", () => {
  const id = idMaker(line);
  assert.equal(id("p"), "p4");
  assert.equal(id("p"), "p5");
  assert.equal(id("f"), "f8");
  assert.equal(id("j"), "j1");
  const merged = mergeTrack(
    line,
    {
      points: [
        { id: "p1", x: 5, y: 5 },
        { id: "p2", x: 9, y: 9, bumper: true },
      ],
      tracks: [{ id: "t1", a: "p1", b: "p2" }],
      junctions: [],
    },
    "upper",
  );
  assert.deepEqual(merged.tracks[2], { id: "t3", a: "p4", b: "p5", layer: "upper" });
  assert.deepEqual(merged.points[4], { id: "p5", x: 9, y: 9, bumper: true });
});

test("makeID turns names into IDs that aren't taken", () => {
  assert.equal(makeID("Times Sq–42 St", new Set(), "station"), "times_sq_42_st");
  assert.equal(makeID("Track 1", new Set(["track_1"]), "block"), "track_1_2");
});

test("backgroundBounds covers a background as it is turned", () => {
  const bg = { image: "background-0123456789abcdef.png", x: 0, y: 0, width: 400, height: 200, opacity: 1 };
  const flat = backgroundBounds(bg)!;
  for (const [k, v] of Object.entries({ minX: 0, minY: 0, maxX: 400, maxY: 200 })) near(flat[k as keyof typeof flat], v, k);
  const turned = backgroundBounds({ ...bg, rotation: 90 })!;
  near(turned.minX, 100);
  near(turned.minY, -100);
  assert.equal(backgroundBounds(undefined), undefined);
});
