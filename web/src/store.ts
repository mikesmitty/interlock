// One live connection to the server feeds every page: nodes, links and
// bus traffic arrive over server-sent events and land in this store.
import { useSyncExternalStore } from "react";
import { api, type Health, type LinkStatus, type MonitorEntry, type NodeInfo } from "./api";

export type Connection = "connecting" | "live" | "reconnecting";

export interface BusState {
  connection: Connection;
  /** True once the first full load after connecting has finished. */
  loaded: boolean;
  nodes: Record<string, NodeInfo>;
  links: LinkStatus[];
  entries: MonitorEntry[];
  lastSeq: number;
  lagged: boolean;
  health?: Health;
}

const MAX_ENTRIES = 2000;

type Listener = () => void;

function mergeEntries(current: MonitorEntry[], incoming: MonitorEntry[]): MonitorEntry[] {
  if (incoming.length === 0) return current;
  const bySeq = new Map<number, MonitorEntry>();
  for (const e of current) bySeq.set(e.seq, e);
  for (const e of incoming) bySeq.set(e.seq, e);
  const merged = [...bySeq.values()].sort((a, b) => a.seq - b.seq);
  return merged.length > MAX_ENTRIES ? merged.slice(merged.length - MAX_ENTRIES) : merged;
}

class BusStore {
  private state: BusState = {
    connection: "connecting",
    loaded: false,
    nodes: {},
    links: [],
    entries: [],
    lastSeq: 0,
    lagged: false,
  };
  private listeners = new Set<Listener>();
  private source?: EventSource;
  private pending: MonitorEntry[] = [];
  private flushTimer?: number;

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  getSnapshot = (): BusState => this.state;

  private set(patch: Partial<BusState>) {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener();
  }

  start() {
    if (this.source) return;
    const es = new EventSource("/api/v1/events");
    this.source = es;
    es.addEventListener("ready", (ev) => {
      const { seq } = JSON.parse((ev as MessageEvent<string>).data) as { seq: number };
      void this.load(seq);
    });
    es.addEventListener("node", (ev) => {
      const info = JSON.parse((ev as MessageEvent<string>).data) as NodeInfo;
      if (info.forgotten) {
        const nodes = { ...this.state.nodes };
        delete nodes[info.id];
        this.set({ nodes });
        return;
      }
      this.set({ nodes: { ...this.state.nodes, [info.id]: info } });
    });
    es.addEventListener("links", (ev) => {
      const { links } = JSON.parse((ev as MessageEvent<string>).data) as { links: LinkStatus[] };
      this.set({ links });
    });
    es.addEventListener("frame", (ev) => {
      this.pending.push(JSON.parse((ev as MessageEvent<string>).data) as MonitorEntry);
      if (this.flushTimer === undefined) {
        this.flushTimer = window.setTimeout(() => this.flush(), 40);
      }
    });
    es.addEventListener("lagged", () => this.set({ lagged: true }));
    es.onerror = () => {
      if (this.state.connection !== "reconnecting") this.set({ connection: "reconnecting" });
    };
  }

  private flush() {
    this.flushTimer = undefined;
    const incoming = this.pending;
    this.pending = [];
    const fresh = incoming.filter((e) => e.seq > this.state.lastSeq);
    if (fresh.length === 0) return;
    let entries = this.state.entries.concat(fresh);
    if (entries.length > MAX_ENTRIES) entries = entries.slice(entries.length - MAX_ENTRIES);
    this.set({ entries, lastSeq: fresh[fresh.length - 1].seq });
  }

  /** After (re)connecting: fetch the state we may have missed. */
  private async load(serverSeq: number) {
    this.set({ connection: "live" });
    try {
      const [nodes, links, health] = await Promise.all([api.nodes(), api.links(), api.health()]);
      const map: Record<string, NodeInfo> = {};
      for (const n of nodes) map[n.id] = n;
      const haveHistory = this.state.lastSeq > 0 && this.state.lastSeq <= serverSeq;
      const backlog = haveHistory
        ? await api.monitor({ since: this.state.lastSeq, limit: 0 })
        : await api.monitor({ limit: 500 });
      const entries = mergeEntries(haveHistory ? this.state.entries : [], backlog.entries);
      this.set({
        nodes: map,
        links,
        health,
        entries,
        lastSeq: Math.max(this.state.lastSeq, backlog.last_seq),
        lagged: false,
        loaded: true,
      });
    } catch (err) {
      console.error("loading state after connect", err);
    }
  }

  clearEntries() {
    this.set({ entries: [], lagged: false });
  }

  async refreshNodes() {
    const nodes = await api.nodes();
    const map: Record<string, NodeInfo> = {};
    for (const n of nodes) map[n.id] = n;
    this.set({ nodes: map });
  }
}

export const bus = new BusStore();

export function useBus(): BusState {
  return useSyncExternalStore(bus.subscribe, bus.getSnapshot);
}
