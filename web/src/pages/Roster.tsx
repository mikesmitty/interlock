import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { api, type ImportPreview, type ImportResult, type ImportStep, type Train } from "../api";
import { Status } from "../components/Lamp";
import { Empty, Page } from "../components/Page";
import { plural } from "../format";
import { useBus } from "../store";
import { consistLamp, memberNames } from "./Consist";

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function addressText(t: { address: number; long: boolean }): string {
  if (t.address > 127) return String(t.address);
  return `${t.address} ${t.long ? "long" : "short"}`;
}

/** Road name and number, as "CSX 6641". */
export function roadText(t: { road_name?: string; road_number?: string }): string {
  return [t.road_name, t.road_number].filter(Boolean).join(" ");
}

/** The fitted decoder's name or model. */
export function fittedText(t: Train): string {
  const d = t.decoders?.find((x) => x.id === t.installed);
  if (!d) return t.decoders?.length ? "None fitted" : "";
  return d.name && d.model && d.name !== d.model ? `${d.name}: ${d.model}` : d.name || d.model || d.id;
}

export function Roster() {
  const { trains, consists, loaded } = useBus();
  const navigate = useNavigate();
  const [importing, setImporting] = useState(false);
  const [adding, setAdding] = useState(false);
  const [addingConsist, setAddingConsist] = useState(false);
  const list = Object.values(trains)
    .filter((t) => t.roster)
    .sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
  const consistList = Object.values(consists).sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));

  return (
    <Page
      title="Roster"
      lead={loaded ? (list.length ? `${plural(list.length, "locomotive")}.` : "No locomotives yet.") : "Loading…"}
      actions={
        <>
          <button className="btn" onClick={() => setImporting((v) => !v)} aria-pressed={importing}>
            Import from JMRI
          </button>
          {list.length >= 2 && (
            <button className="btn" onClick={() => setAddingConsist((v) => !v)} aria-pressed={addingConsist}>
              Add a consist
            </button>
          )}
          <button className="btn btn--primary" onClick={() => setAdding((v) => !v)} aria-pressed={adding}>
            Add a locomotive
          </button>
        </>
      }
    >
      {adding && <AddLocomotive onCancel={() => setAdding(false)} />}
      {addingConsist && <AddConsist locomotives={list} onCancel={() => setAddingConsist(false)} />}
      {importing && <ImportPanel onClose={() => setImporting(false)} />}
      {loaded && list.length === 0 && !importing && !adding && (
        <Empty title="Start the roster">
          <p>
            Add each locomotive with its DCC address, or import a JMRI roster: its locomotives come in with their
            decoders, saved CV values and function labels.
          </p>
          <p>
            A locomotive can have more than one decoder, for when you swap them. Each keeps its own settings, and the
            fitted one gives the locomotive its address.
          </p>
        </Empty>
      )}
      {list.length > 0 && (
        <div className="panel">
          <table className="table table--rows ros-table">
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">Address</th>
                <th scope="col" className="ros-col-road">
                  Road
                </th>
                <th scope="col">Decoder</th>
              </tr>
            </thead>
            <tbody>
              {list.map((t) => {
                const open = () => navigate(`/roster/${encodeURIComponent(t.id)}`);
                const others = (t.decoders?.length ?? 0) - (t.installed ? 1 : 0);
                return (
                  <tr
                    key={t.id}
                    className="is-link"
                    tabIndex={0}
                    onClick={open}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") open();
                    }}
                  >
                    <td>
                      <div className="cell-title">{t.name}</div>
                      {(t.manufacturer || t.model) && <div className="cell-sub">{[t.manufacturer, t.model].filter(Boolean).join(" ")}</div>}
                    </td>
                    <td className="num">{addressText(t)}</td>
                    <td className="dim ros-col-road">{roadText(t)}</td>
                    <td>
                      {fittedText(t) || <span className="faint">No decoder</span>}
                      {others > 0 && <span className="faint"> and {plural(others, "other")}</span>}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {consistList.length > 0 && (
        <>
          <h2 className="ros-heading">Consists</h2>
          <div className="panel">
            <table className="table table--rows ros-table">
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Locomotives</th>
                  <th scope="col">State</th>
                </tr>
              </thead>
              <tbody>
                {consistList.map((c) => {
                  const open = () => navigate(`/roster/consists/${encodeURIComponent(c.id)}`);
                  const lamp = consistLamp(c);
                  return (
                    <tr
                      key={c.id}
                      className="is-link"
                      tabIndex={0}
                      onClick={open}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") open();
                      }}
                    >
                      <td>
                        <div className="cell-title">{c.name}</div>
                      </td>
                      <td>{memberNames(c, trains)}</td>
                      <td>
                        <Status color={lamp.color}>{lamp.label}</Status>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </>
      )}
    </Page>
  );
}

/** The form that starts a consist with its lead and one more locomotive. */
function AddConsist({ locomotives, onCancel }: { locomotives: Train[]; onCancel: () => void }) {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [lead, setLead] = useState("");
  const [second, setSecond] = useState("");
  const [reverse, setReverse] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function add(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const c = await api.createConsist({
        name,
        members: [
          { train: lead, reverse: false, f0: false, functions: false },
          { train: second, reverse, f0: false, functions: false },
        ],
      });
      navigate(`/roster/consists/${encodeURIComponent(c.id)}`);
    } catch (err) {
      setError(describe(err));
      setBusy(false);
    }
  }

  const options = (skip: string) =>
    locomotives
      .filter((t) => t.id !== skip)
      .map((t) => (
        <option key={t.id} value={t.id}>
          {t.name} ({addressText(t)})
        </option>
      ));
  return (
    <form className="panel ros-add" onSubmit={(e) => void add(e)}>
      <label className="auto-field">
        <span>Name</span>
        <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="Coal drag" autoFocus required />
      </label>
      <label className="auto-field">
        <span>Lead</span>
        <select className="input input--select" value={lead} onChange={(e) => setLead(e.target.value)} required>
          <option value="">Choose…</option>
          {options(second)}
        </select>
      </label>
      <label className="auto-field">
        <span>Then</span>
        <select className="input input--select" value={second} onChange={(e) => setSecond(e.target.value)} required>
          <option value="">Choose…</option>
          {options(lead)}
        </select>
      </label>
      <label className="check">
        <input type="checkbox" checked={reverse} onChange={(e) => setReverse(e.target.checked)} /> facing backward
      </label>
      <button className="btn btn--primary" type="submit" disabled={busy || !name.trim() || !lead || !second}>
        Add
      </button>
      <button className="btn btn--quiet" type="button" onClick={onCancel}>
        Cancel
      </button>
      {error && <p className="field__error">{error}</p>}
    </form>
  );
}

/** The form that adds a locomotive, with the address filled in when it is known already. */
export function AddLocomotive({ onCancel, start }: { onCancel: () => void; start?: { address: number; long: boolean } }) {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [address, setAddress] = useState(start ? String(start.address) : "");
  const [long, setLong] = useState(start?.long ?? false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const n = Number(address);

  async function add(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const t = await api.createTrain({ name, address: n, long: n > 127 || long });
      navigate(`/roster/${encodeURIComponent(t.id)}`);
    } catch (err) {
      setError(describe(err));
      setBusy(false);
    }
  }

  return (
    <form className="panel ros-add" onSubmit={(e) => void add(e)}>
      <label className="auto-field">
        <span>Name</span>
        <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="CSX 6641" autoFocus />
      </label>
      <label className="auto-field">
        <span>DCC address</span>
        <input className="input input--short" type="number" min={1} max={10239} value={address} onChange={(e) => setAddress(e.target.value)} required />
      </label>
      {n > 0 && n <= 127 && (
        <label className="check">
          <input type="checkbox" checked={long} onChange={(e) => setLong(e.target.checked)} /> long address
        </label>
      )}
      <button className="btn btn--primary" type="submit" disabled={busy || !(n > 0)}>
        Add
      </button>
      <button className="btn btn--quiet" type="button" onClick={onCancel}>
        Cancel
      </button>
      {error && <p className="field__error">{error}</p>}
    </form>
  );
}

/** A plan step's choice as one select value: new, skip, or the locomotive it joins. */
function choiceOf(s: ImportStep): string {
  if (s.action === "decoder") return `into:${s.into ?? ""}`;
  return s.action;
}

function ImportPanel({ onClose }: { onClose: () => void }) {
  const { trains } = useBus();
  const [files, setFiles] = useState<File[]>([]);
  const [preview, setPreview] = useState<ImportPreview>();
  const [plan, setPlan] = useState<ImportStep[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [result, setResult] = useState<ImportResult>();

  async function choose(list: FileList | null) {
    const chosen = list ? Array.from(list) : [];
    setFiles(chosen);
    setPreview(undefined);
    setResult(undefined);
    setError(undefined);
    if (chosen.length === 0) return;
    setBusy(true);
    try {
      const p = await api.importRoster(chosen);
      setPreview(p);
      setPlan(p.plan);
    } catch (err) {
      setError(describe(err));
    } finally {
      setBusy(false);
    }
  }

  async function run() {
    setBusy(true);
    setError(undefined);
    try {
      setResult(await api.applyImport(files, plan));
      setPreview(undefined);
    } catch (err) {
      setError(describe(err));
    } finally {
      setBusy(false);
    }
  }

  function change(key: string, patch: Partial<ImportStep>) {
    setPlan((steps) => {
      const next = steps.map((s) => (s.key === key ? { ...s, ...patch } : s));
      // Decoders headed for a locomotive that is no longer made come in on their own.
      const made = new Set(next.filter((s) => s.action === "new").map((s) => s.key));
      return next.map((s) =>
        s.action === "decoder" && s.into && next.some((o) => o.key === s.into) && !made.has(s.into)
          ? { ...s, action: "new", into: undefined, name: preview?.locomotives.find((l) => l.key === s.key)?.entry.name }
          : s,
      );
    });
  }

  function pick(key: string, value: string) {
    const item = preview?.locomotives.find((l) => l.key === key);
    if (value === "new" || value === "skip") {
      change(key, { action: value, into: undefined, name: value === "new" ? item?.entry.name : undefined });
    } else {
      const into = value.slice("into:".length);
      change(key, { action: "decoder", into, name: item?.decoder?.name || item?.decoder?.model });
    }
  }

  const roster = Object.values(trains)
    .filter((t) => t.roster)
    .sort((a, b) => a.name.localeCompare(b.name));
  const coming = plan.filter((s) => s.action === "new").length;
  const joining = plan.filter((s) => s.action === "decoder").length;

  return (
    <div className="panel ros-import">
      <h2>Import from JMRI</h2>
      <p className="dim">
        Choose <code>roster.xml</code> and the locomotive files from the <code>roster</code> folder of your JMRI profile,
        or a zip of the profile folder. Nothing changes until you import.
      </p>
      {!result && <input className="input" type="file" multiple accept=".xml,.zip" onChange={(e) => void choose(e.target.files)} disabled={busy} />}
      {busy && !preview && <p className="dim">Reading the roster…</p>}
      {error && <p className="note note--error">{error}</p>}

      {preview && (
        <>
          {preview.locomotives.length === 0 ? (
            <p className="note">No locomotives were found in these files.</p>
          ) : (
            <table className="table ros-import__table">
              <thead>
                <tr>
                  <th scope="col">In JMRI</th>
                  <th scope="col">Address</th>
                  <th scope="col">Comes in as</th>
                </tr>
              </thead>
              <tbody>
                {preview.locomotives.map((l) => {
                  const s = plan.find((x) => x.key === l.key)!;
                  const others = plan.filter((x) => x.key !== l.key && x.action === "new");
                  return (
                    <tr key={l.key}>
                      <td>
                        <div className="cell-title">{l.entry.name}</div>
                        <div className="cell-sub">
                          {l.decoder ? (l.decoder.model === l.decoder.family ? l.decoder.model : `${l.decoder.model}, ${l.decoder.family}`) : "No decoder"}
                          {l.decoder && !l.definition && <span className="ros-warn"> (no such definition; choose one later)</span>}
                          {l.cv_count > 0 ? `; ${l.cv_count.toLocaleString()} CV value${l.cv_count === 1 ? "" : "s"}` : "; no CV values"}
                        </div>
                      </td>
                      <td className="num">{addressText(l.entry)}</td>
                      <td>
                        <div className="ros-import__step">
                          <select className="input input--select" value={choiceOf(s)} onChange={(e) => pick(l.key, e.target.value)}>
                            <option value="new">A locomotive of its own</option>
                            {l.decoder && others.length > 0 && (
                              <optgroup label="A decoder of a locomotive in this import">
                                {others.map((o) => (
                                  <option key={o.key} value={`into:${o.key}`}>
                                    {o.name || o.key}
                                  </option>
                                ))}
                              </optgroup>
                            )}
                            {l.decoder && roster.length > 0 && (
                              <optgroup label="A decoder of a locomotive in the roster">
                                {roster.map((t) => (
                                  <option key={t.id} value={`into:${t.id}`}>
                                    {t.name}
                                  </option>
                                ))}
                              </optgroup>
                            )}
                            <option value="skip">Leave it out</option>
                          </select>
                          {s.action === "new" && (
                            <input className="input" aria-label="Locomotive name" value={s.name ?? ""} onChange={(e) => change(l.key, { name: e.target.value })} />
                          )}
                          {s.action === "decoder" && (
                            <>
                              <input className="input" aria-label="Decoder name" value={s.name ?? ""} onChange={(e) => change(l.key, { name: e.target.value })} />
                              <label className="check">
                                <input type="checkbox" checked={!!s.install} onChange={(e) => change(l.key, { install: e.target.checked })} /> fitted now
                              </label>
                            </>
                          )}
                        </div>
                        {s.note && <div className="cell-sub">{s.note}</div>}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
          {preview.problems.length > 0 && (
            <details className="note">
              <summary>{plural(preview.problems.length, "thing")} could not be read</summary>
              <ul>
                {preview.problems.map((p) => (
                  <li key={p}>{p}</li>
                ))}
              </ul>
            </details>
          )}
          <div className="ros-import__actions">
            <button className="btn btn--primary" disabled={busy || coming + joining === 0} onClick={() => void run()}>
              {busy ? "Importing…" : `Import ${plural(coming, "locomotive")}${joining ? ` and ${plural(joining, "more decoder")}` : ""}`}
            </button>
            <button className="btn btn--quiet" onClick={onClose} disabled={busy}>
              Cancel
            </button>
          </div>
        </>
      )}

      {result && (
        <div className="ros-import__done">
          <p>
            {result.error
              ? `The import stopped part way: ${result.error}`
              : `Imported ${plural(result.result.decoders, "decoder")} with ${result.result.cvs.toLocaleString()} CV value${result.result.cvs === 1 ? "" : "s"}${result.result.skipped ? `; left out ${result.result.skipped}` : ""}.`}
          </p>
          <ul>
            {result.result.trains.map((t) => (
              <li key={t.id}>
                <Link to={`/roster/${encodeURIComponent(t.id)}`}>{t.name}</Link> <span className="dim">{fittedText(t)}</span>
              </li>
            ))}
          </ul>
          <button className="btn btn--quiet" onClick={onClose}>
            Done
          </button>
        </div>
      )}
    </div>
  );
}
