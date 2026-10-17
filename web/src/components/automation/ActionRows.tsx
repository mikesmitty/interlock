import type { ReactNode } from "react";
import type { Action, ActionType, Choice } from "../../api";
import { newAction, newCondition } from "../../automation";
import { ConditionList } from "./ConditionRows";
import { useBus } from "../../store";
import {
  AddButton,
  Check,
  CommandPicker,
  DurationField,
  EntityPicker,
  EventField,
  Field,
  RemoveButton,
  TextField,
  TypeSelect,
  removeAt,
  replaceAt,
} from "./fields";
import { TriggerList } from "./TriggerRows";

const ACTION_TYPES: [ActionType, string][] = [
  ["command", "Command an entity"],
  ["train", "Drive a train"],
  ["event", "Send an event"],
  ["delay", "Wait a while"],
  ["wait", "Wait for something"],
  ["if", "If … then"],
  ["choose", "Choose"],
  ["repeat", "Repeat"],
  ["stop", "Stop"],
  ["log", "Write to the log"],
];

const BLOCKS: ActionType[] = ["wait", "if", "choose", "repeat"];

interface ActionListProps {
  actions: Action[];
  onChange: (actions: Action[]) => void;
  addLabel?: string;
}

export function ActionList({ actions, onChange, addLabel = "Add an action" }: ActionListProps) {
  return (
    <div className="auto-list">
      {actions.map((a, i) => (
        <div
          key={i}
          className={[
            BLOCKS.includes(a.type) ? "auto-row auto-row--block" : "auto-row",
            a.disabled ? "is-off" : "",
          ].join(" ")}
        >
          <TypeSelect
            value={a.type}
            options={ACTION_TYPES}
            onChange={(type) => onChange(replaceAt(actions, i, newAction(type)))}
            label={`Action ${i + 1} type`}
          />
          <ActionFields a={a} onChange={(next) => onChange(replaceAt(actions, i, next))} />
          <Check
            checked={!a.disabled}
            onChange={(on) =>
              onChange(
                replaceAt(actions, i, {
                  ...a,
                  disabled: on ? undefined : true,
                }),
              )
            }
          >
            on
          </Check>
          <RemoveButton onClick={() => onChange(removeAt(actions, i))} label={`Remove action ${i + 1}`} />
        </div>
      ))}
      <div className="auto-list__add">
        <AddButton onClick={() => onChange([...actions, newAction("command")])}>
          {actions.length ? "Add another action" : addLabel}
        </AddButton>
      </div>
    </div>
  );
}

function ActionFields({ a, onChange }: { a: Action; onChange: (a: Action) => void }) {
  const set = (patch: Partial<Action>) => onChange({ ...a, ...patch });
  switch (a.type) {
    case "command":
      return (
        <>
          <EntityPicker value={a.entity} onChange={(entity) => set({ entity, command: undefined })} />
          <CommandPicker entity={a.entity} value={a.command} onChange={(command) => set({ command })} />
        </>
      );
    case "train":
      return <TrainFields a={a} set={set} />;
    case "event":
      return <EventField value={a.event} onChange={(event) => set({ event })} />;
    case "delay":
      return <DurationField value={a.for} onChange={(v) => set({ for: v })} label="For" />;
    case "wait":
      return (
        <div className="auto-nested">
          <TriggerList triggers={a.triggers ?? []} onChange={(triggers) => set({ triggers })} allowStart={false} />
          <div className="auto-row auto-row--plain">
            <DurationField
              value={a.timeout}
              onChange={(timeout) => set({ timeout })}
              label="Give up after"
              placeholder="never"
            />
            <Check checked={!!a.stop_on_timeout} onChange={(v) => set({ stop_on_timeout: v || undefined })}>
              stop the run when it gives up
            </Check>
          </div>
        </div>
      );
    case "if":
      return (
        <div className="auto-nested">
          <Section label="If">
            <ConditionList conditions={a.conditions ?? []} onChange={(conditions) => set({ conditions })} />
          </Section>
          <Section label="Then">
            <ActionList actions={a.then ?? []} onChange={(then) => set({ then })} />
          </Section>
          <Section label="Else">
            <ActionList
              actions={a.else ?? []}
              onChange={(els) => set({ else: els })}
              addLabel="Add an action for else"
            />
          </Section>
        </div>
      );
    case "choose":
      return (
        <div className="auto-nested">
          {(a.choices ?? []).map((ch, i) => (
            <Section key={i} label={`Option ${i + 1}`} onRemove={() => set({ choices: removeAt(a.choices ?? [], i) })}>
              <ChoiceFields choice={ch} onChange={(next) => set({ choices: replaceAt(a.choices ?? [], i, next) })} />
            </Section>
          ))}
          <div className="auto-list__add">
            <AddButton
              onClick={() =>
                set({
                  choices: [
                    ...(a.choices ?? []),
                    {
                      conditions: [newCondition("state")],
                      actions: [newAction("command")],
                    },
                  ],
                })
              }
            >
              Add an option
            </AddButton>
          </div>
          <Section label="Otherwise">
            <ActionList
              actions={a.default ?? []}
              onChange={(def) => set({ default: def })}
              addLabel="Add an action for when nothing matches"
            />
          </Section>
        </div>
      );
    case "repeat": {
      const mode = a.while ? "while" : a.until ? "until" : "count";
      return (
        <div className="auto-nested">
          <div className="auto-row auto-row--plain">
            <Field label="Repeat">
              <select
                className="input input--select"
                value={mode}
                onChange={(e) => {
                  const m = e.target.value;
                  set({
                    count: m === "count" ? 2 : undefined,
                    while: m === "while" ? [newCondition("state")] : undefined,
                    until: m === "until" ? [newCondition("state")] : undefined,
                  });
                }}
              >
                <option value="count">a number of times</option>
                <option value="while">while a condition holds</option>
                <option value="until">until a condition holds</option>
              </select>
            </Field>
            {mode === "count" && (
              <Field label="Times">
                <input
                  className="input input--num"
                  type="number"
                  min={1}
                  max={1000}
                  value={a.count ?? 1}
                  onChange={(e) => set({ count: Number(e.target.value) })}
                />
              </Field>
            )}
          </div>
          {mode === "while" && (
            <Section label="While">
              <ConditionList conditions={a.while ?? []} onChange={(w) => set({ while: w })} />
            </Section>
          )}
          {mode === "until" && (
            <Section label="Until">
              <ConditionList conditions={a.until ?? []} onChange={(u) => set({ until: u })} />
            </Section>
          )}
          <Section label="Do">
            <ActionList actions={a.actions ?? []} onChange={(actions) => set({ actions })} />
          </Section>
        </div>
      );
    }
    case "stop":
      return (
        <>
          <TextField
            label="Reason"
            value={a.reason}
            placeholder="optional"
            onChange={(reason) => set({ reason })}
            wide
          />
          <Check checked={!!a.error} onChange={(v) => set({ error: v || undefined })}>
            count it as a failure
          </Check>
        </>
      );
    case "log":
      return (
        <TextField
          label="Message"
          value={a.message}
          placeholder="What to write"
          onChange={(message) => set({ message })}
          wide
        />
      );
  }
}

