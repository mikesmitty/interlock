import { useState } from "react";
import { useNavigate } from "react-router";
import { api, nodeName, trainNodeAddress, type NodeInfo, type ReplaceResult } from "../api";

interface Props {
  /** The node being replaced; it may already be forgotten. */
  id: string;
  node?: NodeInfo;
  nodes: Record<string, NodeInfo>;
  onClose?: () => void;
}

const sameModel = (a?: NodeInfo, b?: NodeInfo) => !!a?.snip && !!b?.snip && a.snip.Manufacturer === b.snip.Manufacturer && a.snip.Model === b.snip.Model;

/** Moves what names one node over to the node taking its place: for a
 * board swapped after a failure, or given a new node ID. */
export function ReplaceNode({ id, node, nodes, onClose }: Props) {
  const navigate = useNavigate();
  // The likeliest replacements first: the same kind of board, online.
  const candidates = Object.values(nodes)
    .filter((n) => n.id !== id && !n.local && !trainNodeAddress(n.id))
    .sort((a, b) => Number(sameModel(node, b)) - Number(sameModel(node, a)) || Number(b.online) - Number(a.online) || nodeName(a).localeCompare(nodeName(b)));
  const [target, setTarget] = useState(candidates[0]?.id ?? "");
  const [forget, setForget] = useState(!node?.online);
  const [preview, setPreview] = useState<ReplaceResult>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const targetNode = nodes[target];

  async function run(apply: boolean) {
    setBusy(true);
    setError(undefined);
    try {
      const res = await api.replaceNode(id, target, apply, forget);
      if (!apply) {
        setPreview(res);
        return;
      }
      if (res.forgotten || !node) {
        await navigate(`/nodes/${encodeURIComponent(res.to)}`, { state: { replaced: { from: id, count: res.changes.length } } });
      } else {
        setPreview(res);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const areas = [...new Set(preview?.changes.map((c) => c.area))];
  return (
    <section className="panel replace">
      <h2>Replace with another node</h2>
      <p className="muted">
        When this board is swapped for a new one, or given a new node ID, move everything that names it over to the node taking its place: the track plan's links, automations, and
        the names and settings of its entities. On the same kind of board, automations that use its events get the new board's events in the same places.
      </p>
      {candidates.length === 0 ? (
        <p className="note">No other node has been seen. Connect the new board first; it shows up here once it has identified itself.</p>
      ) : (
        <div className="replace__form">
          <label className="replace__field">
            <span>Node taking its place</span>
            <select
              className="input input--select"
              value={target}
              onChange={(e) => {
                setTarget(e.target.value);
                setPreview(undefined);
              }}
            >
              {candidates.map((n) => (
                <option key={n.id} value={n.id}>
                  {nodeName(n)} · {n.id}
                  {n.online ? "" : " (offline)"}
                  {node && !sameModel(node, n) ? " · a different model" : ""}
                </option>
              ))}
            </select>
          </label>
          <label className="check">
            <input type="checkbox" checked={forget} disabled={!node} onChange={(e) => setForget(e.target.checked)} />
            Forget this node afterward
          </label>
          <p className="replace__actions">
            <button type="button" className="btn" disabled={busy || !target} onClick={() => void run(false)}>
              Show what changes
            </button>
            {onClose && (
              <button type="button" className="btn btn--quiet" onClick={onClose}>
                Cancel
              </button>
            )}
          </p>
        </div>
      )}
      {error && <p className="note note--error">{error}</p>}
      {preview && (
        <div className="replace__result">
          {preview.notes?.map((n) => (
            <p key={n} className="note">
              {n}
            </p>
          ))}
          {preview.changes.length === 0 ? (
            <p>{preview.applied ? "Done; nothing named this node." : "Nothing names this node, so there is nothing to move."}</p>
          ) : (
            <>
              <p>
                {preview.applied
                  ? `Moved ${preview.changes.length} ${preview.changes.length === 1 ? "reference" : "references"} to ${targetNode ? nodeName(targetNode) : preview.to}.`
                  : `${preview.changes.length} ${preview.changes.length === 1 ? "reference" : "references"} will name ${targetNode ? nodeName(targetNode) : preview.to} instead:`}
              </p>
              {areas.map((area) => (
                <div key={area}>
                  <h3>{area}</h3>
                  <table className="table">
                    <tbody>
                      {preview.changes
                        .filter((c) => c.area === area)
                        .map((c, i) => (
                          <tr key={i}>
                            <td>
                              <div className="cell-title">{c.item}</div>
                              <div className="cell-sub">{c.field}</div>
                            </td>
                            <td className="replace__ids">
                              <div className="faint">{c.from}</div>
                              <div>
                                {c.to}
                                {c.missing && <span className="replace__missing"> · not on the new node yet</span>}
                              </div>
                            </td>
                          </tr>
                        ))}
                    </tbody>
                  </table>
                </div>
              ))}
            </>
          )}
          {!preview.applied && (preview.changes.length > 0 || forget) && (
            <p className="replace__actions">
              <button type="button" className="btn btn--primary" disabled={busy} onClick={() => void run(true)}>
                {busy ? "Moving" : forget ? "Move everything and forget this node" : "Move everything"}
              </button>
            </p>
          )}
        </div>
      )}
    </section>
  );
}
