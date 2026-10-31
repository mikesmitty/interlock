import { useState, type ReactNode } from "react";
import { Link } from "react-router";
import { api, type Entity } from "../api";
import { aspectState, aspectWord } from "../aspects";
import { relativeTime, useNow } from "../format";
import { useBus } from "../store";
import { locomotiveName } from "../tracking";
import { Lamp, type LampColor } from "./Lamp";

export interface LampState {
  color: LampColor;
  pulse?: boolean;
  word: string;
}

/** The lamp and word for an entity's state. */
export function lampState(e: Entity): LampState {
  if (!e.available) return { color: "off", word: "Unavailable" };
  switch (e.kind) {
    case "turnout":
      switch (e.state) {
        case "closed":
          return { color: "green", word: "Closed" };
        case "thrown":
          return { color: "amber", word: "Thrown" };
        case "moving":
          return { color: "amber", pulse: true, word: e.commanded === "thrown" ? "Throwing" : e.commanded === "closed" ? "Closing" : "Moving" };
        default:
          return { color: "off", word: e.commanded === "center" ? "Centered" : "Unknown" };
      }
    case "block":
    case "reporter":
      if (e.state === "occupied") return { color: "red", word: "Occupied" };
      if (e.state === "clear") return { color: "green", word: "Clear" };
      return { color: "off", word: "Unknown" };
    case "sensor":
    case "switch":
      if (e.state === "on") return { color: "green", word: "On" };
      if (e.state === "off") return { color: "off", word: "Off" };
      return { color: "off", word: "Unknown" };
    case "button":
      return { color: "off", word: "" };
    case "signal_head":
      return aspectState(e.state);
  }
}

interface LeverOption {
  command: string;
  state: string;
  label: string;
}

const LEVERS: Partial<Record<Entity["kind"], LeverOption[]>> = {
  turnout: [
    { command: "close", state: "closed", label: "Closed" },
    { command: "throw", state: "thrown", label: "Thrown" },
  ],
  switch: [
    { command: "off", state: "off", label: "Off" },
    { command: "on", state: "on", label: "On" },
  ],
};

/** A two position lever: the set position is lit, the other one sends. */
function Lever({ entity, busy, onCommand }: { entity: Entity; busy: boolean; onCommand: (command: string) => void }) {
  const options = LEVERS[entity.kind];
  if (!options) return null;
  const current = entity.state === "moving" ? entity.commanded : entity.state;
  return (
    <span className="lever" role="group" aria-label={`${entity.name} position`}>
      {options.map((o) => (
        <button
          key={o.command}
          type="button"
          className="lever__opt"
          aria-pressed={current === o.state}
          disabled={!entity.available || busy || !entity.commands?.includes(o.command)}
          onClick={() => onCommand(o.command)}
        >
          {o.label}
        </button>
      ))}
    </span>
  );
}

/** The aspects a signal head has: a list, since it can have eleven. */
function AspectSelect({ entity, busy, onCommand }: { entity: Entity; busy: boolean; onCommand: (command: string) => void }) {
  const aspects = entity.commands ?? [];
  const known = aspects.includes(entity.state);
  return (
    <select
      className="input input--select ent-aspect"
      aria-label={`${entity.name} aspect`}
      value={known ? entity.state : ""}
      disabled={!entity.available || busy}
      onChange={(ev) => onCommand(ev.target.value)}
    >
      {!known && <option value="">{aspectWord(entity.state)}</option>}
      {aspects.map((a) => (
        <option key={a} value={a}>
          {aspectWord(a)}
        </option>
      ))}
    </select>
  );
}

