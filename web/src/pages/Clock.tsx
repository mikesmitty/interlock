import { useEffect, useState } from "react";
import { api, type ClockState } from "../api";
import { RATES, fastDate, fastHMS, fastNow, rateLabel } from "../clock";
import { Status } from "../components/Lamp";
import { Page } from "../components/Page";
import { useNow } from "../format";
import { useBus } from "../store";

export function Clock() {
  const { clock } = useBus();
  const now = useNow(250);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [timeDraft, setTimeDraft] = useState("");
  const [dateDraft, setDateDraft] = useState("");
  const [rateDraft, setRateDraft] = useState("");

  // The forms start from the clock's own values once they are known.
  useEffect(() => {
    if (clock && timeDraft === "") setTimeDraft(clock.time);
    if (clock && dateDraft === "") setDateDraft(clock.date);
  }, [clock, timeDraft, dateDraft]);

  async function change(c: Parameters<typeof api.setClock>[0]) {
    setBusy(true);
    setError(undefined);
    try {
      await api.setClock(c);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  if (!clock) {
    return (
      <Page title="Clock" lead="The layout's fast clock, broadcast to every node that keeps time.">
        <p className="muted">Waiting for the clock.</p>
      </Page>
    );
  }
  return (
    <Page title="Clock" lead="The layout's fast clock, broadcast to every node that keeps time.">
      <div className="clk-layout">
        <div>
          <Face clock={clock} now={now} busy={busy} onChange={change} />
          {error && <p className="note note--error">{error}</p>}
          <div className="panel clk-set" style={{ marginTop: "1rem" }}>
            <h2>Rate</h2>
            <div className="clk-rates" role="group" aria-label="Clock rate">
              {RATES.map((r) => (
                <button key={r} type="button" className="btn btn--sm" aria-pressed={clock.rate === r} disabled={busy} onClick={() => void change({ rate: r })}>
                  {rateLabel(r)}
                </button>
              ))}
            </div>
            <form
              className="clk-set__row"
              style={{ marginTop: "0.75rem" }}
              onSubmit={(e) => {
                e.preventDefault();
                const r = Number(rateDraft);
                if (Number.isFinite(r)) void change({ rate: r });
              }}
            >
              <label className="clk-set__field">
                <span>Other rate</span>
                <input className="input input--num" type="number" step="0.25" min={-512} max={511.75} value={rateDraft} placeholder={String(clock.rate)} onChange={(e) => setRateDraft(e.target.value)} />
              </label>
              <button type="submit" className="btn" disabled={busy || rateDraft === ""}>
                Set rate
              </button>
            </form>
            <p className="clk-note muted">A fast minute passes every {clock.rate > 0 ? `${Math.round((60 / clock.rate) * 10) / 10} seconds` : "never, at this rate"}. Negative rates run the clock backward.</p>
          </div>
        </div>
        <div>
          <div className="panel clk-set">
            <h2>Set the clock</h2>
            <form
              className="clk-set__row"
              onSubmit={(e) => {
                e.preventDefault();
                void change({ time: timeDraft });
              }}
            >
              <label className="clk-set__field">
                <span>Time</span>
                <input className="input input--short" type="time" value={timeDraft} onChange={(e) => setTimeDraft(e.target.value)} required />
              </label>
              <button type="submit" className="btn" disabled={busy}>
                Set time
              </button>
            </form>
            <form
              className="clk-set__row"
              onSubmit={(e) => {
                e.preventDefault();
                void change({ date: dateDraft });
              }}
            >
              <label className="clk-set__field">
                <span>Date</span>
                <input className="input input--short" type="date" value={dateDraft} min="0001-01-01" max="4095-12-31" onChange={(e) => setDateDraft(e.target.value)} required />
              </label>
              <button type="submit" className="btn" disabled={busy}>
                Set date
              </button>
            </form>
            <p className="clk-note muted">Setting the time or date tells every clock on the bus. Throttles and other nodes can set this clock the same way, and their changes show here.</p>
          </div>
          <div className="panel clk-set" style={{ marginTop: "1rem" }}>
            <h2>What uses it</h2>
            <p className="clk-note muted" style={{ marginTop: 0 }}>
              Interlock produces the default fast clock on the bus, so station clocks and throttles that follow the Broadcast Time protocol show this time. Automations can start when the fast
              clock reads a time and check that it is within a window. The clock keeps its time between restarts.
            </p>
          </div>
        </div>
      </div>
    </Page>
  );
}

function Face({ clock, now, busy, onChange }: { clock: ClockState; now: number; busy: boolean; onChange: (c: { running: boolean }) => Promise<void> }) {
  const fast = fastNow(clock, now);
  const hms = fastHMS(fast);
  return (
    <div className="panel clk-face">
      <div className="clk-face__time" aria-live="off">
        {hms.slice(0, 5)}
        <small>{hms.slice(5)}</small>
      </div>
      <div className="clk-face__date">{fastDate(fast)}</div>
      <div className="clk-face__state">
        <Status color={clock.running ? "green" : "amber"}>{clock.running ? `Running at ${rateLabel(clock.rate)}` : "Stopped"}</Status>
        {clock.running ? (
          <button type="button" className="btn" disabled={busy} onClick={() => void onChange({ running: false })}>
            Stop the clock
          </button>
        ) : (
          <button type="button" className="btn btn--primary" disabled={busy} onClick={() => void onChange({ running: true })}>
            Start the clock
          </button>
        )}
      </div>
    </div>
  );
}
