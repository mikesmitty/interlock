import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { api, type AutomationStatus } from "../api";
import { automationLamp, namesFrom, resultLamp, summarize } from "../automation";
import { Lamp } from "../components/Lamp";
import { Empty, Page } from "../components/Page";
import { plural, relativeTime, useNow } from "../format";
import { useBus } from "../store";

export function Automations() {
  const { automations, entities, nodes, trains, loaded } = useBus();
  const now = useNow();
  const navigate = useNavigate();
  const [message, setMessage] = useState<string>();
  const [busy, setBusy] = useState<string>();
  const names = namesFrom(entities, nodes, trains);

  const list = Object.values(automations).sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
  const on = list.filter((a) => a.enabled).length;
  const problems = list.filter((a) => a.problems?.length).length;

  async function act(a: AutomationStatus, label: string, fn: () => Promise<unknown>) {
    setBusy(a.id);
    setMessage(undefined);
    try {
      await fn();
    } catch (err) {
      setMessage(`${label} ${a.name}: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setBusy(undefined);
    }
  }

  const lead = !loaded
    ? "Loading…"
    : list.length === 0
      ? "Nothing happens on its own yet."
      : `${plural(list.length, "automation")}, ${on} on${problems ? `, ${problems} needing attention` : ""}.`;

  return (
    <Page
      title="Automations"
      lead={lead}
      actions={
        <Link className="btn btn--primary" to="/automations/new">
          New automation
        </Link>
      }
    >
      {message && <p className="note note--error">{message}</p>}
      {loaded && list.length === 0 && (
        <Empty title="No automations yet">
          <p>
            An automation watches for something, checks a few things, and then acts: when Track 2 becomes occupied and Turnout 1 is thrown, close Turnout 1 after three seconds.
          </p>
          <p>
            It is built from the <Link to="/entities">entities</Link> on the layout, so the layout needs a node or two first. Every run is recorded as a trace you can read
            afterwards.
          </p>
        </Empty>
      )}
      {list.length > 0 && (
        <div className="panel">
          <table className="table auto-table">
            <thead>
              <tr>
                <th scope="col">State</th>
                <th scope="col">Automation</th>
                <th scope="col">Last run</th>
                <th scope="col">
                  <span className="sr-only">Controls</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.map((a) => {
                const lamp = automationLamp(a);
                const last = a.last_result ? resultLamp(a.last_result) : undefined;
                return (
                  <tr key={a.id} className="auto-item">
                    <td className="auto-state">
                      <Lamp color={lamp.color} pulse={lamp.pulse} label={lamp.word} />
                      <span>{lamp.word}</span>
                    </td>
                    <td className="auto-name">
                      <div className="cell-title">
                        <Link to={`/automations/${encodeURIComponent(a.id)}`}>{a.name}</Link>
                      </div>
                      <div className="cell-sub auto-summary">{a.description || summarize(a, names)}</div>
                      {a.problems?.map((p) => (
                        <div key={p} className="auto-problem">
                          {p}
                        </div>
                      ))}
                    </td>
                    <td className="auto-last">
                      {a.last_triggered && last ? (
                        <Link to={`/automations/${encodeURIComponent(a.id)}/traces`} className="auto-last__link">
                          <span>{relativeTime(a.last_triggered, now)}</span>
                          <span className="cell-sub">{last.word}</span>
                        </Link>
                      ) : (
                        <span className="faint">never</span>
                      )}
                    </td>
                    <td className="auto-controls">
                      <span className="lever" role="group" aria-label={`${a.name} on or off`}>
                        <button
                          type="button"
                          className="lever__opt"
                          aria-pressed={!a.enabled}
                          disabled={busy === a.id}
                          onClick={() => void act(a, "Turning off", () => api.setAutomationEnabled(a.id, false))}
                        >
                          Off
                        </button>
                        <button
                          type="button"
                          className="lever__opt"
                          aria-pressed={a.enabled}
                          disabled={busy === a.id}
                          onClick={() => void act(a, "Turning on", () => api.setAutomationEnabled(a.id, true))}
                        >
                          On
                        </button>
                      </span>
                      {a.running > 0 ? (
                        <button className="btn btn--sm" disabled={busy === a.id} onClick={() => void act(a, "Stopping", () => api.stopAutomation(a.id))}>
                          Stop
                        </button>
                      ) : (
                        <button
                          className="btn btn--sm"
                          disabled={busy === a.id}
                          onClick={() =>
                            void act(a, "Running", async () => {
                              await api.runAutomation(a.id);
                              navigate(`/automations/${encodeURIComponent(a.id)}/traces`);
                            })
                          }
                        >
                          Run
                        </button>
                      )}
                      <Link className="btn btn--quiet btn--sm" to={`/automations/${encodeURIComponent(a.id)}/traces`}>
                        Runs
                      </Link>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Page>
  );
}
