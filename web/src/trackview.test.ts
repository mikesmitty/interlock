import { test } from "node:test";
import assert from "node:assert/strict";
import { MAX_ZOOM, MIN_ZOOM, fitView, panBy, pinchView, toPlan, viewBounds, zoomAt, zoomBy } from "./trackview.ts";

const size = { x: 800, y: 400 };
const near = (a: { x: number; y: number }, b: { x: number; y: number }) => Math.abs(a.x - b.x) < 1e-9 && Math.abs(a.y - b.y) < 1e-9;

test("a fitted view shows all of the area, centered", () => {
  const view = fitView({ minX: 100, minY: 0, maxX: 2100, maxY: 500 }, size);
  assert.deepEqual(view, { x: 1100, y: 250, zoom: 0.4 }, "the width decides: 2000 mm across 800 pixels");
  const b = viewBounds(view, size);
  assert.deepEqual([b.minX, b.maxX], [100, 2100]);
  assert.ok(b.minY <= 0 && b.maxY >= 500);
  assert.equal(fitView({ minX: 0, minY: 0, maxX: 1, maxY: 1 }, size).zoom, MAX_ZOOM);
  assert.equal(fitView({ minX: 0, minY: 0, maxX: 1e7, maxY: 1 }, size).zoom, MIN_ZOOM);
});

test("zooming keeps the plan under the pointer where it was", () => {
  const view = { x: 0, y: 0, zoom: 1 };
  const at = { x: 700, y: 50 };
  const before = toPlan(view, size, at);
  assert.deepEqual(before, { x: 300, y: -150 });
  const zoomed = zoomAt(view, size, 4, at);
  assert.equal(zoomed.zoom, 4);
  assert.ok(near(toPlan(zoomed, size, at), before));
  assert.equal(zoomAt(zoomed, size, 1e6, at).zoom, MAX_ZOOM, "only so far in");
  assert.ok(near(toPlan(zoomAt(zoomed, size, 1e6, at), size, at), before), "and the pointer still holds its place");
  assert.deepEqual(zoomBy(view, 2), { x: 0, y: 0, zoom: 2 }, "the buttons zoom about the middle");
});

test("the plan follows a drag and a pinch", () => {
  const view = { x: 0, y: 0, zoom: 2 };
  const moved = panBy(view, { x: 100, y: -40 });
  assert.ok(near(toPlan(moved, size, { x: 500, y: 160 }), toPlan(view, size, { x: 400, y: 200 })));
  const from = { x: 300, y: 100 };
  const to = { x: 350, y: 220 };
  const pinched = pinchView(view, size, from, to, 3);
  assert.equal(pinched.zoom, 6);
  assert.ok(near(toPlan(pinched, size, to), toPlan(view, size, from)));
});
