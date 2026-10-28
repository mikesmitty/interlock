import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, type Car, type Formation, type Train, type Vehicle } from "../api";
import { Empty, Page } from "../components/Page";
import { plural } from "../format";
import { formationOf, lengthText, makeupLength, makeupText, vehicleInfo } from "../makeup";
import { useBus } from "../store";

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** A train made up of locomotives and cars: what it is made of, in order from the front. */
export function FormationPage() {
  const { id = "" } = useParams();
  const { formations, trains, cars, loaded } = useBus();
  const f = formations[id];
  const back = (
    <Link className="page__back" to="/roster">
      Roster
    </Link>
  );
  if (!loaded) {
    return (
      <Page title="Train" back={back} lead="Loading…">
        {null}
      </Page>
    );
  }
  if (!f) {
    return (
      <Page title="Train" back={back}>
        <Empty title="No such train">
          <p>It may have been removed. Trains are listed on the Roster page.</p>
        </Empty>
      </Page>
    );
  }
  const lead = f.vehicles.length
    ? `${makeupText(f, trains)}, ${lengthText(f.length)} long${f.unmeasured ? ` (${plural(f.unmeasured, "length")} not given yet)` : ""}.`
    : "Nothing in it yet.";
  return (
    <Page title={f.name} back={back} lead={lead}>
      <div className="ros-layout">
        <Vehicles key={JSON.stringify(f.vehicles)} formation={f} trains={trains} cars={cars} formations={formations} />
        <About key={`${f.name}\n${f.comment ?? ""}`} formation={f} />
        <RemoveFormation formation={f} />
      </div>
    </Page>
  );
}

