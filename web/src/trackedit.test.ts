import { test } from "node:test";
import assert from "node:assert/strict";
import type { TrackPlan } from "./api.ts";
import {
  addJunction,
  addTrack,
  arcInfo,
  endNear,
  indexPlan,
  layerRefs,
  moveSelection,
  openEnds,
  pieceShapes,
  placePiece,
  refsInBox,
  removeElements,
  selectionBounds,
  selectionMiddle,
  setBlock,
  setLayer,
  turnSelection,
  updateElement,
} from "./trackgeom.ts";

const empty: TrackPlan = { points: [], tracks: [], junctions: [], platforms: [], labels: [], layers: [], blocks: [], stations: [], grid: 33 };

test("track drawn from a free end joins it and takes its buffer stop away", () => {
  let plan = addTrack(empty, indexPlan(empty), { x: 0, y: 0 }, { x: 248, y: 0 }, 0);
  plan = { ...plan, points: plan.points.map((p) => (p.x === 248 ? { ...p, bumper: true } : p)) };
  const index = indexPlan(plan);
  const end = endNear(index, { x: 250, y: 3 }, 10)!;
  assert.equal(end.point.x, 248);
  assert.equal(end.heading, 0);
  assert.equal(endNear(index, { x: 300, y: 0 }, 10), undefined);
  plan = addTrack(plan, index, end.point.id, { x: 464, y: 216 }, 90, "upper");
  assert.equal(plan.tracks.length, 2);
  assert.deepEqual(plan.tracks[1], { id: "t2", a: "p2", b: "p3", angle: 90, layer: "upper" });
  assert.equal(plan.points[1].bumper, undefined, "the buffer stop went");
  assert.deepEqual(
    openEnds(indexPlan(plan)).map((p) => p.id),
    ["p1", "p3"],
  );
});

test("a crossover placed between two parallel ends joins both", () => {
  let plan = addTrack(empty, indexPlan(empty), { x: 0, y: 0 }, { x: 100, y: 0 }, 0);
  plan = addTrack(plan, indexPlan(plan), { x: 0, y: 33 }, { x: 100, y: 33 }, 0);
  const index = indexPlan(plan);
  const end = endNear(index, { x: 100, y: 0 }, 5)!;
  const shape = pieceShapes(33, 310).find((p) => p.id === "xdr")!;
  const placed = placePiece(shape, end.point, end.heading);
  plan = addJunction(plan, index, shape.kind, [end.point.id, ...placed.ends.slice(1)], placed.at);
  const j = plan.junctions[0];
  assert.equal(j.kind, "double_crossover");
  assert.equal(j.ends[0], "p2");
  assert.equal(j.ends[2], "p4", "the lower track's end, not a new point");
  assert.equal(plan.points.length, 6);
});

test("removing track drops the points left with nothing", () => {
  let plan = addTrack(empty, indexPlan(empty), { x: 0, y: 0 }, { x: 100, y: 0 }, 0);
  plan = addTrack(plan, indexPlan(plan), "p2", { x: 200, y: 0 }, 0);
  plan = { ...plan, labels: [{ id: "l1", x: 0, y: 0, text: "Here" }] };
  const after = removeElements(plan, [
    { kind: "track", id: "t2" },
    { kind: "label", id: "l1" },
  ]);
  assert.deepEqual(
    after.points.map((p) => p.id),
    ["p1", "p2"],
  );
  assert.equal(after.labels.length, 0);
});

test("setBlock and updateElement change only what they name", () => {
  let plan = addTrack(empty, indexPlan(empty), { x: 0, y: 0 }, { x: 100, y: 0 }, 0);
  plan = addTrack(plan, indexPlan(plan), "p2", { x: 200, y: 0 }, 0);
  plan = setBlock(plan, [{ kind: "track", id: "t1" }], "a");
  assert.equal(plan.tracks[0].block, "a");
  assert.equal(plan.tracks[1].block, undefined);
  plan = setBlock(plan, [{ kind: "track", id: "t1" }], undefined);
  assert.equal("block" in plan.tracks[0], false);
  plan = updateElement(plan, { kind: "track", id: "t2" }, { layer: "upper" });
  assert.equal(plan.tracks[1].layer, "upper");
  plan = updateElement(plan, { kind: "track", id: "t2" }, { layer: undefined });
  assert.equal("layer" in plan.tracks[1], false);
});

/** Two tracks joined at p2, a curve on from there, a platform and a label. */
function scene(): TrackPlan {
  let plan = addTrack(empty, indexPlan(empty), { x: 0, y: 0 }, { x: 100, y: 0 }, 0);
  plan = addTrack(plan, indexPlan(plan), "p2", { x: 316, y: 216 }, 90);
  return { ...plan, platforms: [{ id: "f1", x: 50, y: -30, length: 100, width: 20 }], labels: [{ id: "l1", x: 0, y: -60, text: "Here" }] };
}

