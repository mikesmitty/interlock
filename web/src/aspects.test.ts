import { test } from "node:test";
import assert from "node:assert/strict";
import { aspectState, aspectWord } from "./aspects.ts";

test("each aspect has its word and lamp", () => {
  assert.deepEqual(aspectState("red"), { word: "Red", color: "red" });
  assert.deepEqual(aspectState("yellow"), { word: "Yellow", color: "amber" });
  assert.deepEqual(aspectState("lunar"), { word: "Lunar", color: "lunar" });
  assert.equal(aspectState("dark").color, "off");
  assert.equal(aspectWord("s"), "S");
});

test("a flashing aspect pulses in its color", () => {
  assert.deepEqual(aspectState("flashing_red"), { word: "Flashing red", color: "red", pulse: true });
  assert.equal(aspectState("green").pulse, undefined);
});

test("a state that isn't an aspect reads as unknown", () => {
  assert.deepEqual(aspectState("unknown"), { color: "off", word: "Unknown" });
  assert.equal(aspectWord("sideways"), "Unknown");
});
