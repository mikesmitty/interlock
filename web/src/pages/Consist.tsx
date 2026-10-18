import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, type Consist, type ConsistMember, type Train } from "../api";
import { Status, type LampColor } from "../components/Lamp";
import { Empty, Page } from "../components/Page";
import { plural } from "../format";
import { useBus } from "../store";
import { addressText } from "./Roster";

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** The lamp for a consist: lit green while the command station has it together. */
export function consistLamp(c: Consist): { color: LampColor; label: string } {
  if (!c.made_up) return { color: "off", label: "Apart" };
  if (c.problem) return { color: "red", label: "Could not be put together" };
  if (!c.attached) return { color: "amber", label: "Made up; put together when next driven" };
  return { color: "green", label: "Made up" };
}

/** The names of a consist's locomotives, the lead first. */
export function memberNames(c: Consist, trains: Record<string, Train>): string {
  return c.members.map((m) => trains[m.train]?.name ?? m.train).join(", ");
}

export function ConsistPage() {
  const { id = "" } = useParams();
  const { consists, trains, loaded } = useBus();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const c = consists[id];
  const back = (
    <Link className="page__back" to="/roster">
      Roster
    </Link>
  );
  if (!loaded) {
    return (
      <Page title="Consist" back={back} lead="Loading…">
        {null}
      </Page>
    );
  }
  if (!c) {
    return (
      <Page title="Consist" back={back}>
        <Empty title="No such consist">
          <p>It may have been removed. Consists are listed on the Roster page.</p>
        </Empty>
      </Page>
    );
  }

  async function act(label: string, fn: () => Promise<unknown>) {
    setBusy(true);
    setMessage(undefined);
    try {
      await fn();
    } catch (err) {
      setMessage(`${label}: ${describe(err)}`);
    } finally {
      setBusy(false);
    }
  }

  const lead = trains[c.members[0]?.train];
  const lamp = consistLamp(c);
  return (
    <Page
      title={c.name}
      back={back}
      lead={`${plural(c.members.length, "locomotive")}${lead ? `, led by ${lead.name}` : ""}.`}
      actions={
        <>
          {c.made_up ? (
            <button className="btn" disabled={busy} onClick={() => void act("Break up", () => api.breakUpConsist(c.id))}>
              Break up
            </button>
          ) : (
            <button
              className="btn btn--primary"
              disabled={busy || c.members.length < 2}
              onClick={() => void act("Make up", () => api.makeUpConsist(c.id))}
            >
              {busy ? "Making up…" : "Make up"}
            </button>
          )}
          {lead && (
            <Link className="btn" to={`/throttle/${encodeURIComponent(lead.id)}`}>
              Drive
            </Link>
          )}
        </>
      }
    >
      <div className="ros-layout">
        <div className="panel ros-details">
          <Status color={lamp.color}>{lamp.label}</Status>
          {message && <p className="note note--error">{message}</p>}
          {c.made_up && c.problem && <p className="note note--error">{c.problem}</p>}
          <p className="thr-note">
            {c.made_up
              ? `The command station passes ${lead?.name ?? "the lead"}'s speed on to the others, with the functions each one follows. Drive the lead to drive the consist; stop it before breaking it up.`
              : "Making up the consist tells the command station to pass the lead's speed on to the others. Nothing is written to the decoders. A simple-dcc command station takes up to four locomotives besides the lead, all on that command station."}
          </p>
        </div>
        <Members key={JSON.stringify(c.members)} consist={c} trains={trains} />
        <Name key={c.name} consist={c} />
        <Remove consist={c} />
      </div>
    </Page>
  );
}

