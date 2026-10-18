// Settings are views of CVs: each variable reads its share of one or more
// CVs through masks, and editing it changes only those bits. These
// functions turn CV values into setting values and back, following the
// rules of JMRI's decoder definitions. Only type imports here, so Node can
// run the tests without a build.
import type { Choice, DecoderDefinition, DecoderVariable, Part, Qualifier } from "./types";

/**
 * Where a CV's value came from: "read" from the decoder and "stored" to
 * it in this session; "saved" the roster's copy of what was read or
 * written before it; "imported" another roster's copy, not read since;
 * "fromfile" the definition's default; "edited" changed here and not
 * written yet.
 */
export type CVState = "unknown" | "fromfile" | "imported" | "edited" | "read" | "stored" | "saved";

export interface CVCell {
  value: number;
  state: CVState;
  /** Set once the value came from the decoder (or the roster's copy of it); an imported value is not known. */
  known: boolean;
  /** Bits changed since the value was last known. */
  edited: number;
  /** An edited CV's value and state before the edits, to go back to. */
  prior?: { value: number; state: CVState };
}

/** A change not written yet, as the roster keeps it: the bits it sets and their values. */
export interface CVChange {
  value: number;
  bits: number;
}

export type CVTable = Record<string, CVCell>;

/** One change to a CV: its new value and the bits the change owns. */
export interface CVUpdate {
  cv: string;
  value: number;
  bits: number;
}

export type Value = number | string | number[];

export type EncodeResult = { updates: CVUpdate[] } | { error: string };

const pow2 = (n: number) => 2 ** n;

function modulusOf(v: DecoderVariable): number {
  return v.modulus && v.modulus > 0 ? v.modulus : 256;
}

/** The field a part reads from its CV. */
export function field(p: Part, cv: number, modulus = 256): number {
  if (p.radix) return Math.floor(cv / p.radix) % modulus;
  return ((cv & (p.mask ?? 0)) >>> 0) >>> (p.shift ?? 0);
}

/** The CV value with a part's field replaced. */
export function withField(p: Part, cv: number, value: number, modulus = 256): number {
  if (p.radix) {
    const r = p.radix;
    const n = modulus;
    return Math.floor(cv / (r * n)) * (r * n) + (((value % n) + n) % n) * r + (cv % r);
  }
  const mask = p.mask ?? 0;
  return ((cv & ~mask) | ((value << (p.shift ?? 0)) & mask)) >>> 0;
}

/**
 * The bits of its CV a part owns. A radix field is a digit, not bits, so
 * an edit to one claims the whole CV.
 */
export function partBits(p: Part): number {
  return p.radix ? 0xff : (p.mask ?? 0);
}

function cvValue(t: CVTable, name: string): number {
  return t[name]?.value ?? 0;
}

/** The raw number a variable's parts hold, least significant part first. */
export function rawValue(v: DecoderVariable, t: CVTable): number {
  const parts = v.parts ?? [];
  if (parts.length === 1) return field(parts[0], cvValue(t, parts[0].cv), modulusOf(v));
  let raw = 0;
  let offset = 0;
  for (const p of parts) {
    raw += field(p, cvValue(t, p.cv)) * pow2(offset);
    offset += p.width ?? 8;
  }
  return raw;
}

/** The updates that store a raw number in a variable's parts. */
export function rawUpdates(v: DecoderVariable, t: CVTable, raw: number): CVUpdate[] {
  const parts = v.parts ?? [];
  if (parts.length === 1) {
    const p = parts[0];
    const m = modulusOf(v);
    return [{ cv: p.cv, value: withField(p, cvValue(t, p.cv), raw, m), bits: partBits(p) }];
  }
  const out: CVUpdate[] = [];
  let offset = 0;
  for (const p of parts) {
    const width = p.width ?? 8;
    const f = Math.floor(raw / pow2(offset)) % pow2(width);
    out.push({ cv: p.cv, value: withField(p, cvValue(t, p.cv), f), bits: partBits(p) });
    offset += width;
  }
  return out;
}

const scale = (v: DecoderVariable, raw: number) => raw * (v.factor || 1) + (v.offset ?? 0);
const unscale = (v: DecoderVariable, shown: number) => Math.trunc((shown - (v.offset ?? 0)) / (v.factor || 1));

