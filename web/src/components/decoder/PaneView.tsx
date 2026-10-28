import { useEffect, useState, type CSSProperties, type ReactNode } from "react";
import type { DecoderCV, DecoderNode, DecoderVariable } from "../../decoder/types";
import {
  addressItems,
  choicesFor,
  compositeIndex,
  currentAddress,
  cvsOf,
  decode,
  isEdited,
  isKnown,
  qualified,
  setSpeedPoint,
  sourceOf,
  type CVTable,
  type CVUpdate,
  type Value,
} from "../../decoder/values";
import { ESUMapView } from "./ESUMapView";
import { SourceLamp } from "./SourceLamp";

/** What the pane's controls need: the CV values and how to change them. */
export interface PaneContext {
  table: CVTable;
  vars: Map<string, DecoderVariable>;
  cvs: DecoderCV[];
  /** Locks every control while the decoder is being read or written. */
  busy: boolean;
  /** Settings are written on the main, where a few are only available. */
  onMain: boolean;
  setValue: (v: DecoderVariable, value: Value) => void;
  setAddress: (address: number) => void;
  setCV: (cv: string, value: number) => void;
  /** Changes some bits of CVs, as the ESU function map does. */
  setBits: (updates: CVUpdate[]) => void;
  /** Reads some of the page's CVs; unset where nothing can be read. */
  readCVs?: (cvs: string[]) => void;
  /** The roster's function labels, by function number. */
  functionLabels?: Record<string, string>;
  /** Problems with the last edit, by setting. */
  errors: Record<string, string>;
}

type Direction = "column" | "row";

export function PaneNodes({ nodes, ctx }: { nodes: DecoderNode[]; ctx: PaneContext }) {
  return (
    <div className="dec-pane">
      {nodes.map((n, i) => (
        <NodeView key={i} node={n} ctx={ctx} dir="row" />
      ))}
    </div>
  );
}

function NodeView({ node, ctx, dir }: { node: DecoderNode; ctx: PaneContext; dir: Direction }): ReactNode {
  if (!qualified(node.qualifiers, ctx.table, ctx.vars)) return null;
  const kids = (d: Direction) => node.children?.map((c, i) => <NodeView key={i} node={c} ctx={ctx} dir={d} />);
  switch (node.kind) {
    case "column":
      return <div className="dec-col">{kids("column")}</div>;
    case "row":
    case "griditem":
      return (
        <div className="dec-row" style={gridStyle(node)}>
          {kids("row")}
        </div>
      );
    case "group":
      return <div className={dir === "row" ? "dec-row" : "dec-col"}>{kids(dir)}</div>;
    case "grid":
      return <div className="dec-grid">{kids("row")}</div>;
    case "label":
      return node.text?.trim() ? <p className="dec-label">{node.text}</p> : <div className="dec-spacer" />;
    case "separator":
      return <hr className={dir === "row" ? "dec-sep dec-sep--v" : "dec-sep"} />;
    case "display":
      return <Display node={node} ctx={ctx} />;
    case "dccaddress":
      return <AddressField ctx={ctx} />;
    case "fnmap":
      return <FunctionMap node={node} ctx={ctx} />;
    case "esu_fnmap":
      return node.esu_map ? <ESUMapView map={node.esu_map} ctx={ctx} /> : null;
    case "cvtable":
      return <CVTableView ctx={ctx} />;
  }
  return null;
}

function gridStyle(node: DecoderNode): CSSProperties | undefined {
  const g = node.grid;
  if (!g) return undefined;
  return {
    gridColumn: g.w === 0 ? `${g.x + 1} / -1` : `${g.x + 1} / span ${g.w}`,
    gridRow: g.h === 0 ? `${g.y + 1} / -1` : `${g.y + 1} / span ${g.h}`,
  };
}

function available(v: DecoderVariable, ctx: PaneContext): boolean {
  return qualified(v.qualifiers, ctx.table, ctx.vars);
}

