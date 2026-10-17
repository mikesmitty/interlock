// An ESU decoder's function mapping: rows that each switch outputs,
// logical functions and sounds on while all of the row's conditions hold.
// Each row's items are bits in CVs behind the index CVs 31 and 32; these
// functions find them and read and change them in a CV table. Only type
// imports here, so Node can run the tests without a build.
import type { ESUItem, ESUMap } from "./types";
import type { CVTable, CVUpdate } from "./values";

/** Where one item of a row lives: its CV, the CV's bits it uses and how far up they sit. */
export interface ESUCell {
  cv: string;
  mask: number;
  shift: number;
}

/** CV 31's value for the mapping pages. */
const INDEX_VALUE = 16;

export function esuCell(m: ESUMap, row: number, block: number, item: number): ESUCell {
  const b = m.blocks[block];
  const bit = item * b.bits;
  const page = b.page + Math.floor(row / 16);
  const cv = b.start + ((16 * row + Math.floor(bit / 8)) % 256);
  const shift = bit % 8;
  return { cv: `${INDEX_VALUE}.${page}.${cv}`, mask: ((2 ** b.bits - 1) << shift) & 0xff, shift };
}

/** A row's CVs that hold an item shown, each once, in order. */
export function esuRowCVs(m: ESUMap, row: number): string[] {
  const out: string[] = [];
  m.blocks.forEach((b, bi) =>
    b.items.forEach((it, i) => {
      if (it.hidden) return;
      const { cv } = esuCell(m, row, bi, i);
      if (!out.includes(cv)) out.push(cv);
    }),
  );
  return out;
}

/** Every CV of the map, row by row. */
export function esuCVs(m: ESUMap): string[] {
  const out: string[] = [];
  for (let r = 0; r < m.rows; r++) out.push(...esuRowCVs(m, r));
  return out;
}

/** An item's value: 0 or 1, and for a condition 0 (either), 1 (on) or 2 (off). */
export function esuValue(m: ESUMap, t: CVTable, row: number, block: number, item: number): number {
  const c = esuCell(m, row, block, item);
  return ((t[c.cv]?.value ?? 0) & c.mask) >>> c.shift;
}

/** The change that sets one item. */
export function esuUpdate(m: ESUMap, t: CVTable, row: number, block: number, item: number, value: number): CVUpdate {
  const c = esuCell(m, row, block, item);
  const old = t[c.cv]?.value ?? 0;
  return { cv: c.cv, value: ((old & ~c.mask) | ((value << c.shift) & c.mask)) >>> 0, bits: c.mask };
}

/** Whether every CV of a row has been read, written or saved. */
export function esuRowKnown(m: ESUMap, t: CVTable, row: number): boolean {
  return esuRowCVs(m, row).every((cv) => t[cv]?.known);
}

/** Whether a row has changes not written yet. */
export function esuRowEdited(m: ESUMap, t: CVTable, row: number): boolean {
  return esuRowCVs(m, row).some((cv) => t[cv]?.state === "edited");
}

/** How a condition reads in a row's summary. */
export function conditionText(item: ESUItem, value: number): string {
  if (value === 1) return item.on || item.name;
  if (value === 2) return item.off || `not ${item.name}`;
  return `${item.name} (value ${value})`;
}

/** What a row does, in words: its conditions and, block by block, what it switches on. */
export interface ESURowSummary {
  conditions: string[];
  switches: { block: string; names: string[] }[];
}

export function esuSummary(m: ESUMap, t: CVTable, row: number): ESURowSummary {
  const out: ESURowSummary = { conditions: [], switches: [] };
  m.blocks.forEach((b, bi) => {
    const names: string[] = [];
    b.items.forEach((it, i) => {
      if (it.hidden) return;
      const v = esuValue(m, t, row, bi, i);
      if (v === 0) return;
      if (b.bits === 2) out.conditions.push(conditionText(it, v));
      else names.push(it.name);
    });
    if (b.bits !== 2 && names.length > 0) out.switches.push({ block: b.name, names });
  });
  return out;
}
