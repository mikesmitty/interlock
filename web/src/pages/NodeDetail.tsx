import { useMemo, useState } from "react";
import { Link, useParams } from "react-router";
import { api, aliasHex, nodeModel, nodeName, type MonitorEntry } from "../api";
import { Status } from "../components/Lamp";
import { Empty, Page } from "../components/Page";
import { clockTime, relativeTime, useNow } from "../format";
import { useBus } from "../store";
import { describeEndpoint } from "./Monitor";

export function NodeDetail() {
  const { id = "" } = useParams();
  const { nodes, entries, loaded } = useBus();
  const now = useNow();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const node = nodes[id.toUpperCase()] ?? Object.values(nodes).find((n) => n.id.toUpperCase() === id.toUpperCase());

  const traffic = useMemo(() => {
    if (!node) return [] as MonitorEntry[];
    const alias = node.alias;
    const matches = (e: MonitorEntry) => {
      const d = e.decoded;
      if (!d) return false;
      return d.source?.id === node.id || d.dest?.id === node.id || (alias !== 0 && (d.source?.alias === alias || d.dest?.alias === alias));
    };
    return entries.filter(matches).slice(-40).reverse();
  }, [entries, node]);

  const back = (
    <Link className="page__back" to="/nodes">
      Nodes
    </Link>
  );

  if (!node && !loaded) {
    return (
      <Page title="Loading…" back={back}>
        <p className="muted">Waiting for the node list.</p>
      </Page>
    );
  }

  if (!node) {
    return (
      <Page title="Unknown node" back={back}>
        <Empty title={`No node ${id} has been seen`}>
          <p>Nodes appear in the list when they announce themselves or answer a check-in.</p>
        </Empty>
      </Page>
    );
  }

  async function refresh() {
    if (!node) return;
    setBusy(true);
    setMessage(undefined);
    try {
      await api.refreshNode(node.id);
      setMessage("Asked the node to identify itself again.");
    } catch (err) {
      setMessage(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const snip = node.snip;
  return (
    <Page
      title={nodeName(node)}
      lead={node.local ? "This controller" : nodeModel(node) !== nodeName(node) ? nodeModel(node) : undefined}
      back={back}
      actions={
        !node.local && (
          <button className="btn" onClick={() => void refresh()} disabled={busy}>
            Identify again
          </button>
        )
      }
    >
      {message && <p className="note">{message}</p>}
      {node.error && !snip && <p className="note note--error">The node did not answer identification: {node.error}</p>}
      <div className="panel">
        <dl className="plate">
          <dt>State</dt>
          <dd>
            <Status color={node.online ? "green" : "red"}>{node.online ? "Online" : "Offline"}</Status>
          </dd>
          <dt>Node ID</dt>
          <dd>{node.id}</dd>
          <dt>Alias</dt>
          <dd>{node.alias ? aliasHex(node.alias) : "none"}</dd>
          {snip && (
            <>
              <dt>Manufacturer</dt>
              <dd>{snip.Manufacturer || "—"}</dd>
              <dt>Model</dt>
              <dd>{snip.Model || "—"}</dd>
              <dt>Hardware</dt>
              <dd>{snip.HardwareVersion || "—"}</dd>
              <dt>Software</dt>
              <dd>{snip.SoftwareVersion || "—"}</dd>
              {snip.UserDescription && (
                <>
                  <dt>Description</dt>
                  <dd>{snip.UserDescription}</dd>
                </>
              )}
            </>
          )}
          <dt>Protocols</dt>
          <dd>{node.protocols && node.protocols.length > 0 ? node.protocols.join(", ") : <span className="muted">not known yet</span>}</dd>
          <dt>First seen</dt>
          <dd>{relativeTime(node.first_seen, now)}</dd>
          <dt>Last seen</dt>
          <dd>{relativeTime(node.last_seen, now)}</dd>
        </dl>
      </div>

      <h2 style={{ marginTop: "1.5rem" }}>Recent traffic</h2>
      {traffic.length === 0 ? (
        <p className="muted" style={{ marginTop: "0.5rem" }}>
          Nothing from this node in the monitor yet.
        </p>
      ) : (
        <div className="panel monitor" style={{ marginTop: "0.5rem" }}>
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Time</th>
                <th scope="col">Direction</th>
                <th scope="col">Type</th>
                <th scope="col">Detail</th>
              </tr>
            </thead>
            <tbody>
              {traffic.map((e) => (
                <tr key={e.seq}>
                  <td className="dim">{clockTime(e.time)}</td>
                  <td className="dim">{e.decoded?.source?.id === node.id || e.decoded?.source?.alias === node.alias ? "from node" : "to node"}</td>
                  <td>{e.decoded?.type}</td>
                  <td className="wrap">
                    {e.decoded?.summary}
                    {e.decoded?.dest && e.decoded.source && (
                      <span className="faint">
                        {" "}
                        ({describeEndpoint(e.decoded.source, nodes)} to {describeEndpoint(e.decoded.dest, nodes)})
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Page>
  );
}
