import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, type Car } from "../api";
import { Empty, Page } from "../components/Page";
import { formationOf, lengthText } from "../makeup";
import { useBus } from "../store";
import { roadText } from "./Roster";

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function CarPage() {
  const { id = "" } = useParams();
  const { cars, formations, loaded } = useBus();
  const c = cars[id];
  const back = (
    <Link className="page__back" to="/roster">
      Roster
    </Link>
  );
  if (!loaded) {
    return (
      <Page title="Car" back={back} lead="Loading…">
        {null}
      </Page>
    );
  }
  if (!c) {
    return (
      <Page title="Car" back={back}>
        <Empty title="No such car">
          <p>It may have been removed. Cars are listed on the Roster page.</p>
        </Empty>
      </Page>
    );
  }
  const holder = formationOf({ car: c.id }, formations);
  const lead = [roadText(c), [c.manufacturer, c.model].filter(Boolean).join(" "), c.length ? lengthText(c.length) : "length not given"].filter(Boolean).join(", ");
  return (
    <Page title={c.name} back={back} lead={`${lead}.`}>
      <div className="ros-layout">
        {holder && (
          <p className="note">
            In <Link to={`/roster/trains/${encodeURIComponent(holder.id)}`}>{holder.name}</Link>.
          </p>
        )}
        <CarDetails key={JSON.stringify(c)} car={c} />
        <RemoveCar car={c} holder={holder?.name} />
      </div>
    </Page>
  );
}

/** What the car is, and its length. */
function CarDetails({ car: c }: { car: Car }) {
  const [f, setF] = useState({
    name: c.name,
    road_name: c.road_name ?? "",
    road_number: c.road_number ?? "",
    manufacturer: c.manufacturer ?? "",
    model: c.model ?? "",
    comment: c.comment ?? "",
    length: c.length ? String(c.length) : "",
  });
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMessage(undefined);
    try {
      await api.updateCar(c.id, { ...f, length: Number(f.length) || 0 });
      setMessage("Saved.");
    } catch (err) {
      setMessage(`Could not save: ${describe(err)}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="panel ros-details" onSubmit={(e) => void save(e)}>
      <h2>Car</h2>
      <div className="ros-fields">
        <label className="auto-field auto-field--wide">
          <span>Name</span>
          <input className="input" value={f.name} onChange={set("name")} required />
        </label>
        <label className="auto-field">
          <span>Length, mm</span>
          <input className="input input--short" type="number" min={0} step="any" value={f.length} onChange={set("length")} />
        </label>
      </div>
      <p className="thr-note">Measured over the couplers. A train adds up the lengths of its locomotives and cars, so train tracking knows where the rest of it is.</p>
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
      </div>
      <label className="auto-field">
        <span>Notes</span>
        <textarea className="input ros-notes" rows={3} value={f.comment} onChange={set("comment")} />
      </label>
      <div className="ros-actions">
        <button className="btn btn--primary" type="submit" disabled={busy || !f.name.trim()}>
          Save
        </button>
        {message && <span className="dim">{message}</span>}
      </div>
    </form>
  );
}

function RemoveCar({ car: c, holder }: { car: Car; holder?: string }) {
  const navigate = useNavigate();
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState<string>();
  async function remove() {
    try {
      await api.deleteCar(c.id);
      navigate("/roster");
    } catch (err) {
      setError(describe(err));
    }
  }
  return (
    <p className="thr-note">
      {confirm ? (
        <>
          Remove {c.name} from the roster{holder ? `? It comes out of ${holder} too.` : "?"}{" "}
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
