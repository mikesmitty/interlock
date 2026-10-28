import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api, type Entity, type EntityKind, type EventName, type KindDef } from "../api";
import { EntityRows } from "../components/EntityRows";
import { EventNameList } from "../components/EventNameList";
import { Empty, Page } from "../components/Page";
import { plural } from "../format";
import { useBus } from "../store";

const KIND_ORDER: EntityKind[] = ["turnout", "block", "reporter", "sensor", "switch", "button"];
const EVENT_RE = /^([0-9a-f]{2}[.:]?){8}$/i;

function matches(e: Entity, needle: string): boolean {
  if (!needle) return true;
  return [e.name, e.node_name ?? "", e.group ?? "", e.kind, e.state, e.id].join(" ").toLowerCase().includes(needle);
}

export function Entities() {
  const { entities, nodes, eventNames, loaded } = useBus();
  const [filter, setFilter] = useState("");
  const [showHidden, setShowHidden] = useState(false);
  const [editing, setEditing] = useState<string>();
  const [adding, setAdding] = useState(false);
  const [kinds, setKinds] = useState<KindDef[]>([]);
  const [message, setMessage] = useState<string>();
  const [refreshing, setRefreshing] = useState(false);

  useEffect(() => {
    api
      .entityKinds()
      .then(setKinds)
      .catch(() => setKinds([]));
  }, []);

  // Open the editor of the entity named in the URL hash, from a node page.
  useEffect(() => {
    const id = decodeURIComponent(window.location.hash.slice(1));
    if (id && entities[id]) {
      setEditing(id);
      document.getElementById(id)?.scrollIntoView({ block: "center" });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded]);

  const all = Object.values(entities);
  const needle = filter.trim().toLowerCase();
  const suggested = all.filter((e) => e.suggested && !e.hidden);
  const hiddenCount = all.filter((e) => e.hidden).length;
  const byKind = useMemo(() => {
    const groups = new Map<EntityKind, Entity[]>();
    for (const kind of KIND_ORDER) groups.set(kind, []);
    for (const e of all) {
      if (e.suggested && !e.hidden) continue;
      if (e.hidden && !showHidden) continue;
      if (!matches(e, needle)) continue;
      groups.get(e.kind)?.push(e);
    }
    for (const list of groups.values()) list.sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
    return groups;
  }, [all, needle, showHidden]);
  const shown = [...byKind.values()].reduce((n, list) => n + list.length, 0);
  const nodeCount = new Set(all.filter((e) => e.node && !e.hidden).map((e) => e.node)).size;
  const kindOf = (k: EntityKind) => kinds.find((d) => d.kind === k);

  async function refresh() {
    setRefreshing(true);
    setMessage(undefined);
    try {
      await api.refreshEntities();
      setMessage("Asked every node for its states.");
    } catch (err) {
      setMessage(`Could not ask: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      window.setTimeout(() => setRefreshing(false), 1000);
    }
  }

  async function decide(e: Entity, confirmed: boolean) {
    try {
      await api.updateEntity(e.id, confirmed ? { confirmed: true } : { hidden: true });
    } catch (err) {
      setMessage(`${e.name}: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  const lead = !loaded
    ? "Loading…"
    : all.length === 0
      ? "Nothing on the layout is known yet."
      : `${plural(all.length - hiddenCount, "entity", "entities")} from ${plural(nodeCount, "node")}${hiddenCount ? `, ${hiddenCount} hidden` : ""}.`;

  return (
    <Page
      title="Entities"
      lead={lead}
      actions={
        <>
          <button className="btn btn--quiet" onClick={() => setAdding((a) => !a)} aria-expanded={adding}>
            {adding ? "Cancel" : "Build one by hand"}
          </button>
          <button className="btn" onClick={() => void refresh()} disabled={refreshing || all.length === 0}>
            {refreshing ? "Asking…" : "Ask nodes for states"}
          </button>
        </>
      }
    >
      {message && <p className="note">{message}</p>}
      <EventNameList names={eventNames} />

      {adding && (
        <AddForm
          kinds={kinds}
          onDone={(created) => {
            setAdding(false);
            if (created) setMessage(`Built ${created.name}.`);
          }}
        />
      )}

      {loaded && all.length === 0 && !adding && (
        <Empty title="No entities yet">
          <p>
            Entities are the things on the layout: turnouts, blocks, reporters, sensors. Interlock builds them from the configuration of nodes it knows a profile for, such as a
            canbus-outpost or a simple-dcc command station, as soon as the node is identified.
          </p>
          <p>Nodes without a profile get suggestions worked out from their event names, which you confirm here. Any entity can also be built by hand from event IDs.</p>
          {Object.values(nodes).filter((n) => !n.local).length === 0 && (
            <p>
              No other node has been seen yet; <Link to="/nodes">Nodes</Link> says how to connect one.
            </p>
          )}
        </Empty>
      )}

      {suggested.length > 0 && (
        <section className="ent-section ent-section--suggested">
          <h2>Suggested</h2>
          <p className="muted">
            Guessed from event names on {plural(new Set(suggested.map((e) => e.node)).size, "node")} without a profile. Confirm the ones that are right; hide the rest.
          </p>
          <div className="panel">
            <table className="table ent-table">
              <tbody>
                {suggested
                  .sort((a, b) => (a.node_name ?? "").localeCompare(b.node_name ?? "") || a.name.localeCompare(b.name))
                  .map((e) => (
                    <tr key={e.id} className="ent-row">
                      <td className="ent-kind">{kindOf(e.kind)?.label ?? e.kind}</td>
                      <td className="ent-name">
                        <div className="cell-title">{e.name}</div>
                        <div className="cell-sub">{[e.node_name, e.group].filter(Boolean).join(" · ")}</div>
                      </td>
                      <td className="ent-detail">
                        <EventList events={e.events} />
                      </td>
                      <td className="ent-controls ent-controls--decide">
                        <button className="btn btn--sm" onClick={() => void decide(e, true)}>
                          Confirm
                        </button>
                        <button className="btn btn--quiet btn--sm" onClick={() => void decide(e, false)}>
                          Hide
                        </button>
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {all.length > 0 && (
        <div className="ent-bar">
          <input
            className="input"
            type="search"
            placeholder="Filter by name, node or state"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            aria-label="Filter entities"
          />
          {hiddenCount > 0 && (
            <label className="check">
              <input type="checkbox" checked={showHidden} onChange={(e) => setShowHidden(e.target.checked)} />
              Show {plural(hiddenCount, "hidden entity", "hidden entities")}
            </label>
          )}
          <span className="ent-bar__count">{needle ? `${shown} shown` : ""}</span>
        </div>
      )}

      {KIND_ORDER.map((kind) => {
        const list = byKind.get(kind) ?? [];
        if (list.length === 0) return null;
        return (
          <section key={kind} className="ent-section">
            <h2>{kindOf(kind)?.plural ?? kind}</h2>
            <div className="panel">
              <table className="table ent-table">
                <tbody>
                  <EntityRows
                    entities={list}
                    editing={editing}
                    onEdit={setEditing}
                    onError={setMessage}
                    editor={(e) => <Editor entity={e} def={kindOf(e.kind)} names={eventNames} onDone={() => setEditing(undefined)} onMessage={setMessage} />}
                  />
                </tbody>
              </table>
            </div>
          </section>
        );
      })}

      {all.length > 0 && shown === 0 && suggested.length === 0 && (
        <p className="muted" style={{ marginTop: "1rem" }}>
          Nothing matches{hiddenCount && !showHidden ? "; the hidden ones are not shown" : ""}.
        </p>
      )}
    </Page>
  );
}

/** The roles of an entity and their event IDs, in one line. */
function EventList({ events }: { events?: Record<string, string> }) {
  if (!events) return null;
  return (
    <span className="ent-events">
      {Object.entries(events).map(([role, id]) => (
        <span key={role} className="ent-event">
          <span className="muted">{role}</span> <code>{id}</code>
        </span>
      ))}
    </span>
  );
}

interface EditorProps {
  entity: Entity;
  def?: KindDef;
  names: EventName[];
  onDone: () => void;
  onMessage: (m: string) => void;
}

/** The inline editor under a row: name, visibility, events, deletion. */
function Editor({ entity, def, names, onDone, onMessage }: EditorProps) {
  const manual = entity.source === "manual";
  const [name, setName] = useState(entity.name);
  const [events, setEvents] = useState<Record<string, string>>(entity.events ?? {});
  const [error, setError] = useState<string>();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [saving, setSaving] = useState(false);
  const nameFor = (id: string) => names.find((n) => n.event.toUpperCase() === id.toUpperCase());

  async function run(label: string, fn: () => Promise<unknown>, done?: string) {
    setSaving(true);
    setError(undefined);
    try {
      await fn();
      if (done) onMessage(done);
      onDone();
    } catch (err) {
      setError(`${label} failed: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setSaving(false);
    }
  }

  function save(ev: FormEvent) {
    ev.preventDefault();
    const trimmed = name.trim();
    const change: Parameters<typeof api.updateEntity>[1] = { name: trimmed === entity.default_name ? "" : trimmed };
    if (manual) {
      for (const [role, id] of Object.entries(events)) {
        if (id && !EVENT_RE.test(id)) {
          setError(`The ${role} event must be 8 bytes of hex, such as 05.01.01.01.22.00.00.01`);
          return;
        }
      }
      change.events = Object.fromEntries(Object.entries(events).filter(([, id]) => id));
    }
    void run("Saving", () => api.updateEntity(entity.id, change));
  }

  return (
    <form className="ent-edit" onSubmit={save}>
      <div className="ent-edit__row">
        <label className="ent-edit__field">
          <span>Name</span>
          <input className="input input--text" value={name} onChange={(e) => setName(e.target.value)} placeholder={entity.default_name} />
        </label>
        {entity.default_name && entity.name !== entity.default_name && (
          <button type="button" className="linklike" onClick={() => setName(entity.default_name ?? "")}>
            use the node's name “{entity.default_name}”
          </button>
        )}
      </div>
      <div className="ent-edit__events">
        {(def?.roles ?? Object.keys(entity.events ?? {}).map((r) => ({ name: r, label: r, required: false, command: false }))).map((role) => {
          const id = events[role.name] ?? "";
          const named = id ? nameFor(id) : undefined;
          return (
            <label key={role.name} className="ent-edit__field">
              <span>
                {role.label}
                {role.required ? "" : <span className="faint"> (optional)</span>}
              </span>
              {manual ? (
                <input
                  className="input input--event"
                  list="event-names"
                  value={id}
                  placeholder="05.01.01.01.22.00.00.01"
                  onChange={(e) => setEvents({ ...events, [role.name]: e.target.value })}
                />
              ) : (
                <span className="ent-edit__value">
                  {id ? <code>{id}</code> : <span className="faint">not set</span>}
                  {named && entity.node && (
                    <Link to={`/nodes/${entity.node}`} className="faint">
                      {" "}
                      {named.path.slice(1).join(" / ")}
                    </Link>
                  )}
                </span>
              )}
            </label>
          );
        })}
      </div>
      {!manual && (
        <p className="muted ent-edit__hint">
          The events come from {entity.node_name ? <Link to={`/nodes/${entity.node}`}>{entity.node_name}</Link> : "the node"}
          's configuration{entity.profile ? ` through the ${entity.profile} profile` : ""}; change them there.
        </p>
      )}
      {error && <p className="field__error">{error}</p>}
      <div className="ent-edit__actions">
        <button type="submit" className="btn btn--primary" disabled={saving}>
          Save
        </button>
        <button type="button" className="btn btn--quiet" onClick={onDone} disabled={saving}>
          Cancel
        </button>
        <button
          type="button"
          className="btn btn--quiet"
          disabled={saving}
          onClick={() => void run(entity.hidden ? "Showing" : "Hiding", () => api.updateEntity(entity.id, { hidden: !entity.hidden }))}
        >
          {entity.hidden ? "Show again" : "Hide"}
        </button>
        {entity.suggested && (
          <button type="button" className="btn" disabled={saving} onClick={() => void run("Confirming", () => api.updateEntity(entity.id, { confirmed: true }))}>
            Confirm guess
          </button>
        )}
        {manual &&
          (confirmDelete ? (
            <span className="confirm">
              <span>Delete {entity.name}?</span>
              <button
                type="button"
                className="btn btn--danger btn--sm"
                disabled={saving}
                onClick={() => void run("Deleting", () => api.deleteEntity(entity.id), `Deleted ${entity.name}.`)}
              >
                Delete
              </button>
              <button type="button" className="btn btn--quiet btn--sm" onClick={() => setConfirmDelete(false)}>
                Keep
              </button>
            </span>
          ) : (
            <button type="button" className="btn btn--danger-quiet" onClick={() => setConfirmDelete(true)}>
              Delete…
            </button>
          ))}
      </div>
    </form>
  );
}

/** Builds an entity by hand from event IDs. */
function AddForm({ kinds, onDone }: { kinds: KindDef[]; onDone: (created?: Entity) => void }) {
  const [kind, setKind] = useState<EntityKind>("turnout");
  const [name, setName] = useState("");
  const [events, setEvents] = useState<Record<string, string>>({});
  const [error, setError] = useState<string>();
  const [saving, setSaving] = useState(false);
  const def = kinds.find((k) => k.kind === kind);

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setError(undefined);
    if (!name.trim()) {
      setError("Give it a name.");
      return;
    }
    for (const role of def?.roles ?? []) {
      const id = events[role.name]?.trim() ?? "";
      if (role.required && !id) {
        setError(`A ${def?.label.toLowerCase()} needs its ${role.label.toLowerCase()} event.`);
        return;
      }
      if (id && !EVENT_RE.test(id)) {
        setError(`The ${role.label.toLowerCase()} event must be 8 bytes of hex, such as 05.01.01.01.22.00.00.01`);
        return;
      }
    }
    setSaving(true);
    try {
      const created = await api.createEntity({ kind, name: name.trim(), events: Object.fromEntries(Object.entries(events).filter(([, id]) => id.trim())) });
      onDone(created);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <form className="panel ent-add" onSubmit={submit}>
      <h2>Build an entity by hand</h2>
      <p className="muted">Pick what it is and the events that drive it. Event fields offer every event named in a node's configuration.</p>
      <div className="ent-edit__row">
        <label className="ent-edit__field">
          <span>Kind</span>
          <select
            className="input input--select"
            value={kind}
            onChange={(e) => {
              setKind(e.target.value as EntityKind);
              setEvents({});
            }}
          >
            {(kinds.length ? kinds : KIND_ORDER.map((k) => ({ kind: k, label: k }))).map((k) => (
              <option key={k.kind} value={k.kind}>
                {k.label}
              </option>
            ))}
          </select>
        </label>
        <label className="ent-edit__field">
          <span>Name</span>
          <input className="input input--text" value={name} onChange={(e) => setName(e.target.value)} placeholder="Yard lead" />
        </label>
      </div>
      <div className="ent-edit__events">
        {(def?.roles ?? []).map((role) => (
          <label key={role.name} className="ent-edit__field">
            <span>
              {role.label}
              {role.required ? "" : <span className="faint"> (optional)</span>}
            </span>
            <input
              className="input input--event"
              list="event-names"
              value={events[role.name] ?? ""}
              placeholder={role.range ? "the range base, ending in 00.00" : "05.01.01.01.22.00.00.01"}
              onChange={(e) => setEvents({ ...events, [role.name]: e.target.value })}
            />
          </label>
        ))}
      </div>
      {error && <p className="field__error">{error}</p>}
      <div className="ent-edit__actions">
        <button type="submit" className="btn btn--primary" disabled={saving}>
          Build
        </button>
        <button type="button" className="btn btn--quiet" onClick={() => onDone()} disabled={saving}>
          Cancel
        </button>
      </div>
    </form>
  );
}
