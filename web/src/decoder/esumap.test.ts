import { test } from "node:test";
import assert from "node:assert/strict";
import { conditionText, esuCell, esuCVs, esuRowCVs, esuSummary, esuUpdate, esuValue } from "./esumap.ts";
import type { ESUMap } from "./types.ts";
import type { CVTable } from "./values.ts";

const names = (prefix: string, n: number) => Array.from({ length: n }, (_, i) => ({ name: `${prefix} ${i + 1}` }));

/** A V5 map as the server lays it out (internal/decoder/esumap.go). */
const v5: ESUMap = {
  rows: 72,
  blocks: [
    {
      name: "Conditions",
      bits: 2,
      page: 3,
      start: 257,
      items: [
        { name: "Motion", on: "Moving", off: "Stopped" },
        { name: "Direction", on: "Forward", off: "Reverse" },
        ...Array.from({ length: 32 }, (_, i) => ({ name: `F${i}` })),
        { name: "Wheel sensor" },
        { name: "", hidden: true },
        ...names("Sensor", 4),
      ],
    },
    { name: "Physical outputs", bits: 1, page: 8, start: 257, items: [{ name: "Headlight [1]" }, { name: "Rear light [1]" }, ...names("AUX", 22)] },
    { name: "Logical functions", bits: 1, page: 8, start: 260, items: names("Logical function", 24) },
    { name: "Sounds", bits: 1, page: 8, start: 263, items: names("Sound slot", 32) },
  ],
};

const known = (cvs: Record<string, number>): CVTable =>
  Object.fromEntries(Object.entries(cvs).map(([cv, value]) => [cv, { value, state: "read", known: true, edited: 0 }]));

test("cells follow the server's layout", () => {
  assert.deepEqual(esuCell(v5, 0, 0, 1), { cv: "16.3.257", mask: 0x0c, shift: 2 });
  assert.deepEqual(esuCell(v5, 0, 0, 39), { cv: "16.3.266", mask: 0xc0, shift: 6 });
  assert.deepEqual(esuCell(v5, 16, 0, 0), { cv: "16.4.257", mask: 0x03, shift: 0 });
  assert.deepEqual(esuCell(v5, 0, 3, 31), { cv: "16.8.266", mask: 0x80, shift: 7 });
  assert.deepEqual(esuCell(v5, 17, 3, 0), { cv: "16.9.279", mask: 0x01, shift: 0 });
  assert.equal(esuRowCVs(v5, 0).length, 20);
  const noSounds = { ...v5, blocks: v5.blocks.map((b, i) => (i === 3 ? { ...b, items: b.items.map(() => ({ name: "", hidden: true })) } : b)) };
  assert.equal(esuRowCVs(noSounds, 0).length, 16, "CVs of hidden items only are left out");
  assert.equal(esuCVs(v5).length, 72 * 20);
});

test("a row reads as its conditions and what it switches on", () => {
  // Row 1 of a LokSound 5 sound project: moving forward with F0 on
  // lights the headlight.
  const t = known({ "16.3.257": 0b00010101, "16.8.257": 0b1, "16.8.263": 0b100 });
  assert.equal(esuValue(v5, t, 0, 0, 0), 1);
  assert.deepEqual(esuSummary(v5, t, 0), {
    conditions: ["Moving", "Forward", "F0"],
    switches: [
      { block: "Physical outputs", names: ["Headlight [1]"] },
      { block: "Sounds", names: ["Sound slot 3"] },
    ],
  });
  assert.deepEqual(esuSummary(v5, {}, 1), { conditions: [], switches: [] });
});

test("changing an item keeps the CV's other bits", () => {
  const t = known({ "16.3.257": 0b00010101 });
  assert.deepEqual(esuUpdate(v5, t, 0, 0, 1, 2), { cv: "16.3.257", value: 0b00011001, bits: 0x0c });
  assert.deepEqual(esuUpdate(v5, t, 0, 1, 0, 1), { cv: "16.8.257", value: 1, bits: 0x01 });
});

test("conditions read as their states", () => {
  assert.equal(conditionText({ name: "Motion", on: "Moving", off: "Stopped" }, 2), "Stopped");
  assert.equal(conditionText({ name: "F3" }, 2), "not F3");
  assert.equal(conditionText({ name: "F3" }, 3), "F3 (value 3)");
});
