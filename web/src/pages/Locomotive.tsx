import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, type RosterDecoder, type SavedCVs, type Train } from "../api";
import { DecoderPicker, rosterIdentity, rosterSettingsLink, type PickedDefinition } from "../components/decoder/DecoderPicker";
import { Lamp } from "../components/Lamp";
import { Empty, Page } from "../components/Page";
import type { DecoderIdentity } from "../decoder/types";
import { plural, relativeTime, useNow } from "../format";
import { formationOf } from "../makeup";
import { useBus } from "../store";
import { addressText, roadText } from "./Roster";

const FUNCTION_COUNT = 29;

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function Locomotive() {
  const { id = "" } = useParams();
  const { trains, consists, formations, loaded } = useBus();
  const t = trains[id];
  const back = (
    <Link className="page__back" to="/roster">
      Roster
    </Link>
  );
  if (!loaded) {
    return (
      <Page title="Locomotive" back={back} lead="Loading…">
        {null}
      </Page>
    );
  }
  if (!t?.roster) {
    return (
      <Page title="Locomotive" back={back}>
        <Empty title="Not in the roster">
          <p>No locomotive in the roster has this ID. It may have been removed, or renamed when its address changed.</p>
        </Empty>
      </Page>
    );
  }
  const lead = [`DCC ${addressText(t)}`, roadText(t), [t.manufacturer, t.model].filter(Boolean).join(" ")].filter(Boolean).join(", ");
  const holder = formationOf({ locomotive: t.id }, formations);
  return (
    <Page
      title={t.name}
      back={back}
      lead={`${lead}.`}
      actions={
        <Link className="btn" to={`/throttle/${encodeURIComponent(t.id)}`}>
          Drive
        </Link>
      }
    >
      <div className="ros-layout">
        {holder && (
          <p className="note">
            In <Link to={`/roster/trains/${encodeURIComponent(holder.id)}`}>{holder.name}</Link>.
          </p>
        )}
        {t.consist && consists[t.consist] && (
          <p className="note">
            Runs in <Link to={`/roster/consists/${encodeURIComponent(t.consist)}`}>{consists[t.consist].name}</Link>, which is made
            up: break it up before changing the address.
          </p>
        )}
        <Decoders key={`d-${t.id}`} train={t} />
        <Details key={`e-${t.id}-${t.address}-${t.long}-${t.installed ?? ""}`} train={t} />
        <FunctionLabels key={`f-${t.id}-${t.installed ?? ""}`} train={t} />
        <Remove train={t} />
      </div>
    </Page>
  );
}

/** What the locomotive is: name, address and the roster's descriptive fields. */
function Details({ train: t }: { train: Train }) {
  const navigate = useNavigate();
  const [f, setF] = useState({
    name: t.name,
    address: String(t.address),
    long: t.long,
    road_name: t.road_name ?? "",
    road_number: t.road_number ?? "",
    manufacturer: t.manufacturer ?? "",
    model: t.model ?? "",
    owner: t.owner ?? "",
    comment: t.comment ?? "",
    length: t.length ? String(t.length) : "",
  });
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const n = Number(f.address);
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMessage(undefined);
    try {
      const saved = await api.updateTrain(t.id, { ...f, address: n, long: n > 127 || f.long, length: Number(f.length) || 0 });
      setMessage("Saved.");
      if (saved.id !== t.id) navigate(`/roster/${encodeURIComponent(saved.id)}`, { replace: true });
    } catch (err) {
      setMessage(`Could not save: ${describe(err)}`);
    } finally {
      setBusy(false);
    }
  }

  const fitted = t.decoders?.some((d) => d.id === t.installed);
  return (
    <form className="panel ros-details" onSubmit={(e) => void save(e)}>
      <h2>Locomotive</h2>
      <div className="ros-fields">
        <label className="auto-field auto-field--wide">
          <span>Name</span>
          <input className="input" value={f.name} onChange={set("name")} required />
        </label>
        <label className="auto-field">
          <span>DCC address</span>
          <input className="input input--short" type="number" min={1} max={10239} value={f.address} onChange={set("address")} required />
        </label>
        {n > 0 && n <= 127 && (
          <label className="check ros-fields__check">
            <input type="checkbox" checked={f.long} onChange={(e) => setF({ ...f, long: e.target.checked })} /> long address
          </label>
        )}
      </div>
      {fitted && <p className="thr-note">The address is the fitted decoder's. Writing a new address to the decoder changes it here too.</p>}
      <div className="ros-fields">
        <label className="auto-field">
          <span>Road name</span>
          <input className="input" value={f.road_name} onChange={set("road_name")} />
        </label>
        <label className="auto-field">
          <span>Road number</span>
          <input className="input input--short" value={f.road_number} onChange={set("road_number")} />
        </label>
        <label className="auto-field">
          <span>Manufacturer</span>
          <input className="input" value={f.manufacturer} onChange={set("manufacturer")} />
        </label>
        <label className="auto-field">
          <span>Model</span>
          <input className="input" value={f.model} onChange={set("model")} />
        </label>
        <label className="auto-field">
          <span>Owner</span>
          <input className="input" value={f.owner} onChange={set("owner")} />
        </label>
      </div>
      <div className="ros-fields">
        <label className="auto-field">
          <span>Length, mm</span>
          <input className="input input--short" type="number" min={0} step="any" value={f.length} onChange={set("length")} />
        </label>
      </div>
      <p className="thr-note">
        The locomotive's own length, measured over the couplers. A train adds up the lengths of its locomotives and cars, so train tracking knows where the rest of it is.
      </p>
      <label className="auto-field">
        <span>Notes</span>
        <textarea className="input ros-notes" rows={3} value={f.comment} onChange={set("comment")} />
      </label>
      <div className="ros-actions">
        <button className="btn btn--primary" type="submit" disabled={busy || !(n > 0)}>
          Save
        </button>
        {message && <span className="dim">{message}</span>}
      </div>
    </form>
  );
}

