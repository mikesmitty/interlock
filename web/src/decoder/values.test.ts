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
  discard,
  pendingChanges,
  withChanges,
  variableMap,
  withField,
  withImported,
  withSaved,
  isImported,
  sourceOf,
  withSession,
  endSession,
  loadProfile,
  confirmed,
  allConfirmed,
  pagesToRead,
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
  const mode = v({
    item: "Mode",
    type: "enum",
    choices: [
      { label: "Off", value: 0 },
      { label: "On", value: 1 },
    ],
    parts: [{ cv: "50", mask: 0x03, width: 2 }],
  });
  const t = table({ "50": 3 });
  assert.equal(decode(mode, t), 3);
  assert.deepEqual(choicesFor(mode, 3).at(-1), { label: "Reserved value 3", value: 3 });
});

test("long addresses", () => {
  const long = v({
    item: "Long Address",
    type: "long_address",
    parts: [
      { cv: "17", mask: 0xff, width: 8 },
      { cv: "18", mask: 0xff, width: 8 },
    ],
  });
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
    [
      "Address Format",
      v({
        item: "Address Format",
        type: "enum",
        choices: [
          { label: "Short", value: 0 },
          { label: "Long", value: 1 },
        ],
        parts: [{ cv: "29", mask: 0x20, shift: 5, width: 1 }],
      }),
    ],
    [
      "Long Address",
      v({
        item: "Long Address",
        type: "long_address",
        parts: [
          { cv: "17", mask: 0xff, width: 8 },
          { cv: "18", mask: 0xff, width: 8 },
        ],
      }),
    ],
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
  assert.deepEqual(
    [1, 2, 3, 4, 5].map((n) => t[String(n)].value),
    [71, 80, 52, 48, 0],
  );
  assert.equal(decode(name, t), "GP40");
});

test("dates count from the base", () => {
  const made = v({
    item: "Made",
    type: "split_date_time",
    date_time: { base: "2000-01-01T00:00:00", unit: "Seconds", factor: 1, display: "dateOnly" },
    parts: ["1", "2", "3", "4"].map((cv) => ({ cv, mask: 0xff, width: 8 })),
  });
  const day = 86400 * 366;
  const t = table({ "1": day & 0xff, "2": (day >> 8) & 0xff, "3": (day >> 16) & 0xff, "4": (day >> 24) & 0xff });
  assert.equal(decode(made, t), "2001-01-01");
});

test("two-digit pairs", () => {
  const n = v({
    item: "Number",
    type: "split_hundreds",
    max: 2047,
    parts: [
      { cv: "201", mask: 0xff, width: 8 },
      { cv: "181", mask: 0xff, width: 8 },
    ],
  });
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
  const mode = v({
    item: "Mode",
    type: "enum",
    choices: [
      { label: "Off", value: 0 },
      { label: "Blink", value: 7 },
    ],
    parts: [{ cv: "50", mask: 0xff, width: 8 }],
  });
  const vars = new Map([["Mode", mode]]);
  const t = table({ "50": 7 });
  assert.equal(qualified([{ item: "Mode", relation: "eq", value: 7 }], t, vars), true);
  assert.equal(
    qualified(
      [
        { item: "Mode", relation: "ne", value: 0 },
        { item: "Mode", relation: "lt", value: 7 },
      ],
      t,
      vars,
    ),
    false,
  );
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
      {
        label: "Off",
        settings: [
          { item: "A", value: 0 },
          { item: "B", value: 0 },
        ],
      },
      {
        label: "Busy",
        settings: [
          { item: "A", value: 1 },
          { item: "B", value: 20 },
        ],
      },
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
  const format = v({
    item: "Address Format",
    type: "enum",
    parts: [{ cv: "29", mask: 0x20, shift: 5, width: 1 }],
    choices: [
      { label: "Short", value: 0 },
      { label: "Long", value: 1 },
    ],
  });
  const long = v({
    item: "Long Address",
    type: "long_address",
    parts: [
      { cv: "17", mask: 0x3f, shift: 0, width: 6 },
      { cv: "18", mask: 0xff, shift: 0, width: 8 },
    ],
  });
  const vars = new Map([short, format, long].map((x) => [x.item, x]));
  let t: CVTable = { "1": { value: 0, state: "unknown", known: false, edited: 0 } };
  t = withSaved(t, { "1": 3, "29": 0x26, "17": 0xd9, "18": 0xf1 });
  assert.equal(t["1"].state, "saved");
  assert.equal(t["1"].known, true);
  assert.deepEqual(addressOf(addressItems(vars), t, vars), { address: 6641, long: true });
  t = applyUpdates(t, [{ cv: "1", value: 5, bits: 0x7f }], "edited");
  assert.deepEqual(knownValues(t), { "1": 3, "29": 0x26, "17": 0xd9, "18": 0xf1 }, "an edited CV saves what the decoder holds");
});

