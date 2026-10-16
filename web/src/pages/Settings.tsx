import { useEffect, useState } from "react";
import { api, aliasHex } from "../api";
import { Lamp } from "../components/Lamp";
import { Page } from "../components/Page";
import { duration, relativeTime, useNow } from "../format";
import { useBus } from "../store";

export function Settings() {
  const { links, health, nodes } = useBus();
  const now = useNow();
  const [ports, setPorts] = useState<string[]>();
  const [portsError, setPortsError] = useState<string>();
  const local = Object.values(nodes).find((n) => n.local);

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
    <Page title="Settings" lead="Links and the controller's identity live in interlock.yaml. Restart Interlock after editing it.">
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
            {"links:\n  - name: outpost\n    type: serial\n    device: " + (ports?.[0] ?? "/dev/cu.usbmodem1234")}
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
