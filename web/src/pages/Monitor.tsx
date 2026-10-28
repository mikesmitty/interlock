import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { aliasHex, api, nodeName, type Endpoint, type MonitorEntry, type NodeInfo } from "../api";
import { useEventLabels, type EventLabeler } from "../labels";
import { Page } from "../components/Page";
import { clockTime, plural } from "../format";
import { bus, useBus } from "../store";

const HOUSEKEEPING = new Set(["CID1", "CID2", "CID3", "CID4", "RID", "AMD", "AME", "AMR"]);
const MAX_ROWS = 1000;
const EVENT_ID_RE = /\b(?:[0-9A-F]{2}\.){7}[0-9A-F]{2}\b/g;

/** The summary with every event ID it names followed by what it is. */
function Annotated({ text, labels }: { text?: string; labels: EventLabeler }) {
  if (!text) return null;
  const parts: ReactNode[] = [];
  let last = 0;
  for (const m of text.matchAll(EVENT_ID_RE)) {
    const label = labels(m[0]);
    if (!label || m.index === undefined) continue;
    const end = m.index + m[0].length;
    parts.push(text.slice(last, end));
    parts.push(
      <span className="faint" key={end}>
        {" "}
        {label}
      </span>,
    );
    last = end;
  }
  if (parts.length === 0) return <>{text}</>;
  parts.push(text.slice(last));
  return <>{parts}</>;
}

/** Name a node the way the nodes page does, falling back to ID or alias. */
export function describeEndpoint(ep: Endpoint | undefined, nodes: Record<string, NodeInfo>): string {
  if (!ep) return "";
  if (ep.id) {
    const n = nodes[ep.id];
    return n ? nodeName(n) : ep.id;
  }
  const byAlias = Object.values(nodes).find((n) => n.alias === ep.alias && n.online);
  return byAlias ? nodeName(byAlias) : aliasHex(ep.alias);
}

function matchesFilter(e: MonitorEntry, needle: string, nodes: Record<string, NodeInfo>): boolean {
  if (!needle) return true;
  const d = e.decoded;
  const hay = [
    e.frame ?? "",
    e.link ?? "",
    d?.type ?? "",
    d?.summary ?? "",
    d?.payload ?? "",
    describeEndpoint(d?.source, nodes),
    describeEndpoint(d?.dest, nodes),
    d?.source?.id ?? "",
    d?.dest?.id ?? "",
  ]
    .join(" ")
    .toLowerCase();
  return hay.includes(needle);
}