test("imported values show but are not known until read", () => {
  let t: CVTable = withImported(withSaved({}, { "1": 3 }), { "29": 0x06, "3": 10 });
  assert.deepEqual(t["29"], { value: 0x06, state: "imported", known: false, edited: 0 });
  assert.equal(isImported(t["29"]), true);
  assert.equal(isImported(t["1"]), false);
  assert.deepEqual(knownValues(t), { "1": 3 }, "only values from the decoder are saved back");

  // An edit to one bit stays unknown, so writing it reads the decoder first.
  t = applyUpdates(t, [{ cv: "29", value: 0x26, bits: 0x20 }], "edited");
  assert.deepEqual(t["29"], { value: 0x26, state: "edited", known: false, edited: 0x20, prior: { value: 0x06, state: "imported" } });
  assert.equal(isImported(t["29"]), true);
  assert.equal(merge(t["29"], 0x0e), 0x2e, "the decoder's other bits win over the imported ones");
  assert.deepEqual(discard(t, ["29"])["29"], { value: 0x06, state: "imported", known: false, edited: 0 });

  // A read replaces the imported value.
  t = settle(t, "3", 12, "read");
  assert.deepEqual(t["3"], { value: 12, state: "read", known: true, edited: 0 });
  assert.equal(isImported(t["3"]), false);
});

test("a setting shows the least settled state of its CVs", () => {
  let t: CVTable = withImported(withSaved({}, { "1": 3, "17": 0xc0 }), { "29": 0x22 });
  t = settle(settle(t, "17", 0xc0, "read"), "18", 150, "stored");
  t["2"] = { value: 0, state: "fromfile", known: false, edited: 0 };
  assert.equal(sourceOf(["17"], t), "read");
  assert.equal(sourceOf(["17", "18"], t), "stored", "read and written show as written");
  assert.equal(sourceOf(["1", "17"], t), "saved", "a value from an earlier visit outranks one read now");
  assert.equal(sourceOf(["18", "29"], t), "imported");
  assert.equal(sourceOf(["2", "29"], t), "fromfile");
  assert.equal(sourceOf(["17", "99"], t), "unknown", "a CV with no value is not read");
  assert.equal(sourceOf(["17"], applyUpdates(t, [{ cv: "17", value: 4, bits: 0xff }], "edited")), "edited");
  assert.equal(sourceOf([], t), undefined);
});

test("a session marks what it read and wrote until it ends", () => {
  let t: CVTable = withSaved({}, { "1": 3, "29": 0x22, "3": 4, "4": 2 });
  t = applyUpdates(t, [{ cv: "3", value: 5, bits: 0xff }], "edited");
  t = withSession(t, { read: ["1", "3"], written: ["29"] });
  assert.equal(t["1"].state, "read");
  assert.equal(t["29"].state, "stored");
  assert.equal(t["4"].state, "saved", "a value from before the session stays saved");
  assert.deepEqual(t["3"].prior, { value: 4, state: "read" }, "an edited CV keeps what the session read under it");
  t = settle(t, "4", 2, "read");
  t = endSession(t);
  assert.deepEqual(
    ["1", "29", "4"].map((cv) => t[cv].state),
    ["saved", "saved", "saved"],
  );
  assert.deepEqual(t["3"].prior, { value: 4, state: "saved" });
  assert.equal(t["3"].state, "edited", "changes not written yet outlast the session");
  assert.equal(t["1"].known, true);
});

