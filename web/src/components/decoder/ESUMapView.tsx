import { useState } from "react";
import { esuRowCVs, esuRowEdited, esuRowKnown, esuSummary, esuUpdate, esuValue } from "../../decoder/esumap";
import type { ESUItem, ESUMap } from "../../decoder/types";
import { applyUpdates, sourceOf, type CVUpdate } from "../../decoder/values";
import type { PaneContext } from "./PaneView";
import { SourceLamp } from "./SourceLamp";

/**
 * An ESU decoder's mapping table: one line per row saying what it does,
 * and an editor for the row that is open.
 */
export function ESUMapView({ map, ctx }: { map: ESUMap; ctx: PaneContext }) {
  const [open, setOpen] = useState<number>();
  const rows = Array.from({ length: map.rows }, (_, r) => r);
  return (
    <div className="dec-esumap">
      <p className="dec-label">Each row switches its outputs, logical functions and sounds on while all of its conditions hold. Choose a row to change it.</p>
      <table className="table dec-esumap__table">
        <thead>
          <tr>
            <th scope="col">Row</th>
            <th scope="col">When</th>
            <th scope="col">Switches on</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <MapRow key={r} map={map} row={r} ctx={ctx} open={open === r} onToggle={() => setOpen(open === r ? undefined : r)} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function MapRow({ map, row, ctx, open, onToggle }: { map: ESUMap; row: number; ctx: PaneContext; open: boolean; onToggle: () => void }) {
  const t = ctx.table;
  const known = esuRowKnown(map, t, row);
  const edited = esuRowEdited(map, t, row);
  // A row with no value from anywhere: not read, imported or a default.
  const neverRead = !edited && esuRowCVs(map, row).every((cv) => (t[cv]?.state ?? "unknown") === "unknown");
  const someKnown = esuRowCVs(map, row).some((cv) => t[cv]?.known);
  const s = esuSummary(map, t, row);
  const unused = s.conditions.length === 0 && s.switches.length === 0;
  const cls = ["dec-esumap__row", edited ? "is-dirty" : "", !known ? "is-unread" : "", unused && !neverRead ? "is-unused" : ""].filter(Boolean).join(" ");
  let when: string;
  let switches: string;
  if (neverRead) {
    when = "Not read";
    switches = "";
  } else if (unused) {
    when = "Not used";
    switches = "";
  } else {
    when = s.conditions.length > 0 ? s.conditions.join(", ") : "Always";
    switches = s.switches.length > 0 ? s.switches.map((b) => b.names.join(", ")).join("; ") : "Nothing";
  }
  return (
    <>
      <tr className={cls}>
        <td className="num">
          <span className="dec-esumap__num">
            <SourceLamp state={sourceOf(esuRowCVs(map, row), t)} />
            <button type="button" className="linklike" aria-expanded={open} onClick={onToggle}>
              {row + 1}
            </button>
          </span>
        </td>
        <td>{when}</td>
        <td>
          {switches}
          {edited && <span className="field__dirty"> changed</span>}
          {!known && !neverRead && <span className="faint">{someKnown ? " not all read" : " not read"}</span>}
        </td>
      </tr>
      {open && (
        <tr className="dec-esumap__open">
          <td colSpan={3}>
            <RowEditor map={map} row={row} ctx={ctx} onClose={onToggle} />
          </td>
        </tr>
      )}
    </>
  );
}

/** A key's label from the roster, as "F1 Bell". */
function itemName(it: ESUItem, labels?: Record<string, string>): string {
  const m = /^F(\d+)$/.exec(it.name);
  const label = m ? labels?.[m[1]] : undefined;
  return label ? `${it.name} ${label}` : it.name;
}

function RowEditor({ map, row, ctx, onClose }: { map: ESUMap; row: number; ctx: PaneContext; onClose: () => void }) {
  const t = ctx.table;
  const set = (block: number, item: number, value: number) => ctx.setBits([esuUpdate(map, ctx.table, row, block, item, value)]);
  const clear = () => {
    // Items share CVs, so each change builds on the last.
    let table = ctx.table;
    const updates: CVUpdate[] = [];
    for (let bi = 0; bi < map.blocks.length; bi++) {
      for (let i = 0; i < map.blocks[bi].items.length; i++) {
        if (map.blocks[bi].items[i].hidden || esuValue(map, table, row, bi, i) === 0) continue;
        const u = esuUpdate(map, table, row, bi, i, 0);
        updates.push(u);
        table = applyUpdates(table, [u], "edited");
      }
    }
    if (updates.length > 0) ctx.setBits(updates);
  };
  return (
    <div className="dec-esumap__editor">
      {map.blocks.map((b, bi) => {
        const items = b.items.map((it, i) => ({ it, i })).filter(({ it }) => !it.hidden);
        if (items.length === 0) return null;
        return (
          <fieldset key={bi} className="dec-esumap__block">
            <legend>{bi === 0 ? `Row ${row + 1} works when` : b.name}</legend>
            <div className="dec-esumap__items">
              {items.map(({ it, i }) => {
                const v = esuValue(map, t, row, bi, i);
                const name = itemName(it, ctx.functionLabels);
                if (b.bits === 2) {
                  return (
                    <label key={i} className="dec-esumap__cond">
                      <span>{name}</span>
                      <select className="input input--select" value={v} disabled={ctx.busy} onChange={(e) => set(bi, i, Number(e.target.value))}>
                        <option value={0}>Either</option>
                        <option value={1}>{it.on || "On"}</option>
                        <option value={2}>{it.off || "Off"}</option>
                        {v === 3 && <option value={3}>Value 3</option>}
                      </select>
                    </label>
                  );
                }
                return (
                  <label key={i} className="check">
                    <input type="checkbox" checked={v === 1} disabled={ctx.busy} onChange={(e) => set(bi, i, e.target.checked ? 1 : 0)} />
                    {name}
                  </label>
                );
              })}
            </div>
          </fieldset>
        );
      })}
      <div className="dec-esumap__actions">
        {ctx.readCVs && (
          <button type="button" className="btn" disabled={ctx.busy} onClick={() => ctx.readCVs!(esuRowCVs(map, row))}>
            Read row
          </button>
        )}
        <button type="button" className="btn btn--quiet" disabled={ctx.busy} onClick={clear}>
          Clear row
        </button>
        <button type="button" className="btn btn--quiet" onClick={onClose}>
          Close
        </button>
      </div>
    </div>
  );
}