function locked(v: DecoderVariable, ctx: PaneContext, node?: DecoderNode): boolean {
  return ctx.busy || !!v.read_only || !!v.info_only || !!node?.view_only || (!!v.ops_only && !ctx.onMain) || v.type === "split_date_time";
}

function Display({ node, ctx }: { node: DecoderNode; ctx: PaneContext }) {
  const v = ctx.vars.get(node.item ?? "");
  if (!v || !available(v, ctx)) return null;
  const label = node.label ?? v.label;
  const edited = isEdited(v, ctx.table);
  const unread = !isKnown(v, ctx.table) && v.type !== "constant";
  const tip = [v.tooltip || node.tooltip, v.comment].filter(Boolean).join("\n") || undefined;
  const cls = ["dec-field", node.layout ? `dec-field--${node.layout}` : "", edited ? "is-dirty" : "", unread ? "is-unread" : ""].filter(Boolean).join(" ");
  const error = ctx.errors[v.item];
  return (
    <div className={cls} title={tip}>
      {label && <span className="dec-field__label">{label}</span>}
      <span className="dec-field__control">
        <SourceLamp state={sourceOf(cvsOf([v.item], ctx.vars), ctx.table)} />
        <Control v={v} node={node} ctx={ctx} label={label || v.label} />
        {edited && <span className="field__dirty">changed</span>}
        {v.read_only && <span className="faint">read-only</span>}
      </span>
      {error && <span className="field__error">{error}</span>}
    </div>
  );
}

function Control({ v, node, ctx, label }: { v: DecoderVariable; node?: DecoderNode; ctx: PaneContext; label: string }) {
  const value = decode(v, ctx.table);
  const disabled = locked(v, ctx, node);
  const format = node?.format ?? "";
  switch (v.type) {
    case "enum":
    case "split_enum":
      return <EnumControl v={v} value={value as number} format={format} disabled={disabled} ctx={ctx} label={label} />;
    case "composite": {
      const current = compositeIndex(v, ctx.table, ctx.vars);
      return (
        <select className="input input--select" aria-label={label} value={current} disabled={disabled} onChange={(e) => ctx.setValue(v, Number(e.target.value))}>
          {current < 0 && <option value={-1}>Custom</option>}
          {v.composite?.map((c, i) => (
            <option key={i} value={i}>
              {c.label}
            </option>
          ))}
        </select>
      );
    }
    case "constant":
      return format === "checkbox" ? <input type="checkbox" aria-label={label} checked={value === 1} disabled readOnly /> : <span className="num">{String(value)}</span>;
    case "speed_table":
      return <SpeedTableControl v={v} points={value as number[]} disabled={disabled} ctx={ctx} />;
    case "split_text":
      return <TextControl label={label} value={value as string} disabled={disabled} onCommit={(s) => ctx.setValue(v, s)} />;
    case "split_date_time":
      return <span className="num">{String(value)}</span>;
    case "hex":
    case "split_hex": {
      const n = value as number;
      const text = v.case === "upper" ? n.toString(16).toUpperCase() : n.toString(16);
      return (
        <TextControl
          label={label}
          value={text}
          disabled={disabled}
          numeric
          onCommit={(s) => ctx.setValue(v, /^[0-9a-f]+$/i.test(s.trim()) ? Number.parseInt(s, 16) : Number.NaN)}
        />
      );
    }
    default: {
      const n = value as number;
      if (format.startsWith("hslider") || format === "vslider") {
        const max = v.max ?? 255;
        return (
          <span className={format === "vslider" ? "slider slider--v" : "slider"}>
            <input type="range" aria-label={label} min={v.min} max={max} value={n} disabled={disabled} onChange={(e) => ctx.setValue(v, Number(e.target.value))} />
            <span className="num">{format === "hslider-percent" ? `${Math.round((n / max) * 100)}%` : n}</span>
          </span>
        );
      }
      return <TextControl label={label} value={String(n)} disabled={disabled} numeric onCommit={(s) => ctx.setValue(v, s.trim() === "" ? Number.NaN : Number(s))} />;
    }
  }
}

