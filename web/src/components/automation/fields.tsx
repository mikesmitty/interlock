// The small controls the automation editor is built from.
import type { ReactNode } from "react";
import { nodeName, type Entity, type EntityKind } from "../../api";
import { KIND_STATES, WEEKDAYS } from "../../automation";
import { useBus } from "../../store";

const KIND_ORDER: EntityKind[] = ["turnout", "block", "reporter", "sensor", "switch", "button"];
const KIND_LABEL: Record<EntityKind, string> = {
  turnout: "Turnouts",
  block: "Blocks",
  reporter: "Reporters",
  sensor: "Sensors",
  switch: "Switches",
  button: "Buttons",
};

export function Field({ label, children, wide }: { label: string; children: ReactNode; wide?: boolean }) {
  return (
    <label className={wide ? "auto-field auto-field--wide" : "auto-field"}>
      <span>{label}</span>
      {children}
    </label>
  );
}

export function TypeSelect<T extends string>({ value, options, onChange, label }: { value: T; options: [T, string][]; onChange: (v: T) => void; label: string }) {
  return (
    <select className="input input--select auto-type" value={value} onChange={(e) => onChange(e.target.value as T)} aria-label={label}>
      {options.map(([v, text]) => (
        <option key={v} value={v}>
          {text}
        </option>
      ))}
    </select>
  );
}

export function useEntityList(kinds?: EntityKind[]): Entity[] {
  const { entities } = useBus();
  return Object.values(entities)
    .filter((e) => !e.hidden && (!kinds || kinds.includes(e.kind)))
    .sort((a, b) => KIND_ORDER.indexOf(a.kind) - KIND_ORDER.indexOf(b.kind) || a.name.localeCompare(b.name));
}

export function EntityPicker({ value, onChange, kinds, label = "Entity" }: { value?: string; onChange: (id: string) => void; kinds?: EntityKind[]; label?: string }) {
  const list = useEntityList(kinds);
  const known = list.some((e) => e.id === value);
  return (
    <Field label={label}>
      <select className="input input--select" value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
        <option value="">Pick one…</option>
        {value && !known && <option value={value}>{value} (not found)</option>}
        {KIND_ORDER.filter((k) => list.some((e) => e.kind === k)).map((k) => (
          <optgroup key={k} label={KIND_LABEL[k]}>
            {list
              .filter((e) => e.kind === k)
              .map((e) => (
                <option key={e.id} value={e.id}>
                  {e.name}
                  {e.node_name && e.name !== `${e.node_name} ${e.group ?? ""}`.trim() ? ` · ${e.node_name}` : ""}
                </option>
              ))}
          </optgroup>
        ))}
      </select>
    </Field>
  );
}

export function StatePicker({ entity, value, onChange, label, any }: { entity?: string; value?: string; onChange: (s: string) => void; label: string; any?: string }) {
  const { entities } = useBus();
  const kind = entity ? entities[entity]?.kind : undefined;
  const states = kind ? KIND_STATES[kind] : undefined;
  if (!states) {
    return (
      <Field label={label}>
        <input className="input input--short" value={value ?? ""} placeholder={any ?? "state"} onChange={(e) => onChange(e.target.value)} />
      </Field>
    );
  }
  return (
    <Field label={label}>
      <select className="input input--select" value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
        <option value="">{any ?? "Pick one…"}</option>
        {value && !states.includes(value) && <option value={value}>{value}</option>}
        {states.map((s) => (
          <option key={s} value={s}>
            {s}
          </option>
        ))}
      </select>
    </Field>
  );
}

