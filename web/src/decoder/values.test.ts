import { test } from "node:test";
import assert from "node:assert/strict";
import type { DecoderDefinition, DecoderVariable } from "./types.ts";
import {
  addressItems,
  addressOf,
  applyUpdates,
  choicesFor,
  compositeIndex,
  currentAddress,
  cvsOf,
  decode,
  encode,
  field,
  initialTable,
  knownValues,
  merge,
  qualified,
  setAddress,
  setSpeedPoint,
  settle,
  variableMap,
  withField,
  withSaved,
  type CVTable,
} from "./values.ts";

const v = (x: Partial<DecoderVariable> & Pick<DecoderVariable, "item" | "type">): DecoderVariable => ({ label: x.item, min: 0, ...x });

function table(values: Record<string, number>): CVTable {
  const t: CVTable = {};
  for (const [cv, value] of Object.entries(values)) t[cv] = { value, state: "read", known: true, edited: 0 };
  return t;
}

function apply(t: CVTable, r: ReturnType<typeof encode>): CVTable {
  if ("error" in r) throw new Error(r.error);
  return applyUpdates(t, r.updates, "edited");
}

const none = new Map<string, DecoderVariable>();

test("bit and radix fields", () => {
  const bits = { cv: "29", mask: 0b00100000, shift: 5, width: 1 };
  assert.equal(field(bits, 0b00100110), 1);
  assert.equal(withField(bits, 0b00000110, 1), 0b00100110);
  assert.equal(withField(bits, 0b00100110, 0), 0b00000110);
  // Base-3 digits: radix 9 is the third digit.
  const digit = { cv: "158", radix: 9 };
  assert.equal(field(digit, 2 * 9 + 1 * 3 + 2, 3), 2);
  assert.equal(withField(digit, 2 * 9 + 1 * 3 + 2, 0, 3), 1 * 3 + 2);
});

test("numbers with an offset", () => {
  const start = v({ item: "Start", type: "decimal", max: 255, offset: 1, factor: 1, parts: [{ cv: "2", mask: 0xff, width: 8 }] });
  let t = table({ "2": 9 });
  assert.equal(decode(start, t), 10);
  t = apply(t, encode(start, 21, t, none));
  assert.equal(t["2"].value, 20);
  assert.equal(t["2"].state, "edited");
  assert.deepEqual(encode(start, 300, t, none), { error: "Enter a number from 0 to 255." });
  assert.deepEqual(encode(start, 2.5, t, none), { error: "Enter a whole number." });
});

test("split values across masked CVs", () => {
  // Low six bits in CV 5, three more in CV 6, shown plus one.
  const addr = v({
    item: "Addr2",
    type: "split",
    offset: 1,
    factor: 1,
    parts: [
      { cv: "5", mask: 0x3f, width: 6 },
      { cv: "6", mask: 0x07, width: 3 },
    ],
  });
  let t = table({ "5": 0b11_000001, "6": 0b11111_010 });
  assert.equal(decode(addr, t), 1 + 1 + 2 * 64);
  t = apply(t, encode(addr, 1 + 63 + 64 * 5, t, none));
  assert.equal(t["5"].value, 0b11_111111);
  assert.equal(t["6"].value, 0b11111_101);
  assert.equal(t["5"].edited, 0x3f);
});

test("wide splits beyond 32 bits", () => {
  const serial = v({
    item: "Serial",
    type: "split",
    factor: 1,
    parts: ["1", "2", "3", "4", "5"].map((cv) => ({ cv, mask: 0xff, width: 8 })),
  });
  const t = table({ "1": 1, "2": 0, "3": 0, "4": 0, "5": 1 });
  assert.equal(decode(serial, t), 1 + 2 ** 32);
});

test("enums keep unknown values", () => {
  const mode = v({ item: "Mode", type: "enum", choices: [{ label: "Off", value: 0 }, { label: "On", value: 1 }], parts: [{ cv: "50", mask: 0x03, width: 2 }] });
  const t = table({ "50": 3 });
  assert.equal(decode(mode, t), 3);
  assert.deepEqual(choicesFor(mode, 3).at(-1), { label: "Reserved value 3", value: 3 });
});

test("long addresses", () => {
  const long = v({ item: "Long Address", type: "long_address", parts: [{ cv: "17", mask: 0xff, width: 8 }, { cv: "18", mask: 0xff, width: 8 }] });
  let t = table({ "17": 0, "18": 0 });
  t = apply(t, encode(long, 6641, t, none));
  assert.equal(t["17"].value, 0xc0 | (6641 >> 8));
  assert.equal(t["18"].value, 6641 & 0xff);
  assert.equal(decode(long, t), 6641);
  assert.ok("error" in encode(long, 10240, t, none));
});