/** An enum's choices, with a "Reserved value" entry when the decoder holds a value the definition does not list. */
export function choicesFor(v: DecoderVariable, raw: number): Choice[] {
  const choices = v.choices ?? [];
  if (choices.some((c) => c.value === raw)) return choices;
  return [...choices, { label: `Reserved value ${raw}`, value: raw }];
}

const UNIT_MS: Record<string, number> = {
  Nanos: 1e-6,
  Micros: 1e-3,
  Millis: 1,
  Seconds: 1000,
  Minutes: 60_000,
  Hours: 3_600_000,
  HalfDays: 43_200_000,
  Days: 86_400_000,
  Weeks: 604_800_000,
};

function textBytes(v: DecoderVariable, t: CVTable): number[] {
  return (v.parts ?? []).map((p) => field(p, cvValue(t, p.cv)));
}

/** A variable's value as the user sees it. */
export function decode(v: DecoderVariable, t: CVTable): Value {
  switch (v.type) {
    case "constant":
      return Number(v.default ?? 0) || 0;
    case "decimal":
    case "short_address":
    case "split":
      return scale(v, rawValue(v, t));
    case "hex":
    case "split_hex":
    case "enum":
    case "split_enum":
      return rawValue(v, t);
    case "split_hundreds": {
      const parts = (v.parts ?? []).map((p) => field(p, cvValue(t, p.cv)));
      return Number(
        parts
          .reverse()
          .map((b) => String(b).padStart(2, "0"))
          .join(""),
      );
    }
    case "split_text": {
      let bytes = textBytes(v, t);
      const term = v.text?.term_byte;
      if (term !== undefined) {
        const end = bytes.indexOf(term);
        if (end >= 0) bytes = bytes.slice(0, end);
      }
      return new TextDecoder().decode(new Uint8Array(bytes));
    }
    case "split_date_time": {
      const dt = v.date_time!;
      const base = Date.parse(dt.base.endsWith("Z") ? dt.base : dt.base + "Z");
      const when = new Date(base + rawValue(v, t) * (dt.factor || 1) * (UNIT_MS[dt.unit] ?? 1000));
      const iso = when.toISOString();
      if (dt.display === "dateOnly") return iso.slice(0, 10);
      if (dt.display === "timeOnly") return iso.slice(11, 19);
      return iso.slice(0, 19).replace("T", " ");
    }
    case "speed_table":
      return (v.parts ?? []).map((p) => cvValue(t, p.cv));
    case "long_address": {
      const [hi, lo] = v.parts ?? [];
      return (cvValue(t, hi.cv) & 0x3f) * 256 + cvValue(t, lo.cv);
    }
    case "composite":
      return -1;
  }
}

/** The number a qualifier compares: an enum's choice value, a number as shown, an address. */
export function numericValue(v: DecoderVariable, t: CVTable, vars: Map<string, DecoderVariable>): number {
  switch (v.type) {
    case "speed_table":
    case "split_text":
      return 0;
    case "split_date_time":
      return rawValue(v, t);
    case "composite":
      return compositeIndex(v, t, vars);
  }
  return decode(v, t) as number;
}

/** Which preset a composite setting matches now, or -1 for none. */
export function compositeIndex(v: DecoderVariable, t: CVTable, vars: Map<string, DecoderVariable>): number {
  return (v.composite ?? []).findIndex((c) =>
    c.settings.every((s) => {
      const target = vars.get(s.item);
      return target !== undefined && numericValue(target, t, vars) === s.value;
    }),
  );
}

function checkRange(v: DecoderVariable, shown: number): string | undefined {
  if (!Number.isInteger(shown)) return "Enter a whole number.";
  if (shown < v.min || (v.max !== undefined && shown > v.max)) {
    return v.max !== undefined ? `Enter a number from ${v.min} to ${v.max}.` : `Enter a number of at least ${v.min}.`;
  }
  return undefined;
}

/**
 * The CV updates that set a variable to a value. Numbers are in the
 * variable's displayed terms; enums take a choice value; text takes a
 * string; a speed table takes every point.
 */