/** The locomotives and cars in order from the front, each facing either way. */
function Vehicles({
  formation: f,
  trains,
  cars,
  formations,
}: {
  formation: Formation;
  trains: Record<string, Train>;
  cars: Record<string, Car>;
  formations: Record<string, Formation>;
}) {
  const [rows, setRows] = useState<Vehicle[]>(f.vehicles);
  const [adding, setAdding] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const dirty = JSON.stringify(rows) !== JSON.stringify(f.vehicles);
  const here = new Set(rows.map((v) => (v.locomotive ? `l:${v.locomotive}` : `c:${v.car}`)));
  // Only what no other train holds: a locomotive or car is in one train at most.
  const freeLocos = Object.values(trains)
    .filter((t) => t.roster && !here.has(`l:${t.id}`) && !formationOf({ locomotive: t.id }, formations, f.id))
    .sort((a, b) => a.name.localeCompare(b.name));
  const freeCars = Object.values(cars)
    .filter((c) => !here.has(`c:${c.id}`) && !formationOf({ car: c.id }, formations, f.id))
    .sort((a, b) => a.name.localeCompare(b.name));
  const total = makeupLength(rows, trains, cars);

  const change = (i: number, patch: Partial<Vehicle>) => setRows(rows.map((v, j) => (j === i ? { ...v, ...patch } : v)));
  function move(i: number, by: number) {
    const next = [...rows];
    const [v] = next.splice(i, 1);
    next.splice(i + by, 0, v);
    setRows(next);
  }
  function add() {
    const [kind, vid] = [adding.slice(0, 1), adding.slice(2)];
    setRows([...rows, kind === "l" ? { locomotive: vid, reverse: false } : { car: vid, reverse: false }]);
    setAdding("");
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMessage(undefined);
    try {
      await api.updateFormation(f.id, { vehicles: rows });
    } catch (err) {
      setMessage(`Could not save: ${describe(err)}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="panel ros-decoders" onSubmit={(e) => void save(e)}>
      <h2>Made up of</h2>
      {rows.length === 0 ? (
        <p className="dim">Add its locomotives and cars, the front first: the front is the end that leads when the train goes forward.</p>
      ) : (
        <table className="table">
          <thead>
            <tr>
              <th scope="col" className="num">
                Place
              </th>
              <th scope="col">Locomotive or car</th>
              <th scope="col" className="num">
                Length
              </th>
              <th scope="col">Faces</th>
              <th scope="col" aria-label="Actions" />
            </tr>
          </thead>
          <tbody>
            {rows.map((v, i) => {
              const info = vehicleInfo(v, trains, cars);
              return (
                <tr key={`${info.kind}:${info.id}`}>
                  <td className="num dim">{i === 0 ? "Front" : i === rows.length - 1 ? "Rear" : i + 1}</td>
                  <td>
                    <Link to={info.href}>{info.name}</Link>
                    {info.kind === "locomotive" && <span className="faint"> locomotive</span>}
                  </td>
                  <td className="num">{info.length ? lengthText(info.length) : <span className="faint">not given</span>}</td>
                  <td>
                    <select
                      className="input input--select"
                      value={v.reverse ? "backward" : "forward"}
                      onChange={(e) => change(i, { reverse: e.target.value === "backward" })}
                      aria-label={`Which way ${info.name} faces`}
                    >
                      <option value="forward">Forward</option>
                      <option value="backward">Backward</option>
                    </select>
                  </td>
                  <td>
                    <span className="ros-actions">
                      <button type="button" className="linklike" disabled={i === 0} onClick={() => move(i, -1)} aria-label={`Move ${info.name} toward the front`}>
                        Up
                      </button>
                      <button
                        type="button"
                        className="linklike"
                        disabled={i === rows.length - 1}
                        onClick={() => move(i, 1)}
                        aria-label={`Move ${info.name} toward the rear`}
                      >
                        Down
                      </button>
                      <button type="button" className="linklike" onClick={() => setRows(rows.filter((_, j) => j !== i))}>
                        Remove
                      </button>
                    </span>
                  </td>
                </tr>
              );
            })}
          </tbody>
          <tfoot>
            <tr>
              <td />
              <td className="dim">Train</td>
              <td className="num">{lengthText(total.length)}</td>
              <td colSpan={2} className="faint">
                {total.unmeasured > 0 && `${plural(total.unmeasured, "length")} not given`}
              </td>
            </tr>
          </tfoot>
        </table>
      )}
      <div className="ros-actions">
        <select className="input input--select" value={adding} onChange={(e) => setAdding(e.target.value)} aria-label="Locomotive or car to add">
          <option value="">Add at the rear…</option>
          {freeLocos.length > 0 && (
            <optgroup label="Locomotives">
              {freeLocos.map((t) => (
                <option key={t.id} value={`l:${t.id}`}>
                  {t.name}
                </option>
              ))}
            </optgroup>
          )}
          {freeCars.length > 0 && (
            <optgroup label="Cars">
              {freeCars.map((c) => (
                <option key={c.id} value={`c:${c.id}`}>
                  {c.name}
                </option>
              ))}
            </optgroup>
          )}
        </select>
        <button type="button" className="btn" disabled={!adding} onClick={add}>
          Add
        </button>
      </div>
      <p className="thr-note">
        Locomotives and cars in another train aren't offered; take them out of it first. A locomotive facing backward runs the train backward
        when its throttle goes forward.
      </p>
      <div className="ros-actions">
        <button className="btn btn--primary" type="submit" disabled={busy || !dirty}>
          Save
        </button>
        {dirty && (
          <button type="button" className="btn btn--quiet" onClick={() => setRows(f.vehicles)}>
            Discard
          </button>
        )}
        {message && <span className="field__error">{message}</span>}
      </div>
    </form>
  );
}

/** The train's name and notes. */
function About({ formation: f }: { formation: Formation }) {
  const [name, setName] = useState(f.name);
  const [comment, setComment] = useState(f.comment ?? "");
  const [message, setMessage] = useState<string>();
  async function save(e: FormEvent) {
    e.preventDefault();
    setMessage(undefined);
    try {
      await api.updateFormation(f.id, { name, comment });
      setMessage("Saved.");
    } catch (err) {
      setMessage(`Could not save: ${describe(err)}`);
    }
  }
  return (
    <form className="panel ros-details" onSubmit={(e) => void save(e)}>
      <h2>Train</h2>
      <label className="auto-field">
        <span>Name</span>
        <input className="input" value={name} onChange={(e) => setName(e.target.value)} required />
      </label>
      <label className="auto-field">
        <span>Notes</span>
        <textarea className="input ros-notes" rows={3} value={comment} onChange={(e) => setComment(e.target.value)} />
      </label>
      <div className="ros-actions">
        <button className="btn btn--primary" type="submit" disabled={!name.trim() || (name === f.name && comment === (f.comment ?? ""))}>
          Save
        </button>
        {message && <span className="dim">{message}</span>}
      </div>
    </form>
  );
}

function RemoveFormation({ formation: f }: { formation: Formation }) {
  const navigate = useNavigate();
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState<string>();
  async function remove() {
    try {
      await api.deleteFormation(f.id);
      navigate("/roster");
    } catch (err) {
      setError(describe(err));
    }
  }
  return (
    <p className="thr-note">
      {confirm ? (
        <>
          Remove the train {f.name}? Its locomotives and cars stay in the roster.{" "}
          <button className="linklike" onClick={() => void remove()}>
            Yes, remove it
          </button>{" "}
          <button className="linklike" onClick={() => setConfirm(false)}>
            Keep it
          </button>
        </>
      ) : (
        <button className="linklike" onClick={() => setConfirm(true)}>
          Remove this train…
        </button>
      )}
      {error && <span className="field__error"> {error}</span>}
    </p>
  );
}
