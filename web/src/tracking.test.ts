import { test } from "node:test";
import assert from "node:assert/strict";
import type { Locomotive, TrackedTrain, Train } from "./api.ts";
import { leadArrow, lineMiddle, locomotiveName } from "./tracking.ts";

const loco = (type: number, number: number, name: string): Locomotive => ({ address: { type, number }, name, since: "" });
const roster = (id: string, name: string, address: number, long: boolean, inRoster = true) => ({ id, name, address, long, roster: inRoster }) as Train;

test("a reported locomotive takes the roster's name for its address", () => {
  const trains = { a: roster("a", "Local 1", 101, false), b: roster("b", "Long 101", 101, true), c: roster("101", "101", 3, false, false) };
  assert.equal(locomotiveName(loco(1, 101, "101 (short)"), trains), "Local 1");
  assert.equal(locomotiveName(loco(0, 101, "101"), trains), "Long 101");
  assert.equal(locomotiveName(loco(1, 3, "3 (short)"), trains), "3 (short)", "not in the roster");
  assert.equal(locomotiveName(loco(2, 101, "consist 101"), trains), "consist 101");
  const shared = { ...trains, d: roster("d", "Alco", 101, false) };
  assert.equal(locomotiveName(loco(1, 101, "101 (short)"), shared), "Alco / Local 1");
});

test("the middle of a line is halfway along it", () => {
  assert.deepEqual(lineMiddle([{ x: 0, y: 0 }, { x: 10, y: 0 }, { x: 10, y: 30 }]), { x: 10, y: 10 });
  assert.deepEqual(lineMiddle([{ x: 5, y: 5 }]), { x: 5, y: 5 });
  assert.equal(lineMiddle([]), undefined);
});

test("the arrow points out of the leading end", () => {
  const t = { line: [{ x: 0, y: 0 }, { x: 100, y: 0 }], direction: "forward" } as TrackedTrain;
  const fwd = leadArrow(t, 10)!;
  assert.deepEqual(fwd[0], { x: 106, y: 0 });
  assert.ok(fwd[1].x < 100 && fwd[2].x < 100);
  const back = leadArrow({ ...t, direction: "reverse" }, 10)!;
  assert.deepEqual(back[0], { x: -6, y: 0 });
  assert.equal(leadArrow({ ...t, line: [{ x: 1, y: 1 }] }, 10), undefined, "a point has no way");
});
