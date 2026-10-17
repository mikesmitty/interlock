import { useEffect, useRef, useState } from "react";
import { api, aliasHex } from "../api";
import { Lamp } from "../components/Lamp";
import { Page } from "../components/Page";
import { duration, relativeTime, useNow } from "../format";
import { useBus } from "../store";

export function Settings() {
  const { links, health, nodes, connection } = useBus();
  const now = useNow();
  const [ports, setPorts] = useState<string[]>();
  const [portsError, setPortsError] = useState<string>();
  const [restarting, setRestarting] = useState(false);
  const [confirmRestart, setConfirmRestart] = useState(false);
  const [restartError, setRestartError] = useState<string>();
  const local = Object.values(nodes).find((n) => n.local);

  // Once the server is back after a restart, the page goes on as usual.
  useEffect(() => {
    if (restarting && connection === "live" && health) setRestarting(false);
  }, [restarting, connection, health]);

  async function restart() {
    setConfirmRestart(false);
    setRestartError(undefined);
    try {
      await api.restart();
      setRestarting(true);
    } catch (err) {
      setRestartError(err instanceof Error ? err.message : String(err));
    }
  }

  useEffect(() => {
    let cancelled = false;
    api
      .serialPorts()
      .then((p) => {
        if (!cancelled) setPorts(p);
      })
      .catch((err: unknown) => {
        if (!cancelled) setPortsError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <Page
      title="Settings"
      lead="Links and the controller's identity live in interlock.yaml. Restart Interlock after editing it."
      actions={
        confirmRestart ? (
          <span className="confirm">
            <span>Restart now? The bus goes quiet for a few seconds.</span>
            <button type="button" className="btn btn--primary" onClick={() => void restart()}>
              Restart
            </button>
            <button type="button" className="btn btn--quiet" onClick={() => setConfirmRestart(false)}>
              Not now
            </button>
          </span>
        ) : (
          <button type="button" className="btn" onClick={() => setConfirmRestart(true)} disabled={restarting}>
            {restarting ? "Restarting…" : "Restart Interlock"}
          </button>
        )
      }
    >
      {restarting && <p className="note">Interlock is restarting. This page reconnects on its own when it is back.</p>}
      {restartError && <p className="note note--error">{restartError}</p>}
      <h2>This controller</h2>
      <div className="panel">
        <dl className="plate">
          <dt>Name</dt>
          <dd>{local?.snip?.UserName || "Interlock"}</dd>
          <dt>Node ID</dt>
          <dd>{health?.node_id ?? "…"}</dd>
          <dt>Alias</dt>
          <dd>{health?.alias ? aliasHex(health.alias) : "none yet"}</dd>
          <dt>Bus state</dt>
          <dd>{health?.node_state ?? "…"}</dd>
          <dt>Version</dt>
          <dd>
            {health?.version ?? "…"}
            {health?.commit ? <span className="faint"> ({health.commit})</span> : null}
          </dd>
          <dt>Up for</dt>
          <dd>{health ? duration(health.uptime_seconds) : "…"}</dd>
        </dl>
      </div>

      <h2 style={{ marginTop: "1.5rem" }}>Links</h2>
      {links.length === 0 ? (
        <div className="panel empty">
          <p>
            No links yet. Add one to <code>interlock.yaml</code>, for example:
          </p>
          <pre className="mono" style={{ marginTop: "0.75rem" }}>
            {"links:\n  - name: outpost\n    type: serial\n    device: " + (ports?.[0] ?? examplePort(health?.os))}
          </pre>
          <p>Other tools can also connect to this controller's hub port and will appear here as links.</p>
        </div>
      ) : (
        <div className="panel">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">
                  <span className="sr-only">State</span>
                </th>
                <th scope="col">Link</th>
                <th scope="col">Connection</th>
                <th scope="col">Since</th>
                <th scope="col" className="num">
                  In
                </th>
                <th scope="col" className="num">
                  Out
                </th>
                <th scope="col" className="num">
                  Dropped
                </th>
                <th scope="col" className="num">
                  Reconnects
                </th>
              </tr>
            </thead>
            <tbody>
              {links.map((l) => (
                <tr key={l.name}>
                  <td>
                    <Lamp color={l.connected ? "green" : "red"} label={l.connected ? "connected" : "not connected"} />
                  </td>
                  <td>
                    <div className="cell-title">{l.name}</div>
                    <div className="cell-sub">
                      {l.kind} {l.target}
                    </div>
                  </td>
                  <td>
                    {l.connected ? "Connected" : "Not connected"}
                    {!l.connected && l.last_error && <div className="cell-sub">{l.last_error}</div>}
                  </td>
                  <td className="dim">{l.connected ? relativeTime(l.since, now) : ""}</td>
                  <td className="num dim">{l.frames_in}</td>
                  <td className="num dim">{l.frames_out}</td>
                  <td className={"num " + (l.dropped > 0 || l.parse_errors > 0 ? "" : "dim")}>
                    {l.dropped}
                    {l.parse_errors > 0 ? ` (+${l.parse_errors} unreadable)` : ""}
                  </td>
                  <td className="num dim">{l.reconnects}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <h2 style={{ marginTop: "1.5rem" }}>Backup and restore</h2>
      <BackupRestore onRestarting={() => setRestarting(true)} />

      <h2 style={{ marginTop: "1.5rem" }}>Serial ports on this machine</h2>
      <div className="panel">
        {portsError && <p className="empty">Could not list serial ports: {portsError}</p>}
        {!portsError && ports === undefined && <p className="empty">Looking…</p>}
        {ports && ports.length === 0 && <p className="empty">None found. Plug in a USB device and reload this page.</p>}
        {ports && ports.length > 0 && (
          <ul className="plate" style={{ listStyle: "none", gridTemplateColumns: "1fr" }}>
            {ports.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        )}
      </div>
    </Page>
  );
}

/** A typical USB serial port name on the server's operating system. */
function examplePort(os: string | undefined): string {
  switch (os) {
    case "windows":
      return "COM3";
    case "darwin":
      return "/dev/cu.usbmodem1234";
    default:
      return "/dev/ttyACM0";
  }
}

/** Download everything, or replace everything from an earlier download. */
function BackupRestore({ onRestarting }: { onRestarting: () => void }) {
  const [file, setFile] = useState<File>();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const input = useRef<HTMLInputElement>(null);

  async function restore() {
    if (!file) return;
    setBusy(true);
    setError(undefined);
    try {
      await api.restore(file);
      setConfirming(false);
      setFile(undefined);
      if (input.current) input.current.value = "";
      onRestarting();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="panel" style={{ padding: "1rem 1.25rem" }}>
      <div className="fw__pick">
        <a className="btn" href="/api/v1/backup" download>
          Download a backup
        </a>
        <span className="muted">A zip of interlock.yaml, the entities, automations, roster, your profiles and the database.</span>
      </div>
      <div className="fw__pick" style={{ marginTop: "1rem" }}>
        <label className="btn">
          Choose a backup to restore
          <input
            ref={input}
            type="file"
            accept=".zip,application/zip"
            className="sr-only"
            onChange={(e) => {
              setFile(e.target.files?.[0]);
              setConfirming(false);
              setError(undefined);
            }}
          />
        </label>
        {file ? (
          <span className="fw__file">
            {file.name} <span className="muted">({Math.round(file.size / 1024)} KB)</span>
          </span>
        ) : (
          <span className="muted">A file downloaded from here.</span>
        )}
        {file && !confirming && (
          <button type="button" className="btn btn--primary" onClick={() => setConfirming(true)} disabled={busy}>
            Restore
          </button>
        )}
      </div>
      {file && confirming && (
        <div className="confirm confirm--block" style={{ marginTop: "0.75rem" }}>
          <span>
            Replace this controller's configuration, entities, automations, roster, profiles and database with the ones in {file.name}? Interlock restarts
            to apply it, and the files it replaces are kept in a before-restore folder next to the data.
          </span>
          <button type="button" className="btn btn--primary" onClick={() => void restore()} disabled={busy}>
            {busy ? "Sending…" : "Restore and restart"}
          </button>
          <button type="button" className="btn btn--quiet" onClick={() => setConfirming(false)} disabled={busy}>
            Keep things as they are
          </button>
        </div>
      )}
      {error && <p className="note note--error">{error}</p>}
    </div>
  );
}