/** The locomotives in order, with how each one faces and what it follows. */
function Members({ consist: c, trains }: { consist: Consist; trains: Record<string, Train> }) {
  const [rows, setRows] = useState<ConsistMember[]>(c.members);
  const [adding, setAdding] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const locked = c.made_up;
  const dirty = JSON.stringify(rows) !== JSON.stringify(c.members);
  const addresses = new Set(rows.map((m) => trains[m.train]).filter(Boolean).map((t) => `${t.address}:${t.long}`));
  const choices = Object.values(trains)
    .filter((t) => t.roster && !addresses.has(`${t.address}:${t.long}`))
    .sort((a, b) => a.name.localeCompare(b.name));

  const change = (i: number, patch: Partial<ConsistMember>) => setRows(rows.map((m, j) => (j === i ? { ...m, ...patch } : m)));

  function makeLead(i: number) {
    const next = [...rows];
    const [m] = next.splice(i, 1);
    const old = next[0];
    // The old lead keeps running the same way and follows nothing until told.
    next[0] = { ...old, reverse: false, f0: false, functions: false };
    setRows([{ ...m, reverse: false, f0: false, functions: false }, ...next]);
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMessage(undefined);
    try {
      await api.updateConsist(c.id, { members: rows });
    } catch (err) {
      setMessage(`Could not save: ${describe(err)}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="panel ros-decoders" onSubmit={(e) => void save(e)}>
      <h2>Locomotives</h2>
      {locked && <p className="thr-note">Break the consist up to change its locomotives.</p>}
      <table className="table">
        <thead>
          <tr>
            <th scope="col" className="num">
              Place
            </th>
            <th scope="col">Locomotive</th>
            <th scope="col">Faces</th>
            <th scope="col">Follows the lead's</th>
            {!locked && <th scope="col" aria-label="Actions" />}
          </tr>
        </thead>
        <tbody>
          {rows.map((m, i) => {
            const t = trains[m.train];
            return (
              <tr key={m.train}>
                <td className="num dim">{i === 0 ? "Lead" : i + 1}</td>
                <td>
                  {t ? <Link to={`/roster/${encodeURIComponent(t.id)}`}>{t.name}</Link> : m.train}
                  {t && <span className="faint"> {addressText(t)}</span>}
                </td>
                <td>
                  {i === 0 ? (
                    <span className="dim">Forward</span>
                  ) : locked ? (
                    m.reverse ? "Backward" : "Forward"
                  ) : (
                    <select
                      className="input input--select"
                      value={m.reverse ? "backward" : "forward"}
                      onChange={(e) => change(i, { reverse: e.target.value === "backward" })}
                      aria-label={`Which way ${t?.name ?? m.train} faces`}
                    >
                      <option value="forward">Forward</option>
                      <option value="backward">Backward</option>
                    </select>
                  )}
                </td>
                <td>
                  {i === 0 ? (
                    <span className="faint">its own</span>
                  ) : (
                    <span className="ros-actions">
                      <label className="check">
                        <input type="checkbox" checked={m.f0} disabled={locked} onChange={(e) => change(i, { f0: e.target.checked })} /> F0
                      </label>
                      <label className="check">
                        <input
                          type="checkbox"
                          checked={m.functions}
                          disabled={locked}
                          onChange={(e) => change(i, { functions: e.target.checked })}
                        />{" "}
                        other functions
                      </label>
                    </span>
                  )}
                </td>
                {!locked && (
                  <td>
                    <span className="ros-actions">
                      {i > 0 && (
                        <button type="button" className="linklike" onClick={() => makeLead(i)}>
                          Make lead
                        </button>
                      )}
                      <button type="button" className="linklike" onClick={() => setRows(rows.filter((_, j) => j !== i))}>
                        Remove
                      </button>
                    </span>
                  </td>
                )}
              </tr>
            );
          })}
        </tbody>
      </table>
      {!locked && (
        <div className="ros-actions">
          <select className="input input--select" value={adding} onChange={(e) => setAdding(e.target.value)} aria-label="Locomotive to add">
            <option value="">Add a locomotive…</option>
            {choices.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name} ({addressText(t)})
              </option>
            ))}
          </select>
          <button
            type="button"
            className="btn"
            disabled={!adding}
            onClick={() => {
              setRows([...rows, { train: adding, reverse: false, f0: false, functions: false }]);
              setAdding("");
            }}
          >
            Add
          </button>
        </div>
      )}
      {!locked && (
        <div className="ros-actions">
          <button className="btn btn--primary" type="submit" disabled={busy || !dirty || rows.length < 2}>
            Save locomotives
          </button>
          {dirty && (
            <button type="button" className="btn btn--quiet" onClick={() => setRows(c.members)}>
              Discard
            </button>
          )}
          {rows.length < 2 && <span className="dim">A consist needs two locomotives or more.</span>}
          {message && <span className="field__error">{message}</span>}
        </div>
      )}
    </form>
  );
}

function Name({ consist: c }: { consist: Consist }) {
  const [name, setName] = useState(c.name);
  const [message, setMessage] = useState<string>();
  async function save(e: FormEvent) {
    e.preventDefault();
    setMessage(undefined);
    try {
      await api.updateConsist(c.id, { name });
    } catch (err) {
      setMessage(`Could not save: ${describe(err)}`);
    }
  }
  return (
    <form className="panel ros-add" onSubmit={(e) => void save(e)}>
      <label className="auto-field">
        <span>Name</span>
        <input className="input" value={name} onChange={(e) => setName(e.target.value)} required />
      </label>
      <button className="btn" type="submit" disabled={!name.trim() || name === c.name}>
        Rename
      </button>
      {message && <p className="field__error">{message}</p>}
    </form>
  );
}

function Remove({ consist: c }: { consist: Consist }) {
  const navigate = useNavigate();
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState<string>();
  if (c.made_up) return null;
  async function remove() {
    try {
      await api.deleteConsist(c.id);
      navigate("/roster");
    } catch (err) {
      setError(describe(err));
    }
  }
  return (
    <p className="thr-note">
      {confirm ? (
        <>
          Remove the consist {c.name}? The locomotives stay in the roster.{" "}
          <button className="linklike" onClick={() => void remove()}>
            Yes, remove it
          </button>{" "}
          <button className="linklike" onClick={() => setConfirm(false)}>
            Keep it
          </button>
        </>
      ) : (
        <button className="linklike" onClick={() => setConfirm(true)}>
          Remove this consist…
        </button>
      )}
      {error && <span className="field__error"> {error}</span>}
    </p>
  );
}