export function encode(v: DecoderVariable, value: Value, t: CVTable, vars: Map<string, DecoderVariable>): EncodeResult {
  switch (v.type) {
    case "constant":
    case "split_date_time":
      return { error: "This value cannot be changed." };
    case "decimal":
    case "short_address":
    case "split": {
      const shown = Number(value);
      const err = checkRange(v, shown);
      if (err) return { error: err };
      return { updates: rawUpdates(v, t, unscale(v, shown)) };
    }
    case "hex":
    case "split_hex": {
      const n = Number(value);
      const err = checkRange(v, n);
      if (err) return { error: err.replace("a whole number", "a hex number") };
      return { updates: rawUpdates(v, t, n) };
    }
    case "enum":
    case "split_enum":
      return { updates: rawUpdates(v, t, Number(value)) };
    case "split_hundreds": {
      const shown = Number(value);
      const err = checkRange(v, shown);
      if (err) return { error: err };
      const updates: CVUpdate[] = [];
      let rest = shown;
      for (const p of v.parts ?? []) {
        updates.push({ cv: p.cv, value: withField(p, cvValue(t, p.cv), rest % 100), bits: partBits(p) });
        rest = Math.floor(rest / 100);
      }
      return { updates };
    }
    case "split_text": {
      const text = String(value);
      if (v.text?.match && !new RegExp(`^(?:${v.text.match})$`).test(text)) return { error: "That text is not allowed here." };
      const bytes = Array.from(new TextEncoder().encode(text));
      const parts = v.parts ?? [];
      const updates = parts.map((p, i) => {
        let b = 0;
        if (i < bytes.length) b = bytes[i];
        else if (i === bytes.length && v.text?.term_byte !== undefined) b = v.text.term_byte;
        else if (v.text?.pad_byte !== undefined) b = v.text.pad_byte;
        return { cv: p.cv, value: withField(p, cvValue(t, p.cv), b), bits: partBits(p) };
      });
      return { updates };
    }
    case "speed_table": {
      const points = value as number[];
      return { updates: (v.parts ?? []).map((p, i) => ({ cv: p.cv, value: points[i] ?? 0, bits: 0xff })) };
    }
    case "long_address": {
      const a = Number(value);
      if (!Number.isInteger(a) || a < 1 || a > 10239) return { error: "Enter an address from 1 to 10239." };
      const [hi, lo] = v.parts ?? [];
      return {
        updates: [
          { cv: hi.cv, value: 0xc0 | ((a >> 8) & 0x3f), bits: 0xff },
          { cv: lo.cv, value: a & 0xff, bits: 0xff },
        ],
      };
    }
    case "composite": {
      const choice = v.composite?.[Number(value)];
      if (!choice) return { error: "Choose a preset." };
      let table = t;
      const updates: CVUpdate[] = [];
      for (const s of choice.settings) {
        const target = vars.get(s.item);
        if (!target) continue;
        const r = encode(target, s.value, table, vars);
        if ("error" in r) return r;
        updates.push(...r.updates);
        table = applyUpdates(table, r.updates, "edited");
      }
      return { updates };
    }
  }
}

const blank: CVCell = { value: 0, state: "unknown", known: false, edited: 0 };

/** A copy of the table with updates applied; changed CVs take the state given. */
export function applyUpdates(t: CVTable, updates: CVUpdate[], state: CVState): CVTable {
  const out = { ...t };
  for (const u of updates) {
    const old = out[u.cv] ?? blank;
    if (old.value === u.value && state === "edited") continue;
    const cell: CVCell = {
      value: u.value,
      state,
      known: old.known,
      edited: state === "edited" ? (old.edited | u.bits) >>> 0 : old.edited,
    };
    if (state === "edited") cell.prior = old.state === "edited" ? old.prior : { value: old.value, state: old.state };
    out[u.cv] = cell;
  }
  return out;
}

/**
 * A copy of the table with a value read from or written to the decoder.
 * A read keeps changes not written yet, over the decoder's other bits,
 * unless the decoder already holds them.
 */
export function settle(t: CVTable, cv: string, value: number, state: "read" | "stored"): CVTable {
  const old = t[cv];
  if (state === "read" && old?.state === "edited" && old.edited !== 0) {
    const merged = merge(old, value);
    if (merged !== value) return { ...t, [cv]: { value: merged, state: "edited", known: true, edited: old.edited, prior: { value, state: "read" } } };
  }
  return { ...t, [cv]: { value, state, known: true, edited: 0 } };
}

/**
 * A copy of the table with a kept profile's values as changes to write:
 * each writable CV whose value differs from the profile's, or has none,
 * takes the profile's whole value, and a change not written yet that the
 * profile undoes is dropped. changed counts the CVs it marks, replaced the
 * changes not written yet it gives another value or drops.
 */