test("setting an address picks short or long", () => {
  const vars = new Map<string, DecoderVariable>([
    ["Short Address", v({ item: "Short Address", type: "short_address", max: 127, factor: 1, parts: [{ cv: "1", mask: 0xff, width: 8 }] })],
    ["Address Format", v({ item: "Address Format", type: "enum", choices: [{ label: "Short", value: 0 }, { label: "Long", value: 1 }], parts: [{ cv: "29", mask: 0x20, shift: 5, width: 1 }] })],
    ["Long Address", v({ item: "Long Address", type: "long_address", parts: [{ cv: "17", mask: 0xff, width: 8 }, { cv: "18", mask: 0xff, width: 8 }] })],
  ]);
  const a = addressItems(vars);
  let t = table({ "1": 3, "29": 0x06, "17": 0xc0, "18": 0 });
  assert.equal(currentAddress(a, t, vars), 3);
  t = apply(t, setAddress(a, 6641, t, vars));
  assert.equal(t["29"].value, 0x26);
  assert.equal(currentAddress(a, t, vars), 6641);
  t = apply(t, setAddress(a, 44, t, vars));
  assert.equal(t["29"].value, 0x06);
  assert.equal(t["1"].value, 44);
  assert.equal(currentAddress(a, t, vars), 44);
});

test("text in consecutive CVs", () => {
  const name = v({ item: "Name", type: "split_text", text: { term_byte: 0, pad_byte: 0 }, parts: ["1", "2", "3", "4", "5"].map((cv) => ({ cv, mask: 0xff, width: 8 })) });
  let t = table({ "1": 0, "2": 0, "3": 0, "4": 0, "5": 0 });
  t = apply(t, encode(name, "GP40", t, none));
  assert.deepEqual([1, 2, 3, 4, 5].map((n) => t[String(n)].value), [71, 80, 52, 48, 0]);
  assert.equal(decode(name, t), "GP40");
});

test("dates count from the base", () => {
  const made = v({ item: "Made", type: "split_date_time", date_time: { base: "2000-01-01T00:00:00", unit: "Seconds", factor: 1, display: "dateOnly" }, parts: ["1", "2", "3", "4"].map((cv) => ({ cv, mask: 0xff, width: 8 })) });
  const day = 86400 * 366;
  const t = table({ "1": day & 0xff, "2": (day >> 8) & 0xff, "3": (day >> 16) & 0xff, "4": (day >> 24) & 0xff });
  assert.equal(decode(made, t), "2001-01-01");
});

test("two-digit pairs", () => {
  const n = v({ item: "Number", type: "split_hundreds", max: 2047, parts: [{ cv: "201", mask: 0xff, width: 8 }, { cv: "181", mask: 0xff, width: 8 }] });
  let t = table({ "201": 0, "181": 0 });
  t = apply(t, encode(n, 2047, t, none));
  assert.equal(t["201"].value, 47);
  assert.equal(t["181"].value, 20);
  assert.equal(decode(n, t), 2047);
});

test("speed tables stay rising", () => {
  const st = v({ item: "Table", type: "speed_table", speed_table: { entries: 5, min: 0, max: 100 } });
  assert.deepEqual(setSpeedPoint(st, [0, 25, 50, 75, 100], 2, 90), [0, 25, 90, 90, 100]);
  assert.deepEqual(setSpeedPoint(st, [0, 25, 50, 75, 100], 2, 10), [0, 10, 10, 75, 100]);
  const mfx = v({ item: "Table", type: "speed_table", speed_table: { entries: 3, min: 1, max: 255, mfx: true } });
  assert.deepEqual(setSpeedPoint(mfx, [1, 128, 255], 0, 50), [1, 128, 255]);
});

test("qualifiers compare the shown value", () => {
  const mode = v({ item: "Mode", type: "enum", choices: [{ label: "Off", value: 0 }, { label: "Blink", value: 7 }], parts: [{ cv: "50", mask: 0xff, width: 8 }] });
  const vars = new Map([["Mode", mode]]);
  const t = table({ "50": 7 });
  assert.equal(qualified([{ item: "Mode", relation: "eq", value: 7 }], t, vars), true);
  assert.equal(qualified([{ item: "Mode", relation: "ne", value: 0 }, { item: "Mode", relation: "lt", value: 7 }], t, vars), false);
  assert.equal(qualified([{ relation: "never" }], t, vars), false);
  assert.equal(qualified([{ item: "Gone", relation: "eq", value: 1 }], t, vars), true);
});

