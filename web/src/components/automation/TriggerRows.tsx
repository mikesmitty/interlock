import type { Trigger, TriggerType } from "../../api";
import { newTrigger } from "../../automation";
import { AddButton, ClockField, DurationField, EntityPicker, EventField, Field, NodePicker, RemoveButton, StatePicker, TextField, TypeSelect, WeekdayPicker, removeAt, replaceAt } from "./fields";

const TRIGGER_TYPES: [TriggerType, string][] = [
  ["state", "An entity changes state"],
  ["locomotive", "A locomotive is seen"],
  ["event", "An event is received"],
  ["node", "A node comes or goes"],
  ["time", "A time of day"],
  ["clock", "The fast clock reads"],
  ["interval", "Every so often"],
  ["start", "Interlock starts"],
];

interface TriggerListProps {
  triggers: Trigger[];
  onChange: (triggers: Trigger[]) => void;
  /** Wait actions cannot wait for Interlock to start. */
  allowStart?: boolean;
  /** Trigger labels only matter at the top level. */
  labels?: boolean;
}

export function TriggerList({ triggers, onChange, allowStart = true, labels = false }: TriggerListProps) {
  const types = allowStart ? TRIGGER_TYPES : TRIGGER_TYPES.filter(([t]) => t !== "start");
  return (
    <div className="auto-list">
      {triggers.map((t, i) => (
        <div key={i} className="auto-row">
          <TypeSelect value={t.type} options={types} onChange={(type) => onChange(replaceAt(triggers, i, newTrigger(type)))} label={`Trigger ${i + 1} type`} />
          <TriggerFields t={t} onChange={(next) => onChange(replaceAt(triggers, i, next))} />
          {labels && <TextField label="Label" value={t.id} placeholder="optional" onChange={(id) => onChange(replaceAt(triggers, i, { ...t, id }))} />}
          <RemoveButton onClick={() => onChange(removeAt(triggers, i))} label={`Remove trigger ${i + 1}`} />
        </div>
      ))}
      <div className="auto-list__add">
        <AddButton onClick={() => onChange([...triggers, newTrigger("state")])}>{triggers.length ? "Add another trigger" : "Add a trigger"}</AddButton>
      </div>
    </div>
  );
}

function TriggerFields({ t, onChange }: { t: Trigger; onChange: (t: Trigger) => void }) {
  const set = (patch: Partial<Trigger>) => onChange({ ...t, ...patch });
  switch (t.type) {
    case "state":
      return (
        <>
          <EntityPicker value={t.entity} onChange={(entity) => set({ entity, from: undefined, to: undefined })} />
          <StatePicker entity={t.entity} value={t.from} onChange={(from) => set({ from })} label="From" any="any state" />
          <StatePicker entity={t.entity} value={t.to} onChange={(to) => set({ to })} label="To" any="any state" />
          <DurationField value={t.for} onChange={(v) => set({ for: v })} label="Held for" placeholder="at once" />
        </>
      );
    case "event":
      return <EventField value={t.event} onChange={(event) => set({ event })} />;
    case "locomotive":
      return (
        <>
          <EntityPicker value={t.entity} onChange={(entity) => set({ entity })} kinds={["reporter"]} label="Reporter" />
          <Field label="Movement">
            <select className="input input--select" value={t.direction ?? ""} onChange={(e) => set({ direction: (e.target.value || undefined) as Trigger["direction"] })}>
              <option value="entered">enters</option>
              <option value="left">leaves</option>
              <option value="">enters or leaves</option>
            </select>
          </Field>
          <Field label="Address">
            <input
              className="input input--num"
              type="number"
              min={0}
              max={10239}
              value={t.address ?? ""}
              placeholder="any"
              onChange={(e) => set({ address: e.target.value ? Number(e.target.value) : undefined })}
            />
          </Field>
          {t.address ? (
            <Field label="Kind">
              <select className="input input--select" value={t.address_type ?? "long"} onChange={(e) => set({ address_type: e.target.value as Trigger["address_type"] })}>
                <option value="long">long</option>
                <option value="short">short</option>
                <option value="consist">consist</option>
              </select>
            </Field>
          ) : null}
        </>
      );
    case "node":
      return (
        <>
          <NodePicker value={t.node} onChange={(node) => set({ node })} />
          <Field label="Change">
            <select className="input input--select" value={t.to ?? ""} onChange={(e) => set({ to: e.target.value || undefined })}>
              <option value="online">comes online</option>
              <option value="offline">goes offline</option>
              <option value="">either</option>
            </select>
          </Field>
        </>
      );
    case "time":
    case "clock":
      return (
        <>
          <ClockField value={t.at} onChange={(at) => set({ at })} label={t.type === "clock" ? "Fast time" : "At"} />
          <WeekdayPicker value={t.weekdays} onChange={(weekdays) => set({ weekdays })} />
        </>
      );
    case "interval":
      return <DurationField value={t.every} onChange={(every) => set({ every })} label="Every" placeholder="10m" />;
    case "start":
      return null;
  }
}
