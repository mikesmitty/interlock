import { test } from "node:test";
import assert from "node:assert/strict";
import type { Drive } from "./api.ts";
import { driveActive, driveText, stepsUpTo } from "./driving.ts";

const blockName = (id: string) => ({ park: "Park Av outer" })[id] ?? id;
const drive = (extra: Partial<Drive>): Drive => ({
  train: "local-1",
  name: "Local 1",
  kind: "drive",
  to: "park",
  marker: "m1",
  routes: [],
  direction: "forward",
  speed: 60,
  step: 60,
  braking: 40,
  state: "running",
  remaining: 1234.4,
  started: "",
  updated: "",
  ...extra,
});

test("a drive under way says how far it has to go", () => {
  assert.equal(driveText(drive({}), blockName), "Running to Park Av outer, 1234 mm to go");
  assert.equal(driveText(drive({ state: "braking", remaining: 80 }), blockName), "Braking for Park Av outer, 80 mm to go");
  assert.ok(driveActive(drive({ state: "stopping" })));
  assert.ok(!driveActive(drive({ state: "arrived" })) && !driveActive(undefined));
});

test("a drive that arrived says how near the marker it stopped", () => {
  assert.equal(driveText(drive({ state: "arrived", off: 0.4 }), blockName), "Stopped in Park Av outer at the marker");
  assert.equal(driveText(drive({ state: "arrived", off: 3.2 }), blockName), "Stopped in Park Av outer, 3 mm short of the marker");
  assert.equal(driveText(drive({ state: "arrived", off: -6, marker: undefined }), blockName), "Stopped in Park Av outer, 6 mm past where the track ends");
});

test("a drive that ended early says why", () => {
  assert.equal(driveText(drive({ state: "stopped", error: "A throttle took it over." }), blockName), "Stopped: A throttle took it over.");
  assert.equal(driveText(drive({ state: "stopped" }), blockName), "Stopped when asked");
});

test("a speed measuring run says how far along it is", () => {
  const m = drive({ kind: "measure", to: undefined, measuring: { steps: [20, 40, 60], at: 40, done: [20] } });
  assert.equal(driveText(m, blockName), "Measuring its speed at step 40, 1 of 3 measured");
  assert.equal(
    driveText({ ...m, state: "measured", measuring: { steps: [20, 40, 60], done: [20, 40], skipped: [60] } }, blockName),
    "Speeds measured at step 20, 40; step 60 couldn't be measured",
  );
});

test("the steps measured are spread evenly up to the fastest", () => {
  assert.deepEqual(stepsUpTo(100), [17, 33, 50, 67, 83, 100]);
  assert.deepEqual(stepsUpTo(3), [1, 2, 3]);
  assert.deepEqual(stepsUpTo(500, 2), [63, 126]);
});