export function CommandPicker({ entity, value, onChange }: { entity?: string; value?: string; onChange: (s: string) => void }) {
  const { entities } = useBus();
  const commands = entity ? entities[entity]?.commands : undefined;
  if (!commands) {
    return (
      <Field label="Command">
        <input className="input input--short" value={value ?? ""} placeholder="close" onChange={(e) => onChange(e.target.value)} />
      </Field>
    );
  }
  return (
    <Field label="Command">
      <select className="input input--select" value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
        <option value="">Pick one…</option>
        {value && !commands.includes(value) && <option value={value}>{value} (not available)</option>}
        {commands.map((c) => (
          <option key={c} value={c}>
            {c}
          </option>
        ))}
      </select>
    </Field>
  );
}

export function DurationField({ value, onChange, label, placeholder = "3s" }: { value?: string; onChange: (s: string) => void; label: string; placeholder?: string }) {
  return (
    <Field label={label}>
      <input className="input input--short" value={value ?? ""} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
    </Field>
  );
}

export function ClockField({ value, onChange, label }: { value?: string; onChange: (s: string) => void; label: string }) {
  return (
    <Field label={label}>
      <input className="input input--short" type="time" value={value ?? ""} onChange={(e) => onChange(e.target.value)} />
    </Field>
  );
}

export function WeekdayPicker({ value, onChange }: { value?: string[]; onChange: (days: string[] | undefined) => void }) {
  const set = new Set((value ?? []).map((d) => d.toLowerCase()));
  function toggle(day: string) {
    if (set.has(day)) set.delete(day);
    else set.add(day);
    const days = WEEKDAYS.map(([k]) => k).filter((k) => set.has(k));
    onChange(days.length ? days : undefined);
  }
  return (
    <Field label={set.size ? "On" : "Every day"}>
      <span className="auto-days" role="group" aria-label="Weekdays">
        {WEEKDAYS.map(([k, text]) => (
          <button key={k} type="button" className="auto-day" aria-pressed={set.has(k)} onClick={() => toggle(k)}>
            {text}
          </button>
        ))}
      </span>
    </Field>
  );
}

export function EventField({ value, onChange, label = "Event" }: { value?: string; onChange: (s: string) => void; label?: string }) {
  return (
    <Field label={label}>
      <input className="input input--event" list="event-names" value={value ?? ""} placeholder="05.01.01.01.22.00.00.01" onChange={(e) => onChange(e.target.value)} />
    </Field>
  );
}

export function NodePicker({ value, onChange }: { value?: string; onChange: (id: string) => void }) {
  const { nodes } = useBus();
  const list = Object.values(nodes).filter((n) => !n.local);
  return (
    <Field label="Node">
      <select className="input input--select" value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
        <option value="">Any node</option>
        {value && !nodes[value] && <option value={value}>{value}</option>}
        {list.map((n) => (
          <option key={n.id} value={n.id}>
            {nodeName(n)}
          </option>
        ))}
      </select>
    </Field>
  );
}

export function TextField({ value, onChange, label, placeholder, wide }: { value?: string; onChange: (s: string) => void; label: string; placeholder?: string; wide?: boolean }) {
  return (
    <Field label={label} wide={wide}>
      <input className={wide ? "input input--text" : "input input--short"} value={value ?? ""} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
    </Field>
  );
}

export function Check({ checked, onChange, children }: { checked: boolean; onChange: (v: boolean) => void; children: ReactNode }) {
  return (
    <label className="check auto-check">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      {children}
    </label>
  );
}

export function RemoveButton({ onClick, label }: { onClick: () => void; label: string }) {
  return (
    <button type="button" className="btn btn--quiet btn--sm auto-remove" onClick={onClick} aria-label={label} title={label}>
      Remove
    </button>
  );
}

export function AddButton({ onClick, children }: { onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" className="btn btn--quiet btn--sm" onClick={onClick}>
      {children}
    </button>
  );
}

/** Replaces item i of a list. */
export function replaceAt<T>(list: T[], i: number, item: T): T[] {
  const out = list.slice();
  out[i] = item;
  return out;
}

export function removeAt<T>(list: T[], i: number): T[] {
  return list.filter((_, j) => j !== i);
}