export function loadProfile(
  t: CVTable,
  values: Record<string, number>,
  writable: (cv: string) => boolean,
  bits: (cv: string) => number,
): { table: CVTable; changed: number; replaced: number } {
  const out = { ...t };
  let changed = 0;
  let replaced = 0;
  for (const [cv, value] of Object.entries(values)) {
    const old = out[cv];
    if (!old || !writable(cv)) continue;
    if (old.state === "edited") {
      const prior = old.prior;
      if (prior && prior.value === value && prior.state !== "unknown") {
        if (old.value !== value) replaced++;
        out[cv] = { value, state: prior.state, known: old.known, edited: 0 };
        continue;
      }
      if (old.value === value) continue;
      replaced++;
    } else if (old.value === value && old.state !== "unknown") continue;
    out[cv] = {
      value,
      state: "edited",
      known: old.known,
      edited: bits(cv),
      prior: old.state === "edited" ? old.prior : { value: old.value, state: old.state },
    };
    changed++;
  }
  return { table: out, changed, replaced };
}

/**
 * Whether a CV's value, or for a changed CV the value under the change,
 * was read from the decoder or written to it in this session: the only
 * values trusted to be what the decoder holds now.
 */
export function confirmed(cell: CVCell | undefined): boolean {
  const state = cell?.state === "edited" ? cell.prior?.state : cell?.state;
  return state === "read" || state === "stored";
}

/** A copy of the table with the changes to these CVs undone. */
export function discard(t: CVTable, cvs: string[]): CVTable {
  const out = { ...t };
  for (const cv of cvs) {
    const old = out[cv];
    if (old?.state !== "edited") continue;
    const prior = old.prior ?? { value: old.value, state: old.known ? "read" : "unknown" };
    out[cv] = { value: prior.value, state: prior.state, known: old.known, edited: 0 };
  }
  return out;
}

/** The changes not written yet, for the roster to keep, by CV name in order. */
export function pendingChanges(t: CVTable): Record<string, CVChange> {
  const out: Record<string, CVChange> = {};
  for (const cv of Object.keys(t).sort()) {
    const cell = t[cv];
    if (cell.state === "edited" && cell.edited !== 0) out[cv] = { value: (cell.value & cell.edited) >>> 0, bits: cell.edited };
  }
  return out;
}

/** A copy of the table with kept changes laid over what it holds. */
export function withChanges(t: CVTable, changes: Record<string, CVChange>): CVTable {
  const out = { ...t };
  for (const [cv, c] of Object.entries(changes)) {
    const old = out[cv] ?? blank;
    const value = ((old.value & ~c.bits) | (c.value & c.bits)) >>> 0;
    out[cv] = { value, state: "edited", known: old.known, edited: c.bits, prior: { value: old.value, state: old.state } };
  }
  return out;
}

/**
 * The value to write to a CV that was edited before it was ever read:
 * the decoder's other bits, as just read, with the edited bits on top.
 */
export function merge(cell: CVCell, fromDecoder: number): number {
  return ((fromDecoder & ~cell.edited) | (cell.value & cell.edited)) >>> 0;
}

/** A copy of the table with a roster decoder's saved values, which count as known. */
export function withSaved(t: CVTable, cvs: Record<string, number>): CVTable {
  const out = { ...t };
  for (const [cv, value] of Object.entries(cvs)) out[cv] = { value, state: "saved", known: true, edited: 0 };
  return out;
}

/**
 * A copy of the table with values imported from another roster. They are
 * not known: they show as not read, and an edit to part of one reads the
 * decoder before writing.
 */
export function withImported(t: CVTable, cvs: Record<string, number>): CVTable {
  const out = { ...t };
  for (const [cv, value] of Object.entries(cvs)) out[cv] = { value, state: "imported", known: false, edited: 0 };
  return out;
}

/** What a roster decoder's session has read from it and written to it. */
export interface SessionCVs {
  read: string[];
  written: string[];
}

/** A copy of the table with the session's saved values marked as read or written in it. */
export function withSession(t: CVTable, s: SessionCVs): CVTable {
  const out = { ...t };
  const mark = (cv: string, state: CVState) => {
    const old = out[cv];
    if (old?.state === "saved") out[cv] = { ...old, state };
    else if (old?.state === "edited" && old.prior?.state === "saved") out[cv] = { ...old, prior: { ...old.prior, state } };
  };
  for (const cv of s.read) mark(cv, "read");
  for (const cv of s.written) mark(cv, "stored");
  return out;
}