test("changes not written yet are kept, survive a read and can be undone", () => {
  let t: CVTable = withSaved({}, { "29": 0x06, "3": 10 });
  t = applyUpdates(t, [{ cv: "29", value: 0x26, bits: 0x20 }], "edited");
  t = applyUpdates(t, [{ cv: "29", value: 0x27, bits: 0x01 }], "edited");
  assert.deepEqual(t["29"], { value: 0x27, state: "edited", known: true, edited: 0x21, prior: { value: 0x06, state: "saved" } });
  assert.deepEqual(pendingChanges(t), { "29": { value: 0x21, bits: 0x21 } });

  // Kept by the roster and laid over the saved values again.
  let reopened = withChanges(withSaved({}, { "29": 0x0e }), pendingChanges(t));
  assert.deepEqual(reopened["29"], { value: 0x2f, state: "edited", known: true, edited: 0x21, prior: { value: 0x0e, state: "saved" } });

  // A read keeps the change over the decoder's other bits...
  reopened = settle(reopened, "29", 0x0c, "read");
  assert.deepEqual(reopened["29"], { value: 0x2d, state: "edited", known: true, edited: 0x21, prior: { value: 0x0c, state: "read" } });
  // ...and undoing it goes back to what was read.
  assert.deepEqual(discard(reopened, ["29", "3"])["29"], { value: 0x0c, state: "read", known: true, edited: 0 });
  // A read that finds the change already made settles it.
  assert.deepEqual(settle(reopened, "29", 0x21, "read")["29"], { value: 0x21, state: "read", known: true, edited: 0 });
});

test("a kept profile loads as changes where it differs", () => {
  let t: CVTable = withImported(withSaved({}, { "1": 3, "3": 10, "29": 6 }), { "4": 8 });
  t = applyUpdates(t, [{ cv: "1", value: 4, bits: 0xff }], "edited");
  t["5"] = { value: 0, state: "unknown", known: false, edited: 0 };
  t["8"] = { value: 151, state: "read", known: true, edited: 0 };
  const r = loadProfile(
    t,
    { "1": 5, "3": 10, "29": 7, "4": 8, "5": 0, "8": 99, "99": 1 },
    (cv) => cv !== "8",
    () => 0xff,
  );
  assert.equal(r.changed, 3, "CVs 1, 29 and 5, which had no value");
  assert.equal(r.replaced, 1, "the change to CV 1");
  assert.deepEqual(r.unread, ["3", "4"], "CVs 3 and 4 match, though neither was read in this session");
  assert.deepEqual(
    ["1", "3", "29", "4", "5", "8"].map((cv) => [r.table[cv].state, r.table[cv].value]),
    [
      ["edited", 5],
      ["saved", 10],
      ["edited", 7],
      ["imported", 8],
      ["edited", 0],
      ["read", 151],
    ],
  );
  assert.equal(r.table["99"], undefined, "a CV the definition lacks is left out");
  const back = loadProfile(
    t,
    { "1": 3 },
    () => true,
    () => 0xff,
  );
  assert.deepEqual([back.changed, back.replaced, back.unread], [0, 1, ["1"]]);
  assert.deepEqual(back.table["1"], { value: 3, state: "saved", known: true, edited: 0 }, "a profile that undoes a change drops it");
  assert.deepEqual(pendingChanges(r.table)["29"], { value: 7, bits: 0xff });
  assert.deepEqual(discard(r.table, ["1", "29"])["1"], { value: 3, state: "saved", known: true, edited: 0 }, "undoing goes back to before the edits");
});

