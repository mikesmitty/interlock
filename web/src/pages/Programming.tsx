import { useEffect, useState, type FormEvent } from "react";
import { addressID, api, type CVOp, type ProgrammingTrack } from "../api";
import { DecoderPicker } from "../components/decoder/DecoderPicker";
import { Status } from "../components/Lamp";
import { Page } from "../components/Page";
import { clockTime } from "../format";
import { useBus } from "../store";

const COMMON_CVS: [number, string][] = [
  [1, "Primary address"],
  [2, "Start voltage"],
  [3, "Acceleration"],
  [4, "Deceleration"],
  [5, "Top voltage"],
  [7, "Decoder version"],
  [8, "Manufacturer"],
  [17, "Long address, high byte"],
  [18, "Long address, low byte"],
  [19, "Consist address"],
  [29, "Configuration"],
];

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function Programming() {
  const { trains } = useBus();
  const [track, setTrack] = useState<ProgrammingTrack>();
  const [history, setHistory] = useState<CVOp[]>([]);
  const [message, setMessage] = useState<string>();
  const [finding, setFinding] = useState(false);

  useEffect(() => {
    api
      .programming()
      .then((r) => {
        setTrack(r.track);
        setHistory(r.history);
      })
      .catch((err) => setMessage(describe(err)));
  }, []);

  function record(op: CVOp) {
    setHistory((h) => [op, ...h].slice(0, 50));
  }

  async function find() {
    setFinding(true);
    setMessage(undefined);
    try {
      setTrack(await api.findProgrammingTrack());
    } catch (err) {
      setTrack({ online: false });
      setMessage(describe(err));
    } finally {
      setFinding(false);
    }
  }

  const roster = Object.values(trains)
    .filter((t) => t.roster)
    .sort((a, b) => a.name.localeCompare(b.name));
  const trackLead = !track
    ? "Looking for the programming track…"
    : track.node
      ? `Programming track on ${track.name ?? track.node}${track.online ? "" : " (offline)"}.`
      : "No programming track found yet; the first read or write looks for one.";

  return (
    <Page
      title="Programming"
      lead={trackLead}
      actions={
        <button className="btn" onClick={() => void find()} disabled={finding}>
          {finding ? "Looking…" : "Look for the programming track"}
        </button>
      }
    >
      {message && <p className="note note--error">{message}</p>}
      <div className="prog-layout">
        <DecoderPicker />
        <ServiceMode track={track} onOp={record} onTrack={setTrack} />
        <OnTheMain roster={roster} onOp={record} />
        <div className="panel prog-history">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Time</th>
                <th scope="col">What</th>
                <th scope="col">CV</th>
                <th scope="col">Value</th>
                <th scope="col">Result</th>
              </tr>
            </thead>
            <tbody>
              {history.length === 0 && (
                <tr>
                  <td colSpan={5} className="dim">
                    Nothing read or written yet.
                  </td>
                </tr>
              )}
              {history.map((op) => (
                <tr key={op.id}>
                  <td className="dim">{clockTime(op.finished)}</td>
                  <td>
                    {op.kind === "read"
                      ? "Read"
                      : op.kind === "write"
                        ? "Write"
                        : `Write on the main to ${op.train_name || op.train}`}
                  </td>
                  <td className="num">{op.cv}</td>
                  <td className="num">{op.ok ? op.value : op.kind === "read" ? "" : op.value}</td>
                  <td>
                    {op.ok ? <Status color="green">Done</Status> : <Status color="red">{op.error ?? "Failed"}</Status>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </Page>
  );
}

/** A CV number box with the common ones to pick from. */
function CVField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <>
      <label className="auto-field">
        <span>CV</span>
        <input
          className="input input--short"
          type="number"
          min={1}
          max={1024}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          required
        />
      </label>
      <label className="auto-field">
        <span>Common</span>
        <select
          className="input input--select"
          value={COMMON_CVS.some(([n]) => String(n) === value) ? value : ""}
          onChange={(e) => e.target.value && onChange(e.target.value)}
        >
          <option value="">Pick one…</option>
          {COMMON_CVS.map(([n, label]) => (
            <option key={n} value={n}>
              {n} {label}
            </option>
          ))}
        </select>
      </label>
    </>
  );
}

/** A value box with its bits, which can be toggled. */
function ValueField({ value, onChange }: { value: number | undefined; onChange: (v: number) => void }) {
  const v = value ?? 0;
  return (
    <div className="prog-result">
      <label className="auto-field">
        <span>Value</span>
        <input
          className="input input--short"
          type="number"
          min={0}
          max={255}
          value={value ?? ""}
          onChange={(e) => onChange(Math.max(0, Math.min(255, Number(e.target.value))))}
        />
      </label>
      <span className="prog-result__alt">
        0x{v.toString(16).toUpperCase().padStart(2, "0")} · {v.toString(2).padStart(8, "0")}
      </span>
      <span className="prog-bits" role="group" aria-label="Bits, 7 to 0">
        {[7, 6, 5, 4, 3, 2, 1, 0].map((bit) => (
          <button
            key={bit}
            type="button"
            className="prog-bit"
            aria-pressed={(v & (1 << bit)) !== 0}
            onClick={() => onChange(v ^ (1 << bit))}
            title={`bit ${bit}`}
          >
            {bit}
          </button>
        ))}
      </span>
    </div>
  );
}

function ServiceMode({
  track,
  onOp,
  onTrack,
}: {
  track?: ProgrammingTrack;
  onOp: (op: CVOp) => void;
  onTrack: (t: ProgrammingTrack) => void;
}) {
  const [cv, setCV] = useState("8");
  const [value, setValue] = useState<number>();
  const [busy, setBusy] = useState<string>();
  const [result, setResult] = useState<CVOp>();

  async function run(kind: "read" | "write", e?: FormEvent) {
    e?.preventDefault();
    setBusy(kind);
    setResult(undefined);
    try {
      const op = kind === "read" ? await api.readCV(Number(cv)) : await api.writeCV(Number(cv), value ?? 0);
      setResult(op);
      onOp(op);
      if (op.ok && kind === "read") setValue(op.value);
      if (op.ok || !track?.node) {
        const r = await api.programming();
        onTrack(r.track);
      }
    } catch (err) {
      setResult({
        id: 0,
        kind,
        cv: Number(cv),
        value: value ?? 0,
        ok: false,
        error: describe(err),
        started: "",
        finished: "",
      });
    } finally {
      setBusy(undefined);
    }
  }

  return (
    <form className="panel prog-panel" onSubmit={(e) => void run("read", e)}>
      <h2>Programming track</h2>
      <p className="thr-note" style={{ marginBottom: "0.75rem" }}>
        Service mode talks to whatever decoder is on the programming track. A read takes a few seconds.
      </p>
      <div className="prog-form">
        <CVField value={cv} onChange={setCV} />
        <button className="btn btn--primary" type="submit" disabled={!!busy || !cv}>
          {busy === "read" ? "Reading…" : "Read"}
        </button>
        <button
          className="btn"
          type="button"
          onClick={() => void run("write")}
          disabled={!!busy || !cv || value === undefined}
        >
          {busy === "write" ? "Writing…" : "Write"}
        </button>
      </div>
      <ValueField value={value} onChange={setValue} />
      {result && (
        <p className={result.ok ? "note" : "note note--error"}>
          {result.ok
            ? result.kind === "read"
              ? `CV ${result.cv} is ${result.value}.`
              : `CV ${result.cv} is now ${result.value}.`
            : result.error}
        </p>
      )}
    </form>
  );
}

function OnTheMain({
  roster,
  onOp,
}: {
  roster: { id: string; name: string; address: number; long: boolean }[];
  onOp: (op: CVOp) => void;
}) {
  // Locomotives are named by address here: that is what a write on the main reaches.
  const [train, setTrain] = useState("");
  const [cv, setCV] = useState("3");
  const [value, setValue] = useState<number>();
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<CVOp>();
  const id = train || (roster[0] ? addressID(roster[0]) : "");

  async function write(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setResult(undefined);
    try {
      const op = await api.writePOM(id, Number(cv), value ?? 0);
      setResult(op);
      onOp(op);
    } catch (err) {
      setResult({
        id: 0,
        kind: "pom",
        cv: Number(cv),
        value: value ?? 0,
        ok: false,
        error: describe(err),
        started: "",
        finished: "",
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="panel prog-panel" onSubmit={(e) => void write(e)}>
      <h2>On the main</h2>
      <p className="thr-note" style={{ marginBottom: "0.75rem" }}>
        Writes a CV to one locomotive wherever it is on the layout. Reading on the main needs RailCom, which is not here
        yet.
      </p>
      <div className="prog-form">
        <label className="auto-field">
          <span>Locomotive</span>
          <input
            className="input input--short"
            list="pom-trains"
            value={train || id}
            onChange={(e) => setTrain(e.target.value)}
            placeholder="address"
            required
          />
          <datalist id="pom-trains">
            {roster.map((t) => (
              <option key={t.id} value={addressID(t)}>
                {t.name}
              </option>
            ))}
          </datalist>
        </label>
        <CVField value={cv} onChange={setCV} />
        <button className="btn btn--primary" type="submit" disabled={busy || !cv || !id || value === undefined}>
          {busy ? "Writing…" : "Write"}
        </button>
      </div>
      <ValueField value={value} onChange={setValue} />
      {result && (
        <p className={result.ok ? "note" : "note note--error"}>
          {result.ok ? `CV ${result.cv} of ${result.train_name || result.train} is now ${result.value}.` : result.error}
        </p>
      )}
    </form>
  );
}
