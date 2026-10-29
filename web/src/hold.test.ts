import { test } from "node:test";
import assert from "node:assert/strict";
import { releaseLeavesItSet, resumesAt, speedText } from "./hold.ts";

const held = { emergency_stop: true };
const running = { emergency_stop: false };

test("a held train goes back to its speed, one stopped on its own stays put", () => {
  assert.equal(resumesAt({ speed: 40, direction: "forward", estop: false }, held), 40);
  assert.equal(resumesAt({ speed: 0, direction: "forward", estop: true }, held), 0);
  assert.equal(resumesAt({ speed: 40, direction: "forward", estop: false }, running), 0);
});

test("the speed says when the hold stops the train", () => {
  assert.equal(speedText({ speed: 40, direction: "reverse", estop: false }, held), "held 40 rev");
  assert.equal(speedText({ speed: 0, direction: "forward", estop: false }, held), "held 0");
  assert.equal(speedText({ speed: 40, direction: "forward", estop: false }, running), "40 fwd");
  assert.equal(speedText({ speed: 0, direction: "forward", estop: true }, held), "stop!");
});

test("releasing a held train set to a speed needs a warning", () => {
  const t = { speed: 25, direction: "forward" as const, estop: false };
  assert.equal(releaseLeavesItSet(t, held, false), true);
  assert.equal(releaseLeavesItSet({ ...t, speed: 0 }, held, false), false);
  assert.equal(releaseLeavesItSet(t, running, false), false);
  // A consist member follows its lead.
  assert.equal(releaseLeavesItSet(t, held, true), false);
});