test("moving part of the track splits it from what stays", () => {
  const plan = moveSelection(scene(), [{ kind: "track", id: "t2" }], { x: 0, y: 50 });
  assert.equal(plan.tracks[0].b, "p2", "the track left behind keeps its point");
  assert.equal(plan.tracks[1].a, "p4", "the moved track ends at a point of its own");
  const index = indexPlan(plan);
  assert.deepEqual(index.points.get("p2"), { id: "p2", x: 100, y: 0 });
  assert.deepEqual(index.points.get("p4"), { id: "p4", x: 100, y: 50 });
  assert.deepEqual(index.points.get("p3"), { id: "p3", x: 316, y: 266 });
  const all = moveSelection(scene(), [
    { kind: "track", id: "t1" },
    { kind: "track", id: "t2" },
  ], { x: 10, y: 0 });
  assert.equal(all.points.length, 3, "track moved together stays joined");
  assert.equal(all.tracks[1].a, "p2");
});

test("turning a selection turns its track, platforms and labels about a point", () => {
  const refs = [
    { kind: "track" as const, id: "t1" },
    { kind: "track" as const, id: "t2" },
    { kind: "platform" as const, id: "f1" },
    { kind: "label" as const, id: "l1" },
  ];
  const plan = turnSelection(scene(), refs, 90, { x: 0, y: 0 });
  const index = indexPlan(plan);
  assert.deepEqual(index.points.get("p2"), { id: "p2", x: 0, y: 100 });
  const a = index.points.get("p2")!;
  const b = index.points.get("p3")!;
  const info = arcInfo(a, b, plan.tracks[1].angle);
  assert.ok(Math.abs(info.startHeading - 90) < 1e-6, `the curve now sets off south, not ${info.startHeading}`);
  assert.equal(plan.tracks[1].angle, 90, "a turn keeps a curve's angle");
  assert.deepEqual([plan.platforms[0].x, plan.platforms[0].y, plan.platforms[0].angle], [30, 50, 90]);
  assert.deepEqual([plan.labels[0].x, plan.labels[0].y], [60, 0]);
  const back = turnSelection(plan, refs, -90, { x: 0, y: 0 });
  assert.equal(back.platforms[0].angle, undefined, "a full turn back leaves no angle");
});

test("a box takes in what lies wholly inside it", () => {
  const plan = scene();
  const index = indexPlan(plan);
  const got = refsInBox(plan, index, { minX: -10, minY: -70, maxX: 110, maxY: 10 });
  assert.deepEqual(
    got.map((r) => `${r.kind}:${r.id}`),
    ["track:t1", "platform:f1", "label:l1"],
  );
  const bounds = selectionBounds(plan, index, got)!;
  assert.deepEqual(
    [bounds.minX, bounds.minY, bounds.maxX, bounds.maxY].map((v) => Math.round(v * 1e6) / 1e6 + 0),
    [0, -60, 100, 0],
  );
});

test("layers: put a selection on one, and pick everything on it", () => {
  const plan = setLayer(scene(), [
    { kind: "track", id: "t2" },
    { kind: "label", id: "l1" },
  ], "upper");
  assert.deepEqual(
    layerRefs(plan, "upper").map((r) => `${r.kind}:${r.id}`),
    ["track:t2", "label:l1"],
  );
  assert.equal(setLayer(plan, [{ kind: "track", id: "t2" }], undefined).tracks[1].layer, undefined);
});

test("turning about a selection's middle and back puts it back", () => {
  const refs = [
    { kind: "track" as const, id: "t1" },
    { kind: "track" as const, id: "t2" },
    { kind: "platform" as const, id: "f1" },
  ];
  const plan = scene();
  const about = selectionMiddle(plan, indexPlan(plan), refs)!;
  const turned = turnSelection(plan, refs, 37.5, about);
  const middle = selectionMiddle(turned, indexPlan(turned), refs)!;
  assert.ok(Math.abs(middle.x - about.x) < 0.05 && Math.abs(middle.y - about.y) < 0.05, "the middle stays put");
  const back = turnSelection(turned, refs, -37.5, middle);
  for (const p of plan.points) {
    const q = back.points.find((x) => x.id === p.id)!;
    assert.ok(Math.abs(q.x - p.x) < 0.05 && Math.abs(q.y - p.y) < 0.05, `${p.id} back at ${p.x}, ${p.y}, not ${q.x}, ${q.y}`);
  }
});