function decoderTitle(d: RosterDecoder): string {
  return d.name || d.model || d.id;
}

/** The locomotive's decoders: which is fitted, their settings, and adding one. */
function Decoders({ train: t }: { train: Train }) {
  const [adding, setAdding] = useState(false);
  const [message, setMessage] = useState<string>();
  const decoders = [...(t.decoders ?? [])].sort((a, b) => Number(b.id === t.installed) - Number(a.id === t.installed));

  async function add(d: PickedDefinition, identity?: DecoderIdentity) {
    setMessage(undefined);
    try {
      await api.addDecoder(t.id, { ...d, identity: identity ? rosterIdentity(identity) : undefined, install: !t.installed });
      setAdding(false);
    } catch (err) {
      setMessage(`Could not add the decoder: ${describe(err)}`);
    }
  }

  return (
    <div className="panel ros-decoders">
      <h2>Decoders</h2>
      {decoders.length === 0 && !adding && (
        <p className="dim">No decoder yet. Add the one fitted to open its settings by name and keep its CV values here.</p>
      )}
      {decoders.length > 0 && (
        <ul className="ros-decoders__list">
          {decoders.map((d) => (
            <DecoderRow key={d.id} train={t} decoder={d} onMessage={setMessage} />
          ))}
        </ul>
      )}
      {message && <p className="note note--error">{message}</p>}
      {adding ? (
        <>
          <DecoderPicker
            title="Add a decoder"
            intro="Put the locomotive on the programming track and identify its decoder, or search for the model."
            onPick={(d, id) => void add(d, id)}
          />
          <div>
            <button className="btn btn--quiet" onClick={() => setAdding(false)}>
              Cancel
            </button>
          </div>
        </>
      ) : (
        <div>
          <button className="btn" onClick={() => setAdding(true)}>
            Add a decoder
          </button>
        </div>
      )}
    </div>
  );
}