test("presets set several settings", () => {
  const a = v({ item: "A", type: "enum", choices: [], parts: [{ cv: "1", mask: 0x01, width: 1 }] });
  const b = v({ item: "B", type: "decimal", max: 255, factor: 1, parts: [{ cv: "2", mask: 0xff, width: 8 }] });
  const preset = v({
    item: "P",
    type: "composite",
    composite: [
      { label: "Off", settings: [{ item: "A", value: 0 }, { item: "B", value: 0 }] },
      { label: "Busy", settings: [{ item: "A", value: 1 }, { item: "B", value: 20 }] },
    ],
  });
  const vars = new Map([
    ["A", a],
    ["B", b],
    ["P", preset],
  ]);
  let t = table({ "1": 0, "2": 5 });
  assert.equal(compositeIndex(preset, t, vars), -1);
  t = apply(t, encode(preset, 1, t, vars));
  assert.equal(compositeIndex(preset, t, vars), 1);
  assert.deepEqual(cvsOf(["P"], vars), ["1", "2"]);
});

test("defaults build shared CVs", () => {
  const def: DecoderDefinition = {
    file: "x.xml",
    family: "X",
    manufacturer: "X",
    model: "X",
    num_outs: -1,
    num_fns: -1,
    max_fn_num: 28,
    programming: { direct: "yes", paged: true, register: true },
    cvs: [{ name: "29" }, { name: "2" }, { name: "67" }, { name: "68" }, { name: "69" }, { name: "9" }],
    panes: [],
    variables: [
      v({ item: "Direction", type: "enum", default: "1", parts: [{ cv: "29", mask: 0x01, width: 1 }] }),
      v({ item: "Steps", type: "enum", default: "1", parts: [{ cv: "29", mask: 0x02, shift: 1, width: 1 }] }),
      v({ item: "Start", type: "decimal", max: 255, default: "300", factor: 1, parts: [{ cv: "2", mask: 0xff, width: 8 }] }),
      v({ item: "Table", type: "speed_table", speed_table: { entries: 3, min: 0, max: 255 }, parts: ["67", "68", "69"].map((cv) => ({ cv, mask: 0xff, width: 8 })) }),
    ],
  };
  const t = initialTable(def);
  assert.equal(t["29"].value, 3);
  assert.equal(t["29"].state, "fromfile");
  assert.equal(t["2"].value, 255, "a default above the maximum is clamped");
  assert.deepEqual([t["67"].value, t["68"].value, t["69"].value], [0, 127, 255]);
  assert.equal(t["9"].state, "unknown");
  assert.equal(variableMap(def).size, 4);
});

test("an edit to a never-read CV merges with what the decoder holds", () => {
  const bit = v({ item: "Bit", type: "enum", parts: [{ cv: "29", mask: 0x20, shift: 5, width: 1 }] });
  let t: CVTable = { "29": { value: 0, state: "fromfile", known: false, edited: 0 } };
  t = apply(t, encode(bit, 1, t, none));
  assert.equal(merge(t["29"], 0x06), 0x26);
  t = settle(t, "29", 0x26, "stored");
  assert.deepEqual(t["29"], { value: 0x26, state: "stored", known: true, edited: 0 });
});

test("saved values count as known and are saved back", () => {
  const short = v({ item: "Short Address", type: "short_address", parts: [{ cv: "1", mask: 0x7f, shift: 0, width: 7 }], max: 127 });
  const format = v({ item: "Address Format", type: "enum", parts: [{ cv: "29", mask: 0x20, shift: 5, width: 1 }], choices: [{ label: "Short", value: 0 }, { label: "Long", value: 1 }] });
  const long = v({ item: "Long Address", type: "long_address", parts: [{ cv: "17", mask: 0x3f, shift: 0, width: 6 }, { cv: "18", mask: 0xff, shift: 0, width: 8 }] });
  const vars = new Map([short, format, long].map((x) => [x.item, x]));
  let t: CVTable = { "1": { value: 0, state: "unknown", known: false, edited: 0 } };
  t = withSaved(t, { "1": 3, "29": 0x26, "17": 0xd9, "18": 0xf1 });
  assert.equal(t["1"].state, "saved");
  assert.equal(t["1"].known, true);
  assert.deepEqual(addressOf(addressItems(vars), t, vars), { address: 6641, long: true });
  t = applyUpdates(t, [{ cv: "1", value: 5, bits: 0x7f }], "edited");
  assert.deepEqual(knownValues(t), { "29": 0x26, "17": 0xd9, "18": 0xf1 });
});