export function Monitor() {
  const { entries, nodes, links, lagged, connection } = useBus();
  const labels = useEventLabels();
  const [paused, setPaused] = useState(false);
  const [frozen, setFrozen] = useState<MonitorEntry[]>([]);
  const [filter, setFilter] = useState("");
  const [hideHousekeeping, setHideHousekeeping] = useState(false);
  const [hideFragments, setHideFragments] = useState(true);
  const [expanded, setExpanded] = useState<number>();
  const [follow, setFollow] = useState(true);
  const scrollRef = useRef<HTMLDivElement>(null);

  const source = paused ? frozen : entries;
  const needle = filter.trim().toLowerCase();
  const rows = useMemo(() => {
    const out: MonitorEntry[] = [];
    for (let i = source.length - 1; i >= 0 && out.length < MAX_ROWS; i--) {
      const e = source[i];
      if (hideFragments && e.kind === "fragment") continue;
      if (hideHousekeeping && e.decoded && HOUSEKEEPING.has(e.decoded.type)) continue;
      if (!matchesFilter(e, needle, nodes)) continue;
      out.push(e);
    }
    return out.reverse();
  }, [source, hideFragments, hideHousekeeping, needle, nodes]);

  useEffect(() => {
    if (!follow || paused) return;
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [rows, follow, paused]);

  function onScroll() {
    const el = scrollRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    if (atBottom !== follow) setFollow(atBottom);
  }

  function togglePause() {
    if (!paused) setFrozen(entries);
    setPaused(!paused);
  }

  function clear() {
    bus.clearEntries();
    setFrozen([]);
    setExpanded(undefined);
  }

  const lead = connection !== "live" ? "Waiting for the server." : links.length === 0 ? "No links are configured, so only this controller's own traffic appears." : undefined;

  return (
    <Page title="Monitor" lead={lead}>
      <div className="monitor">
        <div className="monitor__bar">
          <button className="btn btn--quiet" aria-pressed={paused} onClick={togglePause}>
            {paused ? "Resume" : "Pause"}
          </button>
          <button className="btn btn--quiet" onClick={clear}>
            Clear
          </button>
          <input
            className="input"
            type="search"
            placeholder="Filter by node, type or text"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            aria-label="Filter entries"
          />
          <label className="check">
            <input type="checkbox" checked={hideFragments} onChange={(e) => setHideFragments(e.target.checked)} />
            Hide frame fragments
          </label>
          <label className="check">
            <input type="checkbox" checked={hideHousekeeping} onChange={(e) => setHideHousekeeping(e.target.checked)} />
            Hide alias housekeeping
          </label>
          <span className="monitor__count">
            {paused ? "Paused, " : ""}
            {plural(rows.length, "entry", "entries")}
            {rows.length === MAX_ROWS ? " shown" : ""}
            {lagged ? ", some were skipped" : ""}
          </span>
        </div>

        <div className="panel">
          <div className="monitor__scroll" ref={scrollRef} onScroll={onScroll}>
            {rows.length === 0 ? (
              <div className="empty">
                <h2>{entries.length === 0 ? "Waiting for traffic" : "Nothing matches"}</h2>
                <p>
                  {entries.length === 0
                    ? "Every frame on the bus will appear here as it happens, with what it means."
                    : "Try a shorter filter, or show fragments and housekeeping again."}
                </p>
              </div>
            ) : (
              <table className="table">
                <thead>
                  <tr>
                    <th scope="col">Time</th>
                    <th scope="col">Link</th>
                    <th scope="col">From</th>
                    <th scope="col">To</th>
                    <th scope="col">Type</th>
                    <th scope="col">Detail</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((e) => {
                    const d = e.decoded;
                    const classes = [];
                    if (e.dir === "out") classes.push("is-out");
                    if (e.kind === "control") classes.push("is-control");
                    if (expanded === e.seq) classes.push("is-expanded");
                    const isOpen = expanded === e.seq;
                    return (
                      <RowGroup key={e.seq}>
                        <tr className={classes.join(" ")} onClick={() => setExpanded(isOpen ? undefined : e.seq)} aria-expanded={isOpen}>
                          <td className="dim">{clockTime(e.time)}</td>
                          <td className="dim">{e.dir === "out" ? "sent" : e.link}</td>
                          <td>{e.dir === "out" && !d?.source?.id ? "this controller" : describeEndpoint(d?.source, nodes)}</td>
                          <td>{d?.dest ? describeEndpoint(d.dest, nodes) : <span className="faint">all</span>}</td>
                          <td className="cell-type">{d?.type ?? e.kind}</td>
                          <td className="wrap">
                            <Annotated text={d?.summary} labels={labels} />
                          </td>
                        </tr>
                        {isOpen && (
                          <tr className="raw">
                            <td colSpan={6}>
                              {e.frame ? <code>{e.frame}</code> : <code>assembled from several frames</code>}
                              {d?.payload && <span className="faint">payload {d.payload}</span>}
                              {d?.mti !== undefined && d.mti !== 0 && <span className="faint">MTI 0x{d.mti.toString(16).toUpperCase().padStart(3, "0")}</span>}
                            </td>
                          </tr>
                        )}
                      </RowGroup>
                    );
                  })}
                </tbody>
              </table>
            )}
          </div>
        </div>

        <SendFrame />
      </div>
    </Page>
  );
}

function RowGroup({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}

function SendFrame() {
  const [frame, setFrame] = useState("");
  const [result, setResult] = useState<string>();

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setResult(undefined);
    try {
      const r = await api.sendFrame(frame.trim());
      setResult(`Sent ${r.frame}`);
    } catch (err) {
      setResult(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <details className="monitor__send">
      <summary>Send a raw frame</summary>
      <form onSubmit={(e) => void submit(e)}>
        <input className="input" value={frame} onChange={(e) => setFrame(e.target.value)} placeholder=":X19490ABCN;" aria-label="GridConnect frame" spellCheck={false} />
        <button className="btn" type="submit" disabled={!frame.trim()}>
          Send
        </button>
        {result && <span className="muted">{result}</span>}
      </form>
    </details>
  );
}
