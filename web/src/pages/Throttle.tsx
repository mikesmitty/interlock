import { useEffect, useRef, useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router";
import { api, type Direction, type FoundTrain, type Train } from "../api";
import { Lamp, Status, type LampColor } from "../components/Lamp";
import { Empty, Page } from "../components/Page";
import { plural, relativeTime, useNow } from "../format";
import { useBus } from "../store";

const FUNCTION_COUNT = 29;
const MAX_SPEED = 126;

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function addressText(t: { address: number; long: boolean }): string {
  if (t.address > 127) return String(t.address);
  return `${t.address} ${t.long ? "long" : "short"}`;
}

/** The lamp beside a train: lit green while this controller drives it. */
function trainLamp(t: Train, nodeNames: Record<string, string>): { color: LampColor; label: string } {
  if (t.controlled) return { color: "green", label: "Driving" };
  if (t.controller)
    return {
      color: "amber",
      label: `Held by ${nodeNames[t.controller] ?? t.controller}`,
    };
  if (t.node && t.online) return { color: "off", label: "Found, not driving" };
  return { color: "off", label: "Not found yet" };
}

export function Throttle() {
  const { trains, layout, nodes, loaded } = useBus();
  const { id } = useParams();
  const navigate = useNavigate();
  const [message, setMessage] = useState<string>();
  const nodeNames: Record<string, string> = {};
  for (const n of Object.values(nodes)) nodeNames[n.id] = n.snip?.UserName || n.id;

  const list = Object.values(trains).sort(
    (a, b) => Number(b.roster) - Number(a.roster) || a.name.localeCompare(b.name) || a.address - b.address,
  );
  const roster = list.filter((t) => t.roster);
  const driving = list.filter((t) => t.controlled).length;
  const selected = id ? trains[id] : undefined;

  useEffect(() => {
    if (!id && loaded && list.length > 0)
      navigate(`/throttle/${encodeURIComponent(list[0].id)}`, {
        replace: true,
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, loaded, list.length]);

  async function setPower(on: boolean) {
    setMessage(undefined);
    try {
      await api.setLayout({ power: on });
    } catch (err) {
      setMessage(`Could not change track power: ${describe(err)}`);
    }
  }

  async function setEStop(stop: boolean) {
    setMessage(undefined);
    try {
      await api.setLayout({ emergency_stop: stop });
    } catch (err) {
      setMessage(`Could not send the emergency stop: ${describe(err)}`);
    }
  }

  const lead = !loaded
    ? "Loading…"
    : roster.length === 0
      ? "No locomotives in the roster yet."
      : `${plural(roster.length, "locomotive")} in the roster${driving ? `, driving ${driving}` : ""}.`;

  return (
    <Page
      title="Throttle"
      lead={lead}
      actions={
        <div className="thr-strip">
          {layout.emergency_stop ? (
            <>
              <Status color="red">Emergency stop</Status>
              <button className="btn" onClick={() => void setEStop(false)}>
                Resume
              </button>
            </>
          ) : (
            <button className="btn btn--stop" onClick={() => void setEStop(true)}>
              Stop all trains
            </button>
          )}
          <Status color={layout.power_off ? "red" : "green"}>
            {layout.power_off ? "Track power off" : "Track power on"}
          </Status>
          <button className="btn btn--quiet" onClick={() => void setPower(layout.power_off)}>
            {layout.power_off ? "Power on" : "Power off"}
          </button>
        </div>
      }
    >
      {message && <p className="note note--error">{message}</p>}
      <div className="thr-layout">
        <Roster list={list} selected={id} nodeNames={nodeNames} onMessage={setMessage} />
        {selected ? (
          <Cab key={selected.id} train={selected} nodeNames={nodeNames} onMessage={setMessage} />
        ) : id ? (
          <Missing id={id} onMessage={setMessage} />
        ) : loaded && list.length === 0 ? (
          <Empty title="Add a locomotive to start">
            <p>
              Give it a name and its DCC address on the left. The first speed you set finds the train on the command
              station and takes control of it.
            </p>
            <p>Trains that other throttles drive also show up here once Interlock has seen them.</p>
          </Empty>
        ) : null}
      </div>
    </Page>
  );
}

function Roster({
  list,
  selected,
  nodeNames,
  onMessage,
}: {
  list: Train[];
  selected?: string;
  nodeNames: Record<string, string>;
  onMessage: (m?: string) => void;
}) {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [address, setAddress] = useState("");
  const [long, setLong] = useState(false);
  const [query, setQuery] = useState("");
  const [found, setFound] = useState<FoundTrain[]>();
  const [busy, setBusy] = useState(false);
  const n = Number(address);

  async function add(e: FormEvent) {
    e.preventDefault();
    if (!address) return;
    setBusy(true);
    onMessage(undefined);
    try {
      const t = await api.createTrain({
        name,
        address: n,
        long: n > 127 || long,
      });
      setName("");
      setAddress("");
      setLong(false);
      navigate(`/throttle/${encodeURIComponent(t.id)}`);
    } catch (err) {
      onMessage(`Could not add the locomotive: ${describe(err)}`);
    } finally {
      setBusy(false);
    }
  }

  async function search(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    onMessage(undefined);
    try {
      setFound(await api.searchTrains(query));
    } catch (err) {
      onMessage(`Could not search: ${describe(err)}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="panel thr-roster">
      <ul className="thr-roster__list">
        {list.map((t) => {
          const lamp = trainLamp(t, nodeNames);
          return (
            <li key={t.id}>
              <button
                type="button"
                className={["thr-roster__item", t.id === selected ? "is-selected" : ""].join(" ")}
                onClick={() => navigate(`/throttle/${encodeURIComponent(t.id)}`)}
                aria-current={t.id === selected ? "true" : undefined}
              >
                <Lamp color={lamp.color} label={lamp.label} />
                <span className="thr-roster__name">
                  {t.name}
                  {!t.roster && <span className="faint"> (not in roster)</span>}
                </span>
                <span className="thr-roster__address">{addressText(t)}</span>
                <span className="thr-roster__speed">
                  {t.estop ? "stop!" : t.speed ? `${t.speed} ${t.direction === "reverse" ? "rev" : "fwd"}` : "0"}
                </span>
              </button>
            </li>
          );
        })}
      </ul>
      <div className="thr-roster__add">
        <form onSubmit={(e) => void add(e)}>
          <input
            className="input"
            placeholder="Name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            aria-label="Name"
          />
          <input
            className="input"
            type="number"
            min={1}
            max={10239}
            placeholder="DCC address"
            value={address}
            onChange={(e) => setAddress(e.target.value)}
            aria-label="DCC address"
            required
          />
          {n > 0 && n <= 127 && (
            <label className="check">
              <input type="checkbox" checked={long} onChange={(e) => setLong(e.target.checked)} /> long address
            </label>
          )}
          <button className="btn btn--primary" type="submit" disabled={busy || !address}>
            Add
          </button>
        </form>
        <form onSubmit={(e) => void search(e)} style={{ marginTop: "0.5rem" }}>
          <input
            className="input"
            placeholder="Find on the bus by address"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Search address"
          />
          <button className="btn btn--quiet" type="submit" disabled={busy || !query}>
            Find
          </button>
        </form>
        {found && (
          <ul className="thr-found">
            {found.length === 0 && <li className="muted">No train answered.</li>}
            {found.map((f) => (
              <li key={f.node}>
                <span>
                  {f.name} <span className="faint">{addressText(f)}</span>
                </span>
                <button className="linklike" onClick={() => navigate(`/throttle/${encodeURIComponent(f.id)}`)}>
                  open
                </button>
                {!f.roster && (
                  <button
                    className="linklike"
                    onClick={() =>
                      void api
                        .createTrain({
                          name: f.name,
                          address: f.address,
                          long: f.long,
                        })
                        .then((t) => navigate(`/throttle/${encodeURIComponent(t.id)}`))
                        .catch((err) => onMessage(describe(err)))
                    }
                  >
                    add to roster
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

/** A train named in the address bar that Interlock has not met yet. */
function Missing({ id, onMessage }: { id: string; onMessage: (m?: string) => void }) {
  const [busy, setBusy] = useState(false);
  const valid = /^\d{1,5}l?$/i.test(id);
  async function take() {
    setBusy(true);
    try {
      await api.acquireTrain(id);
    } catch (err) {
      onMessage(`Could not take control of ${id}: ${describe(err)}`);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Empty title={valid ? `Locomotive ${id}` : "No such train"}>
      {valid ? (
        <>
          <p>
            This locomotive is not in the roster. Interlock can still drive it: taking control asks the command station
            for its train node.
          </p>
          <button className="btn btn--primary" onClick={() => void take()} disabled={busy}>
            {busy ? "Looking…" : `Take control of ${id}`}
          </button>
        </>
      ) : (
        <p>Pick a locomotive on the left, or add one by its DCC address.</p>
      )}
    </Empty>
  );
}

function Cab({
  train: t,
  nodeNames,
  onMessage,
}: {
  train: Train;
  nodeNames: Record<string, string>;
  onMessage: (m?: string) => void;
}) {
  const now = useNow();
  const [busy, setBusy] = useState<string>();
  const [editing, setEditing] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [draft, setDraft] = useState<number>();
  const pending = useRef<{ timer?: number; value?: number; inFlight: boolean }>({ inFlight: false });
  const lamp = trainLamp(t, nodeNames);
  const speed = draft ?? t.speed;

  async function act(label: string, fn: () => Promise<unknown>) {
    setBusy(label);
    onMessage(undefined);
    try {
      await fn();
    } catch (err) {
      onMessage(`${label}: ${describe(err)}`);
    } finally {
      setBusy(undefined);
    }
  }

  /** Sends the newest speed, at most one request in flight at a time. */
  function sendSpeed(value: number, direction?: Direction) {
    const p = pending.current;
    p.value = value;
    if (p.inFlight) return;
    p.inFlight = true;
    const go = async () => {
      const v = p.value;
      p.value = undefined;
      try {
        await api.trainSpeed(t.id, v ?? value, direction);
      } catch (err) {
        onMessage(`Speed: ${describe(err)}`);
      }
      if (p.value !== undefined) {
        void go();
      } else {
        p.inFlight = false;
        setDraft(undefined);
      }
    };
    void go();
  }

  function slide(value: number) {
    setDraft(value);
    sendSpeed(value);
  }

  function step(delta: number) {
    const next = Math.max(0, Math.min(MAX_SPEED, Math.round(speed) + delta));
    setDraft(next);
    sendSpeed(next);
  }

  function direction(dir: Direction) {
    if (dir === t.direction) return;
    sendSpeed(speed, dir);
  }

  const labels = t.functions ?? {};
  const momentary = new Set(t.momentary ?? []);

  return (
    <div className="panel thr-cab">
      <div className="thr-cab__head">
        <h2>{t.name}</h2>
        <span className="thr-cab__sub">
          DCC {addressText(t)}
          {t.node ? ` · node ${t.node}` : ""}
        </span>
        <Status color={lamp.color}>{lamp.label}</Status>
        <div className="thr-cab__actions">
          {t.controlled ? (
            <button
              className="btn btn--quiet"
              onClick={() => void act("Release", () => api.releaseTrain(t.id))}
              disabled={!!busy}
            >
              Release
            </button>
          ) : (
            <button
              className="btn"
              onClick={() => void act("Take control", () => api.acquireTrain(t.id))}
              disabled={!!busy}
            >
              {busy === "Take control" ? "Looking…" : "Take control"}
            </button>
          )}
          <button
            className="btn btn--quiet"
            onClick={() => void act("Refresh", () => api.refreshTrain(t.id))}
            disabled={!!busy}
          >
            Refresh
          </button>
          <button className="btn btn--quiet" onClick={() => setEditing((v) => !v)} aria-pressed={editing}>
            {editing ? "Done" : t.roster ? "Edit" : "Add to roster"}
          </button>
        </div>
      </div>
      <div className="thr-cab__body">
        {editing && <EditTrain train={t} onDone={() => setEditing(false)} onMessage={onMessage} />}
        <div className="thr-speed">
          <div className={["thr-speed__readout", t.estop ? "is-estop" : ""].join(" ")}>
            {t.estop ? "STOP" : Math.round(speed)}
            <small>{t.estop ? "emergency stop" : t.direction}</small>
          </div>
          <div className="thr-speed__controls">
            <input
              className="thr-slider"
              type="range"
              min={0}
              max={MAX_SPEED}
              step={1}
              value={Math.round(speed)}
              onChange={(e) => slide(Number(e.target.value))}
              aria-label={`Speed of ${t.name}`}
            />
            <div className="thr-steps">
              <button className="btn btn--quiet" onClick={() => step(-10)}>
                −10
              </button>
              <button className="btn btn--quiet" onClick={() => step(-1)}>
                −1
              </button>
              <button className="btn btn--quiet" onClick={() => step(1)}>
                +1
              </button>
              <button className="btn btn--quiet" onClick={() => step(10)}>
                +10
              </button>
              <button className="btn" onClick={() => slide(0)}>
                Stop
              </button>
              <button
                className="btn btn--stop btn--primary"
                onClick={() => void act("Emergency stop", () => api.trainEStop(t.id))}
              >
                Emergency stop
              </button>
              <span className="lever" role="group" aria-label="Direction">
                <button
                  type="button"
                  className="lever__opt"
                  aria-pressed={t.direction === "forward"}
                  onClick={() => direction("forward")}
                >
                  Forward
                </button>
                <button
                  type="button"
                  className="lever__opt"
                  aria-pressed={t.direction === "reverse"}
                  onClick={() => direction("reverse")}
                >
                  Reverse
                </button>
              </span>
            </div>
          </div>
        </div>
        <div className="thr-fns" role="group" aria-label="Functions">
          {Array.from({ length: FUNCTION_COUNT }, (_, i) => i).map((fn) => {
            const on = t.on.includes(fn);
            const hold = momentary.has(fn);
            const label = labels[String(fn)];
            if (!label && fn > 12 && !on) return null;
            const send = (value: boolean) =>
              void api.trainFunction(t.id, fn, value).catch((err) => onMessage(`F${fn}: ${describe(err)}`));
            return (
              <button
                key={fn}
                type="button"
                className="thr-fn"
                aria-pressed={on}
                title={hold ? "Acts while held" : undefined}
                onClick={hold ? undefined : () => send(!on)}
                onPointerDown={hold ? () => send(true) : undefined}
                onPointerUp={hold ? () => send(false) : undefined}
                onPointerLeave={hold ? (on ? () => send(false) : undefined) : undefined}
              >
                <span className="thr-fn__num">F{fn}</span>
                <span className="thr-fn__label">{label ?? (fn === 0 ? "Lights" : "")}</span>
              </button>
            );
          })}
        </div>
        <p className="thr-note">
          {t.known
            ? `State read from the train; updated ${relativeTime(t.updated, now)}.`
            : "Not read from the train yet; the first command finds it and takes control."}
          {!t.roster && " This locomotive is not in the roster; add it to keep a name and function labels."}
        </p>
        {t.roster && (
          <p className="thr-note">
            {confirmRemove ? (
              <>
                Remove {t.name} from the roster?{" "}
                <button className="linklike" onClick={() => void act("Remove", () => api.deleteTrain(t.id))}>
                  Yes, remove it
                </button>{" "}
                <button className="linklike" onClick={() => setConfirmRemove(false)}>
                  Keep it
                </button>
              </>
            ) : (
              <button className="linklike" onClick={() => setConfirmRemove(true)}>
                Remove from the roster…
              </button>
            )}
          </p>
        )}
      </div>
    </div>
  );
}

/** The roster fields: name, function labels and which ones are momentary. */
function EditTrain({
  train: t,
  onDone,
  onMessage,
}: {
  train: Train;
  onDone: () => void;
  onMessage: (m?: string) => void;
}) {
  const [name, setName] = useState(t.roster ? t.name : "");
  const [labels, setLabels] = useState<Record<string, string>>({
    ...(t.functions ?? {}),
  });
  const [momentary, setMomentary] = useState<Set<number>>(new Set(t.momentary ?? []));
  const [busy, setBusy] = useState(false);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    onMessage(undefined);
    const functions: Record<string, string> = {};
    for (const [k, v] of Object.entries(labels)) if (v.trim()) functions[k] = v.trim();
    const entry = {
      name,
      functions,
      momentary: [...momentary].sort((a, b) => a - b),
    };
    try {
      if (t.roster) await api.updateTrain(t.id, entry);
      else await api.createTrain({ ...entry, address: t.address, long: t.long });
      onDone();
    } catch (err) {
      onMessage(`Could not save: ${describe(err)}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="thr-edit" onSubmit={(e) => void save(e)}>
      <div className="thr-edit__row">
        <label className="auto-field">
          <span>Name</span>
          <input
            className="input"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={addressText(t)}
          />
        </label>
        <button className="btn btn--primary" type="submit" disabled={busy}>
          {t.roster ? "Save" : "Add to roster"}
        </button>
        <button className="btn btn--quiet" type="button" onClick={onDone}>
          Cancel
        </button>
      </div>
      <div className="thr-fn-edit">
        {Array.from({ length: FUNCTION_COUNT }, (_, i) => i).map((fn) => (
          <label key={fn}>
            <span className="thr-fn__num">F{fn}</span>
            <input
              className="input"
              value={labels[String(fn)] ?? ""}
              onChange={(e) => setLabels({ ...labels, [String(fn)]: e.target.value })}
              placeholder="label"
            />
            <input
              type="checkbox"
              checked={momentary.has(fn)}
              title="Acts while held"
              aria-label={`F${fn} acts while held`}
              onChange={(e) => {
                const next = new Set(momentary);
                if (e.target.checked) next.add(fn);
                else next.delete(fn);
                setMomentary(next);
              }}
            />
          </label>
        ))}
      </div>
      <p className="thr-note">Tick a function that should act only while its key is held, such as a horn.</p>
    </form>
  );
}
