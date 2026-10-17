import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, aliasHex, nodeModel, nodeName, type CdiStatus, type MonitorEntry, type NodeInfo } from "../api";
import { ConfigForm } from "../components/ConfigForm";
import { EntityRows } from "../components/EntityRows";
import { Status } from "../components/Lamp";
import { Empty, Page } from "../components/Page";
import { clockTime, relativeTime, useNow } from "../format";
import { useBus } from "../store";
import { describeEndpoint } from "./Monitor";

export function NodeDetail() {
  const { id = "" } = useParams();
  const { nodes, entries, entities, loaded } = useBus();
  const now = useNow();
  const navigate = useNavigate();
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

  async function act(label: string, fn: () => Promise<unknown>, done: string) {
    setBusy(true);
    setMessage(undefined);
    try {
      await fn();
      setMessage(done);
    } catch (err) {
      setMessage(`${label} failed: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setBusy(false);
    }
  }

  const snip = node.snip;
  const configurable = !node.local && node.protocols?.includes("CDI") && node.protocols.includes("Memory Configuration");
  return (
    <Page
      title={nodeName(node)}
      lead={node.local ? "This controller" : nodeModel(node) !== nodeName(node) ? nodeModel(node) : undefined}
      back={back}
      actions={
        !node.local && (
          <>
            {!node.online && (
              <button
                className="btn btn--quiet"
                disabled={busy}
                onClick={() =>
                  void act("Forgetting the node", async () => {
                    await api.forgetNode(node.id);
                    await navigate("/nodes");
                  }, "Forgotten.")
                }
              >
                Forget node
              </button>
            )}
            <button className="btn" onClick={() => void act("Identification", () => api.refreshNode(node.id), "Asked the node to identify itself again.")} disabled={busy}>
              Identify again
            </button>
          </>
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
          {!node.local && (
            <>
              <dt>Last seen</dt>
              <dd>{relativeTime(node.last_seen, now)}</dd>
            </>
          )}
        </dl>
      </div>

      {!node.local && <NodeEntities nodeId={node.id} entities={Object.values(entities)} />}

      {configurable && <Configuration node={node} />}

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

/** The entities built from this node, with their live state. */
function NodeEntities({ nodeId, entities }: { nodeId: string; entities: import("../api").Entity[] }) {
  const mine = entities.filter((e) => e.node === nodeId && !e.hidden);
  if (mine.length === 0) return null;
  const suggested = mine.filter((e) => e.suggested).length;
  return (
    <>
      <div className="cfg-segment__head" style={{ marginTop: "1.75rem" }}>
        <h2>Entities</h2>
        <span className="cfg-segment__status">
          {suggested > 0 ? `${suggested} suggested · ` : ""}
          <Link to="/entities">all entities</Link>
        </span>
      </div>
      <div className="panel">
        <table className="table ent-table">
          <tbody>
            <EntityRows entities={mine} compact />
          </tbody>
        </table>
      </div>
    </>
  );
}

function kb(n: number): string {
  return n >= 1024 ? `${(n / 1024).toFixed(1)} KB` : `${n} bytes`;
}

/** The configuration section: the CDI's state, then the form once it is known. */
function Configuration({ node }: { node: NodeInfo }) {
  const now = useNow();
  const [retrying, setRetrying] = useState(false);
  const cdi: CdiStatus | undefined = node.cdi;

  async function readAgain() {
    setRetrying(true);
    try {
      await api.refreshCdi(node.id);
    } finally {
      setRetrying(false);
    }
  }

  let body: React.ReactNode;
  switch (cdi?.state) {
    case "ready":
      body = <ConfigForm nodeId={node.id} nodeName={nodeName(node)} online={node.online} />;
      break;
    case "reading": {
      const pct = cdi.total ? Math.min(100, Math.round(((cdi.read ?? 0) / cdi.total) * 100)) : undefined;
      body = (
        <div className="panel" style={{ padding: "1rem 1.25rem" }}>
          <p>
            Reading how this node is configured
            {cdi.read ? `: ${kb(cdi.read)}${cdi.total ? ` of ${kb(cdi.total)}` : ""}` : "…"}
          </p>
          <div className="progress" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
            <div className={pct === undefined ? "progress__bar progress__bar--indeterminate" : "progress__bar"} style={pct === undefined ? undefined : { width: `${pct}%` }} />
          </div>
          <p className="muted" style={{ marginTop: "0.5rem", fontSize: "var(--fs-sm)" }}>
            Nodes send their description 64 bytes at a time; this takes a few seconds and happens once per software version.
          </p>
        </div>
      );
      break;
    }
    case "error":
      body = (
        <div className="note note--error">
          <p>The node's configuration description could not be read: {cdi.error}</p>
          <button className="btn" style={{ marginTop: "0.5rem" }} onClick={() => void readAgain()} disabled={retrying || !node.online}>
            Try again
          </button>
        </div>
      );
      break;
    case "pending":
      body = <p className="muted">Waiting to read the node's configuration description.</p>;
      break;
    default:
      body = <p className="muted">This node says it is configurable; its description will be read when it is identified.</p>;
  }

  return (
    <>
      <div className="cfg-segment__head" style={{ marginTop: "1.75rem" }}>
        <h2>Configuration</h2>
        {cdi?.state === "ready" && (
          <span className="cfg-segment__status">
            description {cdi.cached ? "from cache, read" : "read"} {cdi.fetched_at ? relativeTime(cdi.fetched_at, now) : ""}
            {cdi.truncated ? ", incomplete" : ""}
            {" · "}
            <a href={`/api/v1/nodes/${encodeURIComponent(node.id)}/cdi?format=xml`} target="_blank" rel="noreferrer">
              XML
            </a>
            {" · "}
            <button type="button" className="linklike" onClick={() => void readAgain()} disabled={retrying || !node.online}>
              read again
            </button>
          </span>
        )}
      </div>
      {cdi?.truncated && cdi.state === "ready" && (
        <p className="note">The node's description ended early, so only part of its configuration is shown. Reading it again may help.</p>
      )}
      {body}
    </>
  );
}
