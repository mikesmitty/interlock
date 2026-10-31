import { test } from "node:test";
import assert from "node:assert/strict";
import type { SignalState } from "./api.ts";
import { aspectState, aspectWord, signalAspects, signalKind, signalMeaning } from "./aspects.ts";

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

const home = (over: Partial<SignalState>): SignalState => ({ id: "s1", kind: "home", indication: "clear", upper: "green", lower: "green", ...over });

test("a signal says what it shows and what that means", () => {
  assert.equal(signalKind(home({})), "Home signal");
  assert.equal(signalKind({ id: "s", kind: "automatic", indication: "clear", upper: "green" }), "Automatic signal");
  assert.equal(signalKind({ id: "s", indication: "stop", upper: "red" }), "Signal");
  assert.equal(signalKind(undefined), "Signal");

  assert.equal(signalAspects(home({ lower: "yellow" })), "Green over yellow");
  assert.equal(signalAspects({ id: "s", kind: "automatic", indication: "approach", upper: "yellow" }), "Yellow");

  assert.equal(signalMeaning(home({})), "Proceed, on the straight route. The next signal is clear.");
  assert.equal(
    signalMeaning(home({ indication: "approach", upper: "yellow", diverging: true, lower: "yellow" })),
    "Proceed, on the diverging route, ready to stop at the next signal.",
  );
  assert.equal(signalMeaning({ id: "s", kind: "automatic", indication: "approach", upper: "yellow" }), "Proceed, ready to stop at the next signal.");
  assert.equal(signalMeaning(home({ indication: "stop", reason: "No route is set." })), "Stop. No route is set.");
});