/** A text field that edits a draft and commits on Enter or leaving the field. */
function TextControl({ label, value, disabled, numeric, onCommit }: { label: string; value: string; disabled: boolean; numeric?: boolean; onCommit: (s: string) => void }) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const commit = () => {
    if (draft !== value) onCommit(draft);
  };
  return (
    <input
      className={numeric ? "input input--num" : "input input--text"}
      aria-label={label}
      value={draft}
      disabled={disabled}
      inputMode={numeric ? "numeric" : undefined}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") commit();
        if (e.key === "Escape") setDraft(value);
      }}
    />
  );
}

function EnumControl({ v, value, format, disabled, ctx, label }: { v: DecoderVariable; value: number; format: string; disabled: boolean; ctx: PaneContext; label: string }) {
  const choices = choicesFor(v, value);
  const index = choices.findIndex((c) => c.value === value);
  const choose = (i: number) => {
    if (choices[i]) ctx.setValue(v, choices[i].value);
  };
  switch (format) {
    case "checkbox":
      return <input type="checkbox" aria-label={label} checked={index === 1} disabled={disabled || choices.length < 2} onChange={(e) => choose(e.target.checked ? 1 : 0)} />;
    case "onradiobutton":
    case "offradiobutton": {
      const which = format === "onradiobutton" ? 1 : 0;
      return <input type="radio" aria-label={choices[which]?.label ?? label} checked={index === which} disabled={disabled} onChange={() => choose(which)} />;
    }
    case "radiobuttons":
      return (
        <span className="radios" role="radiogroup" aria-label={label}>
          {choices.map((c, i) => (
            <label key={i} className="check">
              <input type="radio" checked={i === index} disabled={disabled} onChange={() => choose(i)} />
              {c.label}
            </label>
          ))}
        </span>
      );
  }
  const groups = new Map<string, number[]>();
  choices.forEach((c, i) => groups.set(c.group ?? "", [...(groups.get(c.group ?? "") ?? []), i]));
  return (
    <select className="input input--select" aria-label={label} value={index} disabled={disabled} onChange={(e) => choose(Number(e.target.value))}>
      {[...groups].map(([group, idxs]) =>
        group ? (
          <optgroup key={group} label={group}>
            {idxs.map((i) => (
              <option key={i} value={i}>
                {choices[i].label}
              </option>
            ))}
          </optgroup>
        ) : (
          idxs.map((i) => (
            <option key={i} value={i}>
              {choices[i].label}
            </option>
          ))
        ),
      )}
    </select>
  );
}

function SpeedTableControl({ v, points, disabled, ctx }: { v: DecoderVariable; points: number[]; disabled: boolean; ctx: PaneContext }) {
  const st = v.speed_table!;
  return (
    <div className="dec-speed" role="group" aria-label={v.label}>
      {points.map((p, i) => (
        <label key={i} className="dec-speed__point">
          <span className="dec-speed__bar" style={{ height: `${(p / (st.max || 255)) * 100}%` }} />
          <input
            className="input input--num"
            aria-label={`Step ${i + 1}`}
            value={p}
            disabled={disabled || (!!st.mfx && (i === 0 || i === points.length - 1))}
            inputMode="numeric"
            onChange={(e) => {
              const n = Number(e.target.value);
              if (Number.isFinite(n)) ctx.setValue(v, setSpeedPoint(v, points, i, n));
            }}
          />
          <span className="faint num">{i + 1}</span>
        </label>
      ))}
    </div>
  );
}