function DecoderRow({ train: t, decoder: d, onMessage }: { train: Train; decoder: RosterDecoder; onMessage: (m?: string) => void }) {
  const now = useNow();
  const fitted = d.id === t.installed;
  const [saved, setSaved] = useState<SavedCVs>();
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState(d.name ?? "");
  const [choosing, setChoosing] = useState(false);
  const [confirm, setConfirm] = useState(false);

  useEffect(() => {
    api
      .savedCVs(t.id, d.id)
      .then(setSaved)
      .catch(() => setSaved(undefined));
  }, [t.id, d.id]);

  async function act(what: string, fn: () => Promise<unknown>) {
    onMessage(undefined);
    try {
      await fn();
    } catch (err) {
      onMessage(`${what}: ${describe(err)}`);
    }
  }

  const imported = saved ? Object.keys(saved.imported ?? {}).length : 0;
  const count = (saved ? Object.keys(saved.cvs).length : 0) + imported;
  const id = d.identity;
  return (
    <li className="ros-decoder">
      <div className="ros-decoder__head">
        <Lamp color={fitted ? "green" : "off"} label={fitted ? "Fitted" : "Not fitted"} />
        {renaming ? (
          <form
            className="ros-decoder__rename"
            onSubmit={(e) => {
              e.preventDefault();
              void act("Rename", () => api.updateDecoder(t.id, d.id, { name })).then(() => setRenaming(false));
            }}
          >
            <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder={d.model} aria-label="Decoder name" autoFocus />
            <button className="btn btn--quiet" type="submit">
              Save
            </button>
          </form>
        ) : (
          <span className="cell-title">{decoderTitle(d)}</span>
        )}
        <span className="dim">{fitted ? "Fitted" : d.address ? `Not fitted; drives at ${addressText({ address: d.address, long: !!d.long })} when it is` : "Not fitted"}</span>
      </div>
      <div className="cell-sub">
        {d.file ? (d.model === d.family || !d.family ? d.model : `${d.model}, ${d.family}`) : <span className="ros-warn">No definition chosen</span>}
        {id && `; maker ${id.manufacturer}${id.product_id !== undefined ? `, product ${id.product_id}` : ""}`}
        {count > 0 && saved?.updated
          ? `; ${count.toLocaleString()} saved value${count === 1 ? "" : "s"}${
              imported === count ? ", imported and not read yet" : imported > 0 ? ` (${imported.toLocaleString()} imported, not read yet)` : ""
            }, ${relativeTime(saved.updated, now)}`
          : "; no values saved"}
      </div>
      <div className="ros-decoder__actions">
        {d.file && (
          <Link className="btn" to={rosterSettingsLink(t.id, d.id)}>
            Settings
          </Link>
        )}
        {!fitted && (
          <button className="btn btn--quiet" onClick={() => void act("Fit", () => api.installDecoder(t.id, d.id))}>
            Mark as fitted
          </button>
        )}
        <button className="btn btn--quiet" onClick={() => setRenaming((v) => !v)} aria-pressed={renaming}>
          Rename
        </button>
        <button className="btn btn--quiet" onClick={() => setChoosing((v) => !v)} aria-pressed={choosing}>
          {d.file ? "Change definition" : "Choose a definition"}
        </button>
        {confirm ? (
          <span className="ros-confirm">
            Remove {decoderTitle(d)} and its saved values?{" "}
            <button className="linklike" onClick={() => void act("Remove", () => api.removeDecoder(t.id, d.id))}>
              Yes, remove it
            </button>{" "}
            <button className="linklike" onClick={() => setConfirm(false)}>
              Keep it
            </button>
          </span>
        ) : (
          <button className="btn btn--quiet" onClick={() => setConfirm(true)}>
            Remove…
          </button>
        )}
      </div>
      {choosing && (
        <DecoderPicker
          title="Choose the definition"
          intro="Saved values stay; they are matched to the new definition by CV."
          onPick={(p, identity) =>
            void act("Change definition", () =>
              api.updateDecoder(t.id, d.id, { ...p, ...(identity ? { identity: rosterIdentity(identity) } : {}) }),
            ).then(() => setChoosing(false))
          }
        />
      )}
    </li>
  );
}

/** Function labels and which functions act only while held. */
function FunctionLabels({ train: t }: { train: Train }) {
  const [labels, setLabels] = useState<Record<string, string>>({ ...(t.functions ?? {}) });
  const [momentary, setMomentary] = useState<Set<number>>(new Set(t.momentary ?? []));
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMessage(undefined);
    const functions: Record<string, string> = {};
    for (const [k, v] of Object.entries(labels)) if (v.trim()) functions[k] = v.trim();
    try {
      await api.updateTrain(t.id, { functions, momentary: [...momentary].sort((a, b) => a - b) });
      setMessage("Saved.");
    } catch (err) {
      setMessage(`Could not save: ${describe(err)}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="panel ros-functions" onSubmit={(e) => void save(e)}>
      <h2>Function labels</h2>
      <p className="thr-note">
        {t.installed ? "These belong to the fitted decoder; another decoder brings its own when it is fitted. " : ""}
        Tick a function that acts only while its key is held, such as a horn.
      </p>
      <div className="thr-fn-edit">
        {Array.from({ length: FUNCTION_COUNT }, (_, i) => i).map((fn) => (
          <label key={fn}>
            <span className="thr-fn__num">F{fn}</span>
            <input
              className="input"
              value={labels[String(fn)] ?? ""}
              onChange={(e) => setLabels({ ...labels, [String(fn)]: e.target.value })}
              placeholder="label"
              aria-label={`F${fn} label`}
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
      <div className="ros-actions">
        <button className="btn btn--primary" type="submit" disabled={busy}>
          Save labels
        </button>
        {message && <span className="dim">{message}</span>}
      </div>
    </form>
  );
}

function Remove({ train: t }: { train: Train }) {
  const navigate = useNavigate();
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState<string>();
  async function remove() {
    try {
      await api.deleteTrain(t.id);
      navigate("/roster");
    } catch (err) {
      setError(describe(err));
    }
  }
  return (
    <p className="thr-note">
      {confirm ? (
        <>
          Remove {t.name} from the roster{t.decoders?.length ? `, with ${plural(t.decoders.length, "decoder")} and their saved values` : ""}?{" "}
          <button className="linklike" onClick={() => void remove()}>
            Yes, remove it
          </button>{" "}
          <button className="linklike" onClick={() => setConfirm(false)}>
            Keep it
          </button>
        </>
      ) : (
        <button className="linklike" onClick={() => setConfirm(true)}>
          Remove from the roster…
        </button>
      )}
      {error && <span className="field__error"> {error}</span>}
    </p>
  );
}