/** A copy of the table with the session over: what it read and wrote counts as saved. */
export function endSession(t: CVTable): CVTable {
  const out = { ...t };
  const over = (s: CVState) => s === "read" || s === "stored";
  for (const [cv, cell] of Object.entries(out)) {
    if (over(cell.state)) out[cv] = { ...cell, state: "saved" };
    else if (cell.state === "edited" && cell.prior && over(cell.prior.state)) out[cv] = { ...cell, prior: { ...cell.prior, state: "saved" } };
  }
  return out;
}

/** Whether a CV's value, or its value before edits, was imported and not read since. */
export function isImported(cell: CVCell | undefined): boolean {
  return cell?.state === "imported" || (cell?.state === "edited" && cell.prior?.state === "imported");
}

/** The values the decoder is known to hold, for saving to the roster: an edited CV's value from before the edits. */
export function knownValues(t: CVTable): Record<string, number> {
  const out: Record<string, number> = {};
  for (const [cv, cell] of Object.entries(t)) {
    if (!cell.known) continue;
    if (cell.state !== "edited") out[cv] = cell.value;
    else if (cell.prior && ["read", "stored", "saved"].includes(cell.prior.state)) out[cv] = cell.prior.value;
  }
  return out;
}

// From least settled to most: a setting over several CVs shows the least
// settled state among them. Values saved on an earlier visit rank below
// those read or written on this one.
const SOURCE_ORDER: CVState[] = ["edited", "unknown", "fromfile", "imported", "saved", "stored", "read"];

/** The state a setting shows for its CVs, or undefined when it has none. */
export function sourceOf(cvs: string[], t: CVTable): CVState | undefined {
  let least: number | undefined;
  for (const cv of cvs) {
    const i = SOURCE_ORDER.indexOf(t[cv]?.state ?? "unknown");
    if (least === undefined || i < least) least = i;
  }
  return least === undefined ? undefined : SOURCE_ORDER[least];
}

/** The CV table a definition starts with: its defaults, from the file. */
export function initialTable(def: DecoderDefinition): CVTable {
  let t: CVTable = {};
  for (const c of def.cvs) t[c.name] = { value: 0, state: "unknown", known: false, edited: 0 };
  const vars = variableMap(def);
  const touched = new Set<string>();
  for (const v of def.variables) {
    const updates = defaultUpdates(v, t, vars);
    for (const u of updates) {
      const cell = t[u.cv] ?? { value: 0, state: "unknown", known: false, edited: 0 };
      t = { ...t, [u.cv]: { ...cell, value: u.value } };
      touched.add(u.cv);
    }
  }
  for (const cv of touched) t[cv] = { ...t[cv], state: "fromfile" };
  return t;
}

function defaultUpdates(v: DecoderVariable, t: CVTable, vars: Map<string, DecoderVariable>): CVUpdate[] {
  if (v.type === "speed_table") {
    const st = v.speed_table!;
    const n = st.entries;
    const points = Array.from({ length: n }, (_, i) => (n > 1 ? st.min + Math.floor(((st.max - st.min) * i) / (n - 1)) : st.min));
    return (v.parts ?? []).map((p, i) => ({ cv: p.cv, value: points[i], bits: 0xff }));
  }
  if (v.default === undefined || v.default === "" || v.type === "constant") return [];
  const d = v.default.trim();
  let r: EncodeResult;
  switch (v.type) {
    case "decimal":
    case "short_address": {
      const n = Number.parseInt(d, 10);
      if (Number.isNaN(n)) return [];
      const clamped = Math.min(Math.max(n, v.min), v.max ?? n);
      return rawUpdates(v, t, unscale(v, clamped));
    }
    case "hex":
    case "enum":
    case "split":
    case "split_enum": {
      const n = Number.parseInt(d, 10);
      return Number.isNaN(n) ? [] : rawUpdates(v, t, n);
    }
    case "split_hex": {
      const n = Number.parseInt(d, 16);
      return Number.isNaN(n) ? [] : rawUpdates(v, t, n);
    }
    case "split_hundreds":
    case "long_address":
      r = encode(v, Number.parseInt(d, 10), t, vars);
      break;
    case "split_text":
      r = encode(v, d, t, vars);
      break;
    case "composite":
      r = encode(v, Number.parseInt(d, 10), t, vars);
      break;
    default:
      return [];
  }
  return "updates" in r ? r.updates : [];
}

export function variableMap(def: DecoderDefinition): Map<string, DecoderVariable> {
  const m = new Map<string, DecoderVariable>();
  for (const v of def.variables) if (!m.has(v.item)) m.set(v.item, v);
  return m;
}

