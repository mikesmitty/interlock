import { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router";
import { api, type Trace, type TraceStep } from "../api";
import { automationLamp, namesFrom, resultLamp, stepLamp, summarize } from "../automation";
import { Lamp } from "../components/Lamp";
import { Page } from "../components/Page";
import { clockTime, relativeTime, useNow } from "../format";
import { useBus } from "../store";

export function AutomationTraces() {
  const { id = "" } = useParams();
  const { automations, entities, nodes, liveTraces, loaded } = useBus();
  const now = useNow();
  const auto = automations[id];
  const [fetched, setFetched] = useState<Trace[]>();
  const [selected, setSelected] = useState<string>();
  const [follow, setFollow] = useState(true);
  const [message, setMessage] = useState<string>();
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setFetched(undefined);
    api
      .traces(id)
      .then(setFetched)
      .catch((err: unknown) => {
        setFetched([]);
        setMessage(err instanceof Error ? err.message : String(err));
      });
  }, [id]);

  const traces = useMemo(() => {
    const byId = new Map<string, Trace>();
    for (const t of fetched ?? []) byId.set(t.id, t);
    for (const t of liveTraces) if (t.automation === id) byId.set(t.id, t);
    return [...byId.values()].sort((a, b) => b.started.localeCompare(a.started));
  }, [fetched, liveTraces, id]);

  const current = (follow ? traces[0] : traces.find((t) => t.id === selected)) ?? traces[0];
  const names = namesFrom(entities, nodes);

  async function run() {
    setBusy(true);
    setMessage(undefined);
    try {
      await api.runAutomation(id);
      setFollow(true);
    } catch (err) {
      setMessage(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  if (loaded && !auto) {
    return (
      <Page title="Not found" back={<Back />}>
        <p className="muted">There is no automation called {id}.</p>
      </Page>
    );
  }

  const lamp = auto ? automationLamp(auto) : undefined;
  return (
    <Page
      title={auto?.name ?? "Automation"}
      lead={
        auto && lamp ? (
          <>
            <Lamp color={lamp.color} pulse={lamp.pulse} label={lamp.word} /> {lamp.word}. {auto.description || summarize(auto, names)}
          </>
        ) : undefined
      }
      back={<Back />}
      actions={
        auto && (
          <>
            {auto.running > 0 ? (
              <button className="btn" onClick={() => void api.stopAutomation(id)}>
                Stop
              </button>
            ) : (
              <button className="btn" onClick={() => void run()} disabled={busy}>
                Run now
              </button>
            )}
            <Link className="btn btn--quiet" to={`/automations/${encodeURIComponent(id)}`}>
              Edit
            </Link>
          </>
        )
      }
    >
      {message && <p className="note note--error">{message}</p>}
      {auto?.problems?.map((p) => (
        <p key={p} className="note">
          {p}
        </p>
      ))}
      {fetched && traces.length === 0 && (
        <p className="muted">
          It has not run yet. Running it now skips the conditions and does the actions, which is the quickest way to try them out.
        </p>
      )}
      {traces.length > 0 && (
        <div className="trace-layout">
          <div className="panel trace-list">
            <table className="table">
              <tbody>
                {traces.map((t, i) => {
                  const r = resultLamp(t.result);
                  return (
                    <tr
                      key={t.id}
                      className={t.id === current?.id ? "is-selected is-link" : "is-link"}
                      tabIndex={0}
                      onClick={() => {
                        setSelected(t.id);
                        setFollow(i === 0);
                      }}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          setSelected(t.id);
                          setFollow(i === 0);
                        }
                      }}
                    >
                      <td className="trace-list__lamp">
                        <Lamp color={r.color} pulse={r.pulse} label={r.word} />
                      </td>
                      <td>
                        <div className="cell-title">{t.trigger.description}</div>
                        <div className="cell-sub">
                          {relativeTime(t.started, now)} · {r.word}
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          {current && <TraceDetail trace={current} />}
        </div>
      )}
    </Page>
  );
}

function depthOf(path: string): number {
  return path.split(".").length - 1;
}

function offset(from: string, to: string): string {
  const ms = new Date(to).getTime() - new Date(from).getTime();
  if (!Number.isFinite(ms)) return "";
  return `+${(ms / 1000).toFixed(2)} s`;
}

function took(step: TraceStep): string {
  if (!step.finished) return "";
  const ms = new Date(step.finished).getTime() - new Date(step.started).getTime();
  if (ms < 50) return "";
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`;
}

function TraceDetail({ trace }: { trace: Trace }) {
  const r = resultLamp(trace.result);
  const ended = trace.finished ? offset(trace.started, trace.finished) : undefined;
  return (
    <div className="panel trace-detail">
      <div className="trace-detail__head">
        <div>
          <span className="status">
            <Lamp color={r.color} pulse={r.pulse} label={r.word} size="lg" />
            <strong>{r.word}</strong>
          </span>
          {trace.error && <span className="trace-detail__error"> {trace.error}</span>}
        </div>
        <div className="cell-sub">
          Started {clockTime(trace.started)}
          {ended ? `, ended ${ended.replace("+", "after ")}` : ""}
        </div>
      </div>
      <table className="table trace-steps">
        <tbody>
          <tr className="trace-step trace-step--trigger">
            <td className="trace-step__time">+0.00 s</td>
            <td className="trace-step__lamp">
              <Lamp color="green" label="trigger" />
            </td>
            <td>
              <div className="trace-step__what">{trace.trigger.description}</div>
              <div className="cell-sub">
                {trace.trigger.type === "manual" ? "Run by hand; the conditions were skipped" : `Trigger: ${trace.trigger.type}${trace.trigger.id ? ` “${trace.trigger.id}”` : ""}`}
              </div>
            </td>
          </tr>
          {trace.steps.map((s, i) => {
            const lamp = stepLamp(s.result);
            const kind = s.type.startsWith("condition:") ? "condition" : s.type;
            return (
              <tr key={`${s.path}-${i}`} className={`trace-step trace-step--${lamp.color}`}>
                <td className="trace-step__time">{offset(trace.started, s.started)}</td>
                <td className="trace-step__lamp">
                  <Lamp color={lamp.color} pulse={lamp.pulse} label={lamp.word} />
                </td>
                <td style={{ paddingLeft: `${0.75 + depthOf(s.path) * 1.25}rem` }}>
                  <div className="trace-step__what">
                    <span className="trace-step__kind">{kind}</span> {s.description}
                  </div>
                  <div className="cell-sub">
                    {[lamp.word, s.detail, took(s)].filter(Boolean).join(" · ")}
                    {s.error && <span className="trace-step__err"> {s.error}</span>}
                  </div>
                </td>
              </tr>
            );
          })}
          {trace.result === "running" && trace.steps.every((s) => s.result !== "running") && (
            <tr className="trace-step">
              <td className="trace-step__time"></td>
              <td className="trace-step__lamp">
                <Lamp color="amber" pulse label="running" />
              </td>
              <td className="muted">Starting…</td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

function Back() {
  return (
    <Link className="page__back" to="/automations">
      ← Automations
    </Link>
  );
}