test("a kept profile loads in full where the session has not seen the decoder hold it", () => {
  let t: CVTable = withImported(withSaved({}, { "1": 3, "3": 10, "29": 6 }), { "4": 8 });
  t = withSession(t, { read: ["29"], written: [] });
  t = applyUpdates(t, [{ cv: "1", value: 4, bits: 0x7f }], "edited");
  const profile = { "1": 3, "3": 10, "29": 6, "4": 9 };
  const r = loadProfile(
    t,
    profile,
    () => true,
    () => 0xff,
    true,
  );
  assert.deepEqual([r.changed, r.replaced, r.unread], [3, 1, ["3"]], "CVs 1, 3 and 4; not 29, read in this session");
  assert.deepEqual(
    ["1", "3", "29", "4"].map((cv) => [r.table[cv].state, r.table[cv].value, r.table[cv].edited]),
    [
      ["edited", 3, 0xff],
      ["edited", 10, 0xff],
      ["read", 6, 0],
      ["edited", 9, 0xff],
    ],
  );
  assert.deepEqual(pendingChanges(r.table)["3"], { value: 10, bits: 0xff }, "a value marked though it matches is kept as a change");
  const again = loadProfile(
    r.table,
    profile,
    () => true,
    () => 0xff,
    true,
  );
  assert.deepEqual([again.changed, again.replaced, again.unread], [0, 0, []], "loading it again finds nothing more to mark");
  // Loaded first for the differences, then in full.
  const first = loadProfile(
    t,
    profile,
    () => true,
    () => 0xff,
  );
  assert.deepEqual([first.changed, first.unread], [1, ["1", "3"]], "CV 4 differs; CVs 1 and 3 are taken as they are");
  assert.equal(
    loadProfile(
      first.table,
      profile,
      () => true,
      () => 0xff,
      true,
    ).changed,
    2,
  );
});

test("only values read or written in this session are confirmed", () => {
  let t: CVTable = withImported(withSaved({}, { "1": 3, "29": 6, "3": 10 }), { "4": 8 });
  t = withSession(t, { read: ["1"], written: ["3"] });
  t = applyUpdates(t, [{ cv: "29", value: 0x26, bits: 0x20 }], "edited");
  t = applyUpdates(t, [{ cv: "1", value: 4, bits: 0x7f }], "edited");
  t["5"] = { value: 0, state: "fromfile", known: false, edited: 0 };
  assert.deepEqual(
    ["1", "3", "29", "4", "5", "99"].map((cv) => confirmed(t[cv])),
    [true, true, false, false, false, false],
    "a change over a value read now, a value written now; not a change over a saved value, an imported value, a default or nothing",
  );
  assert.equal(confirmed(endSession(t)["3"]), false, "a session's values are saved once it ends");
});

test("a setting is confirmed when each CV that can be read was read or written in this session", () => {
  let t: CVTable = withSaved({}, { "1": 3, "3": 10, "7": 2, "8": 151 });
  t = withSession(t, { read: ["1"], written: ["3"] });
  const readable = (cv: string) => cv !== "7";
  assert.equal(allConfirmed(["1", "3"], t, readable), true);
  assert.equal(allConfirmed(["1", "8"], t, readable), false, "CV 8 was saved before this session");
  assert.equal(allConfirmed(["1", "7"], t, readable), true, "CV 7 cannot be read, so no read can confirm it");
});

test("each page to read shows a CV the pages before it do not", () => {
  const pages = [{ cvs: ["1", "29"] }, { cvs: ["29", "17", "18"] }, { cvs: ["1", "2", "3", "4", "5", "17", "18", "29"], table: true }, { cvs: ["29", "3"] }, { cvs: [] }];
  assert.deepEqual(pagesToRead(["29", "17", "3"], pages), [0, 1, 3], "Basic for 29, the next for 17 and the last for 3");
  assert.deepEqual(pagesToRead(["18"], pages), [1]);
  assert.deepEqual(pagesToRead(["2", "29"], pages), [0, 2], "the CV table only for CV 2, which no other page shows");
  assert.deepEqual(pagesToRead(["9"], pages), [], "a CV no page shows");
  assert.deepEqual(pagesToRead([], pages), []);
});