function ChoiceFields({ choice, onChange }: { choice: Choice; onChange: (c: Choice) => void }) {
  return (
    <>
      <div className="auto-row auto-row--plain">
        <TextField
          label="Name"
          value={choice.name}
          placeholder="optional"
          onChange={(name) => onChange({ ...choice, name })}
        />
      </div>
      <Section label="When">
        <ConditionList conditions={choice.conditions} onChange={(conditions) => onChange({ ...choice, conditions })} />
      </Section>
      <Section label="Do">
        <ActionList actions={choice.actions} onChange={(actions) => onChange({ ...choice, actions })} />
      </Section>
    </>
  );
}

function Section({ label, children, onRemove }: { label: string; children: ReactNode; onRemove?: () => void }) {
  return (
    <div className="auto-section">
      <div className="auto-section__label">
        <span>{label}</span>
        {onRemove && <RemoveButton onClick={onRemove} label={`Remove ${label}`} />}
      </div>
      {children}
    </div>
  );
}

const TRAIN_COMMANDS: [string, string][] = [
  ["speed", "Set speed"],
  ["stop", "Stop"],
  ["estop", "Emergency stop"],
  ["function", "Set a function"],
];

/** The fields of a train action: which locomotive, and what to do. */
function TrainFields({ a, set }: { a: Action; set: (patch: Partial<Action>) => void }) {
  const { trains } = useBus();
  const roster = Object.values(trains).filter((t) => t.roster);
  const address = a.address ?? 0;
  return (
    <>
      <Field label="Locomotive">
        <input
          className="input input--short"
          type="number"
          min={1}
          max={10239}
          list="roster-addresses"
          placeholder="address"
          value={a.address ?? ""}
          onChange={(e) =>
            set({
              address: e.target.value ? Number(e.target.value) : undefined,
            })
          }
        />
        <datalist id="roster-addresses">
          {roster.map((t) => (
            <option key={t.id} value={t.address}>
              {t.name}
            </option>
          ))}
        </datalist>
      </Field>
      {address > 0 && address <= 127 && (
        <Field label="Address kind">
          <select
            className="input input--select"
            value={a.address_type ?? "short"}
            onChange={(e) =>
              set({
                address_type: e.target.value === "long" ? "long" : undefined,
              })
            }
          >
            <option value="short">short</option>
            <option value="long">long</option>
          </select>
        </Field>
      )}
      <Field label="Do">
        <select
          className="input input--select"
          value={a.command ?? "speed"}
          onChange={(e) =>
            set({
              command: e.target.value,
              speed: undefined,
              direction: undefined,
              function: undefined,
              state: undefined,
            })
          }
        >
          {TRAIN_COMMANDS.map(([v, text]) => (
            <option key={v} value={v}>
              {text}
            </option>
          ))}
        </select>
      </Field>
      {(a.command ?? "speed") === "speed" && (
        <>
          <Field label="Speed (0 to 126)">
            <input
              className="input input--short"
              type="number"
              min={0}
              max={126}
              value={a.speed ?? 0}
              onChange={(e) => set({ speed: Number(e.target.value) })}
            />
          </Field>
          <Field label="Direction">
            <select
              className="input input--select"
              value={a.direction ?? ""}
              onChange={(e) =>
                set({
                  direction: (e.target.value || undefined) as Action["direction"],
                })
              }
            >
              <option value="">as it is</option>
              <option value="forward">forward</option>
              <option value="reverse">reverse</option>
            </select>
          </Field>
        </>
      )}
      {a.command === "function" && (
        <>
          <Field label="Function">
            <input
              className="input input--short"
              type="number"
              min={0}
              max={68}
              value={a.function ?? 0}
              onChange={(e) => set({ function: Number(e.target.value) })}
            />
          </Field>
          <Field label="To">
            <select
              className="input input--select"
              value={a.state ?? "on"}
              onChange={(e) => set({ state: e.target.value === "off" ? "off" : "on" })}
            >
              <option value="on">on</option>
              <option value="off">off</option>
            </select>
          </Field>
        </>
      )}
    </>
  );
}
