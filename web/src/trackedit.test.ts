import { test } from "node:test";
import assert from "node:assert/strict";
import type { TrackPlan } from "./api.ts";
import { addJunction, addTrack, endNear, indexPlan, openEnds, pieceShapes, placePiece, removeElements, setBlock, updateElement } from "./trackgeom.ts";

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