/** Whether every qualifier holds. */
export function qualified(qs: Qualifier[] | undefined, t: CVTable, vars: Map<string, DecoderVariable>): boolean {
  for (const q of qs ?? []) {
    if (q.relation === "never") return false;
    const v = q.item ? vars.get(q.item) : undefined;
    if (!v) continue;
    const n = numericValue(v, t, vars);
    const want = q.value ?? 0;
    const ok =
      q.relation === "eq"
        ? n === want
        : q.relation === "ne"
          ? n !== want
          : q.relation === "gt"
            ? n > want
            : q.relation === "ge"
              ? n >= want
              : q.relation === "lt"
                ? n < want
                : n <= want;
    if (!ok) return false;
  }
  return true;
}

/**
 * A speed table with point i set, kept rising: points before it are
 * lowered and points after it raised as needed. With mfx the ends stay at
 * the table's limits.
 */
export function setSpeedPoint(v: DecoderVariable, points: number[], i: number, value: number): number[] {
  const st = v.speed_table!;
  const out = [...points];
  const last = out.length - 1;
  if (st.mfx && (i === 0 || i === last)) return out;
  out[i] = Math.min(Math.max(Math.round(value), st.min), st.max);
  for (let j = i - 1; j >= 0 && out[j] > out[j + 1]; j--) out[j] = out[j + 1];
  for (let j = i + 1; j <= last && out[j] < out[j - 1]; j++) out[j] = out[j - 1];
  if (st.mfx) {
    out[0] = st.min;
    out[last] = st.max;
  }
  return out;
}

/** The address settings a DCC address field edits. */
export interface AddressItems {
  short?: DecoderVariable;
  format?: DecoderVariable;
  long?: DecoderVariable;
}

export function addressItems(vars: Map<string, DecoderVariable>): AddressItems {
  return { short: vars.get("Short Address"), format: vars.get("Address Format"), long: vars.get("Long Address") };
}

/** The decoder's address as it is set now, long or short by CV 29. */
export function currentAddress(a: AddressItems, t: CVTable, vars: Map<string, DecoderVariable>): number | undefined {
  return addressOf(a, t, vars)?.address;
}

/** The address the decoder answers to and whether it is a long one. */
export function addressOf(a: AddressItems, t: CVTable, vars: Map<string, DecoderVariable>): { address: number; long: boolean } | undefined {
  const useLong = !!a.long && (!a.format || numericValue(a.format, t, vars) === 1);
  const v = useLong ? a.long : a.short;
  return v ? { address: decode(v, t) as number, long: useLong } : undefined;
}

/**
 * The updates that give the decoder an address: 1 to 127 is a short
 * address, anything higher a long one, and CV 29 says which.
 */
export function setAddress(a: AddressItems, address: number, t: CVTable, vars: Map<string, DecoderVariable>): EncodeResult {
  if (!Number.isInteger(address) || address < 1 || address > 10239) return { error: "Enter an address from 1 to 10239." };
  const long = address > 127 || !a.short;
  const target = long ? a.long : a.short;
  if (!target) return { error: "This decoder has no long address." };
  const r = encode(target, address, t, vars);
  if ("error" in r) return r;
  const updates = [...r.updates];
  if (a.format) {
    const f = encode(a.format, long ? 1 : 0, applyUpdates(t, updates, "edited"), vars);
    if ("updates" in f) updates.push(...f.updates);
  }
  return { updates };
}

/** The CVs a list of settings uses, in order, each once. */
export function cvsOf(items: string[], vars: Map<string, DecoderVariable>): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  const add = (v: DecoderVariable | undefined, depth = 0) => {
    if (!v) return;
    if (v.type === "composite" && depth === 0) {
      for (const c of v.composite ?? []) for (const s of c.settings) add(vars.get(s.item), 1);
      return;
    }
    for (const p of v.parts ?? []) {
      if (!seen.has(p.cv)) {
        seen.add(p.cv);
        out.push(p.cv);
      }
    }
  };
  for (const item of items) add(vars.get(item));
  return out;
}

/** Whether a variable has CVs edited since they were last known. */
export function isEdited(v: DecoderVariable, t: CVTable): boolean {
  return (v.parts ?? []).some((p) => t[p.cv]?.state === "edited");
}

/** Whether every CV of a variable has been read or written. */
export function isKnown(v: DecoderVariable, t: CVTable): boolean {
  return (v.parts ?? []).every((p) => t[p.cv]?.known);
}