/** What a reporter sees, or when the entity last changed. */
function Detail({ e, now }: { e: Entity; now: number }) {
  const { trains } = useBus();
  if (e.kind === "reporter") {
    if (!e.available) return null;
    if (!e.locomotives || e.locomotives.length === 0) {
      return <span className="muted">{e.state === "unknown" ? "Not reported yet" : "Nothing detected"}</span>;
    }
    return (
      <span className="ent-locos">
        {e.locomotives.map((l) => (
          <span key={`${l.address.type}-${l.address.number}`} className="ent-loco">
            <span className="ent-loco__name">{locomotiveName(l, trains)}</span>
            <span className="muted">
              {l.facing ? ` facing ${l.facing}` : ""}
              {l.since ? `, ${relativeTime(l.since, now)}` : ""}
            </span>
          </span>
        ))}
      </span>
    );
  }
  if (e.kind === "button") {
    return <span className="muted">{e.last_event ? `Pressed ${relativeTime(e.last_event, now)}` : "Not pressed yet"}</span>;
  }
  if (e.last_event) return <span className="faint">changed {relativeTime(e.last_event, now)}</span>;
  return null;
}

interface RowsProps {
  entities: Entity[];
  /** Compact rows for the node page: no editor, a link to the entities page. */
  compact?: boolean;
  /** The row whose editor is open, and how to toggle it. */
  editing?: string;
  onEdit?: (id: string | undefined) => void;
  editor?: (e: Entity) => ReactNode;
  onError?: (message: string) => void;
}

/** Table rows for entities: lamp and state, name, detail, controls. */
export function EntityRows({ entities, compact = false, editing, onEdit, editor, onError }: RowsProps) {
  const now = useNow();
  const [busy, setBusy] = useState<Record<string, boolean>>({});

  async function command(e: Entity, cmd: string) {
    setBusy((b) => ({ ...b, [e.id]: true }));
    try {
      await api.command(e.id, cmd);
    } catch (err) {
      onError?.(`${e.name}: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setBusy((b) => ({ ...b, [e.id]: false }));
    }
  }

  return (
    <>
      {entities.map((e) => {
        const lamp = lampState(e);
        const isBusy = busy[e.id] === true;
        return (
          <RowGroup key={e.id}>
            <tr id={e.id} className={["ent-row", e.hidden ? "is-hidden" : "", editing === e.id ? "is-editing" : ""].join(" ").trim()}>
              <td className="ent-state">
                <Lamp color={lamp.color} pulse={lamp.pulse} label={lamp.word || e.kind} />
                <span>{lamp.word}</span>
              </td>
              <td className="ent-name">
                <div className="cell-title">
                  {e.name}
                  {e.suggested && <span className="ent-tag">suggested</span>}
                  {e.hidden && <span className="ent-tag">hidden</span>}
                </div>
                <div className="cell-sub">
                  {compact
                    ? e.group
                    : [e.node_name, e.group].filter(Boolean).join(" · ") || (e.accessory ? `DCC accessory ${e.accessory}` : e.source === "manual" ? "built by hand" : "")}
                </div>
              </td>
              <td className="ent-detail">
                <Detail e={e} now={now} />
              </td>
              <td className="ent-controls">
                <Lever entity={e} busy={isBusy} onCommand={(c) => void command(e, c)} />
                {e.kind === "signal_head" && <AspectSelect entity={e} busy={isBusy} onCommand={(c) => void command(e, c)} />}
                {e.kind === "turnout" && e.commands?.includes("center") && !compact && (
                  <button type="button" className="btn btn--quiet btn--sm" disabled={!e.available || isBusy} onClick={() => void command(e, "center")}>
                    Center
                  </button>
                )}
                {e.kind === "button" && (
                  <button type="button" className="btn btn--sm" disabled={!e.available || isBusy} onClick={() => void command(e, "press")}>
                    Press
                  </button>
                )}
              </td>
              <td className="ent-menu">
                {compact ? (
                  <Link to={`/entities#${encodeURIComponent(e.id)}`}>Open</Link>
                ) : (
                  <button type="button" className="linklike" aria-expanded={editing === e.id} onClick={() => onEdit?.(editing === e.id ? undefined : e.id)}>
                    {editing === e.id ? "Close" : "Edit"}
                  </button>
                )}
              </td>
            </tr>
            {editing === e.id && editor && (
              <tr className="ent-editor">
                <td colSpan={5}>{editor(e)}</td>
              </tr>
            )}
          </RowGroup>
        );
      })}
    </>
  );
}

/** Groups a row with its editor row without extra markup. */
function RowGroup({ children }: { children: ReactNode }) {
  return <>{children}</>;
}