function AddressField({ ctx }: { ctx: PaneContext }) {
  const items = addressItems(ctx.vars);
  const address = currentAddress(items, ctx.table, ctx.vars);
  const vs = [items.short, items.format, items.long].filter((v): v is DecoderVariable => !!v);
  const unread = vs.some((v) => !isKnown(v, ctx.table));
  const edited = vs.some((v) => isEdited(v, ctx.table));
  const long = items.format ? decode(items.format, ctx.table) === 1 : !!items.long;
  return (
    <div className={["dec-field", edited ? "is-dirty" : "", unread ? "is-unread" : ""].filter(Boolean).join(" ")}>
      <span className="dec-field__label">Address</span>
      <span className="dec-field__control">
        <SourceLamp
          state={sourceOf(
            cvsOf(
              vs.map((v) => v.item),
              ctx.vars,
            ),
            ctx.table,
          )}
        />
        <TextControl label="Address" value={address === undefined ? "" : String(address)} disabled={ctx.busy} numeric onCommit={(s) => ctx.setAddress(Number(s))} />
        <span className="faint">{long ? "long" : "short"}</span>
        {edited && <span className="field__dirty">changed</span>}
      </span>
      {ctx.errors["Address"] && <span className="field__error">{ctx.errors["Address"]}</span>}
    </div>
  );
}

function FunctionMap({ node, ctx }: { node: DecoderNode; ctx: PaneContext }) {
  const m = node.fnmap!;
  return (
    <div className="dec-fnmap">
      <table className="table">
        <thead>
          <tr>
            <th scope="col">Function</th>
            {m.columns.map((c, i) => (
              <th key={i} scope="col" className="dec-fnmap__out">
                {c.name}
                {c.label && <span className="faint">{c.label}</span>}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {m.rows.map((r, ri) => {
            const vs = r.cells.map((cell) => (cell ?? []).map((item) => ctx.vars.get(item)).find((x) => x && available(x, ctx)));
            return (
              <tr key={ri}>
                <th scope="row">
                  <span className="dec-fnmap__fn">
                    <SourceLamp
                      state={sourceOf(
                        cvsOf(
                          vs.filter((v) => !!v).map((v) => v.item),
                          ctx.vars,
                        ),
                        ctx.table,
                      )}
                    />
                    {r.label}
                  </span>
                </th>
                {vs.map((v, ci) => (
                  <td key={ci}>{v && <Control v={v} node={{ kind: "display", format: cellFormat(v) }} ctx={ctx} label={`${r.label}, output ${m.columns[ci].name}`} />}</td>
                ))}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/** A function map cell is a checkbox when it is one bit or a constant. */
function cellFormat(v: DecoderVariable): string {
  if (v.type === "constant") return "checkbox";
  const p = v.parts?.[0];
  return p && p.width === 1 && !p.radix ? "checkbox" : "";
}

const STATE_WORDS: Record<string, string> = {
  unknown: "Not read",
  fromfile: "Default",
  imported: "Imported",
  edited: "Changed",
  read: "Read",
  stored: "Written",
  saved: "Saved",
};

function CVTableView({ ctx }: { ctx: PaneContext }) {
  return (
    <table className="table dec-cvtable">
      <thead>
        <tr>
          <th scope="col">CV</th>
          <th scope="col">Value</th>
          <th scope="col">State</th>
        </tr>
      </thead>
      <tbody>
        {ctx.cvs.map((c) => {
          const cell = ctx.table[c.name];
          const max = c.name.startsWith("T2CV") ? 0xffff : 0xff;
          return (
            <tr key={c.name}>
              <td className="num">{c.name}</td>
              <td>
                <TextControl
                  label={`CV ${c.name}`}
                  value={String(cell?.value ?? 0)}
                  numeric
                  disabled={ctx.busy || !!c.read_only || !!c.info_only}
                  onCommit={(s) => {
                    const n = Number(s);
                    if (Number.isInteger(n) && n >= 0 && n <= max) ctx.setCV(c.name, n);
                  }}
                />
              </td>
              <td className={cell?.state === "edited" ? "field__dirty" : "dim"}>
                <SourceLamp state={cell?.state ?? "unknown"} /> {STATE_WORDS[cell?.state ?? "unknown"]}
                {c.read_only ? ", read-only" : ""}
                {c.write_only ? ", write-only" : ""}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
