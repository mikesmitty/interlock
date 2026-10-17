import type { Condition, ConditionType } from "../../api";
import { newCondition } from "../../automation";
import { AddButton, ClockField, EntityPicker, RemoveButton, StatePicker, TextField, TypeSelect, WeekdayPicker, removeAt, replaceAt } from "./fields";

const CONDITION_TYPES: [ConditionType, string][] = [
  ["state", "An entity is in a state"],
  ["time", "The time of day"],
  ["clock", "The fast clock's time"],
  ["trigger", "Which trigger started it"],
  ["and", "All of these"],
  ["or", "Any of these"],
  ["not", "Not this"],
];

interface ConditionListProps {
  conditions: Condition[];
  onChange: (conditions: Condition[]) => void;
  /** "not" takes exactly one condition. */
  max?: number;
  addLabel?: string;
}

export function ConditionList({ conditions, onChange, max, addLabel = "Add a condition" }: ConditionListProps) {
  return (
    <div className="auto-list">
      {conditions.map((c, i) => (
        <div key={i} className={c.conditions ? "auto-row auto-row--block" : "auto-row"}>
          <TypeSelect value={c.type} options={CONDITION_TYPES} onChange={(type) => onChange(replaceAt(conditions, i, newCondition(type)))} label={`Condition ${i + 1} type`} />
          <ConditionFields c={c} onChange={(next) => onChange(replaceAt(conditions, i, next))} />
          <RemoveButton onClick={() => onChange(removeAt(conditions, i))} label={`Remove condition ${i + 1}`} />
        </div>
      ))}
      {(max === undefined || conditions.length < max) && (
        <div className="auto-list__add">
          <AddButton onClick={() => onChange([...conditions, newCondition("state")])}>{conditions.length ? "Add another condition" : addLabel}</AddButton>
        </div>
      )}
    </div>
  );
}

function ConditionFields({ c, onChange }: { c: Condition; onChange: (c: Condition) => void }) {
  const set = (patch: Partial<Condition>) => onChange({ ...c, ...patch });
  switch (c.type) {
    case "state":
      return (
        <>
          <EntityPicker value={c.entity} onChange={(entity) => set({ entity, state: undefined })} />
          <StatePicker entity={c.entity} value={c.state} onChange={(state) => set({ state })} label="Is" />
        </>
      );
    case "time":
    case "clock":
      return (
        <>
          <ClockField value={c.after} onChange={(after) => set({ after })} label="After" />
          <ClockField value={c.before} onChange={(before) => set({ before })} label="Before" />
          <WeekdayPicker value={c.weekdays} onChange={(weekdays) => set({ weekdays })} />
        </>
      );
    case "trigger":
      return <TextField label="Trigger label" value={c.trigger} placeholder="as labeled above" onChange={(trigger) => set({ trigger })} />;
    case "and":
    case "or":
    case "not":
      return (
        <div className="auto-nested">
          <ConditionList conditions={c.conditions ?? []} onChange={(conditions) => set({ conditions })} max={c.type === "not" ? 1 : undefined} />
        </div>
      );
  }
}
