import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { api, aliasHex, nodeModel, nodeName, type NodeInfo } from "../api";
import { Lamp } from "../components/Lamp";
import { Empty, Page } from "../components/Page";
import { plural, relativeTime, useNow } from "../format";
import { useBus } from "../store";

/** Train nodes live in the DCC range 06.01.xx.xx.xx.xx; command stations make them for locomotives. */
function isTrainNode(n: NodeInfo): boolean {
  return n.id.startsWith("06.01.");
}

function sortNodes(nodes: NodeInfo[]): NodeInfo[] {
  return [...nodes].sort((a, b) => {
    if (a.local !== b.local) return a.local ? -1 : 1;
    if (a.online !== b.online) return a.online ? -1 : 1;
    return nodeName(a).localeCompare(nodeName(b)) || a.id.localeCompare(b.id);
  });
}

export function Nodes() {
  const { nodes, links, loaded } = useBus();
  const now = useNow();
  const navigate = useNavigate();
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string>();

  const all = sortNodes(Object.values(nodes));
  const trainNodes = all.filter((n) => isTrainNode(n));
  const list = all.filter((n) => !isTrainNode(n));
  const others = list.filter((n) => !n.local);
  const online = others.filter((n) => n.online).length;

  async function refresh() {
    setRefreshing(true);
    setError(undefined);
    try {
      await api.refreshNodes();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      window.setTimeout(() => setRefreshing(false), 1500);
    }
  }

  const lead = !loaded
    ? "Loading…"
    : others.length === 0
      ? "Only this controller so far."
      : `${plural(others.length, "node")} on the bus, ${online} online${trainNodes.length ? `, and ${plural(trainNodes.length, "train node")}` : ""}.`;

  return (
    <Page
      title="Nodes"
      lead={lead}
      actions={
        <button className="btn" onClick={() => void refresh()} disabled={refreshing}>
          {refreshing ? "Asking…" : "Ask nodes to check in"}
        </button>
      }
    >
      {error && <p className="note note--error">Could not start a check-in: {error}</p>}
      {loaded && others.length === 0 && links.length === 0 && (
        <Empty title="No other nodes yet">
          <p>
            Interlock has no connection to a layout. Add a link to the USB port of a canbus-outpost, or to a GridConnect
            TCP server, in <code>interlock.yaml</code>. <Link to="/settings">Settings</Link> shows where that file is
            and which serial ports this machine has.
          </p>
          <p>Nodes announce themselves as soon as a link comes up; nothing else is needed.</p>
        </Empty>
      )}
      {loaded && others.length === 0 && links.length > 0 && (
        <Empty title="Listening for nodes">
          <p>
            {links.some((l) => l.connected)
              ? "A link is up but no node has spoken yet. Powering a node on, or asking nodes to check in, will show them here."
              : "No link is connected. The lamps in the strip above show which links are down and why."}
          </p>
        </Empty>
      )}
      {loaded && (others.length > 0 || links.length > 0) && (
        <NodeTable list={list} now={now} onOpen={(id) => navigate(`/nodes/${id}`)} />
      )}
      {loaded && trainNodes.length > 0 && (
        <>
          <h2 style={{ marginTop: "1.5rem" }}>Train nodes</h2>
          <p className="muted" style={{ marginBottom: "0.5rem" }}>
            Made by the command station for the locomotives throttles asked for. Drive them from the{" "}
            <Link to="/throttle">throttle</Link>.
          </p>
          <NodeTable list={trainNodes} now={now} onOpen={(id) => navigate(`/nodes/${id}`)} />
        </>
      )}
    </Page>
  );
}

function NodeTable({ list, now, onOpen }: { list: NodeInfo[]; now: number; onOpen: (id: string) => void }) {
  return (
    <div className="panel">
      <table className="table table--rows">
        <thead>
          <tr>
            <th scope="col">
              <span className="sr-only">State</span>
            </th>
            <th scope="col">Name</th>
            <th scope="col">Node ID</th>
            <th scope="col">Alias</th>
            <th scope="col">Software</th>
            <th scope="col">Last seen</th>
          </tr>
        </thead>
        <tbody>
          {list.map((n) => (
            <tr
              key={n.id}
              className="is-link"
              onClick={() => onOpen(n.id)}
              onKeyDown={(e) => {
                if (e.key === "Enter") onOpen(n.id);
              }}
              tabIndex={0}
            >
              <td>
                <Lamp color={n.online ? "green" : "red"} label={n.online ? "online" : "offline"} />
              </td>
              <td>
                <div className="cell-title">
                  {nodeName(n)}
                  {n.local && <span className="faint"> (this controller)</span>}
                </div>
                {nodeModel(n) && nodeModel(n) !== nodeName(n) && <div className="cell-sub">{nodeModel(n)}</div>}
                {!n.snip && n.error && <div className="cell-sub">Did not answer identification</div>}
              </td>
              <td className="dim">{n.id}</td>
              <td className="dim">{n.alias ? aliasHex(n.alias) : ""}</td>
              <td className="dim">{n.snip?.SoftwareVersion ?? ""}</td>
              <td className="dim">
                {n.local
                  ? ""
                  : n.online
                    ? relativeTime(n.last_seen, now)
                    : `offline, ${relativeTime(n.last_seen, now)}`}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
