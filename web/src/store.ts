// One live connection to the server feeds every page: nodes, links and
// bus traffic arrive over server-sent events and land in this store.
import { useSyncExternalStore } from "react";
import {
  api,
  type AutomationStatus,
  type ClockState,
  type Car,
  type Consist,
  type Formation,
  type Entity,
  type EventName,
  type FirmwareJob,
  type Health,
  type LayoutState,
  type LinkStatus,
  type MonitorEntry,
  type NodeInfo,
  type Trace,
  type TrackPlanState,
  type TrackedTrain,
  type Train,
} from "./api";

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
  entities: Record<string, Entity>;
  /** The named event table, every node's events by name. */
  eventNames: EventName[];
  automations: Record<string, AutomationStatus>;
  /** Traces that moved since the page loaded, newest last; a trace page
   * merges these over what it fetched. */
  liveTraces: Trace[];
  trains: Record<string, Train>;
  consists: Record<string, Consist>;
  cars: Record<string, Car>;
  /** The trains made up of locomotives and cars, by ID. */
  formations: Record<string, Formation>;
  layout: LayoutState;
  /** The latest firmware upgrade of each node, by node ID. */
  firmware: Record<string, FirmwareJob>;
  /** The fast clock, as of its last change or minute. */
  clock?: ClockState;
  /** The track plan as last saved. */
  trackPlan?: TrackPlanState;
  /** The trains train tracking follows, by ID. */
  tracked: Record<string, TrackedTrain>;
}

const MAX_LIVE_TRACES = 50;

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
    entities: {},
    eventNames: [],
    automations: {},
    liveTraces: [],
    trains: {},
    consists: {},
    cars: {},
    formations: {},
    layout: { power_off: false, power_known: false, emergency_stop: false },
    firmware: {},
    tracked: {},
  };
  private listeners = new Set<Listener>();
  private source?: EventSource;
  private pending: MonitorEntry[] = [];
  private flushTimer?: number;
  private namesTimer?: number;

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
      const { seq } = JSON.parse((ev as MessageEvent<string>).data) as {
        seq: number;
      };
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
    es.addEventListener("trackplan", (ev) => {
      this.set({ trackPlan: JSON.parse((ev as MessageEvent<string>).data) as TrackPlanState });
    });
    es.addEventListener("tracked", (ev) => {
      const t = JSON.parse((ev as MessageEvent<string>).data) as TrackedTrain;
      const tracked = { ...this.state.tracked };
      if (t.removed) delete tracked[t.id];
      else tracked[t.id] = t;
      this.set({ tracked });
    });
    es.addEventListener("clock", (ev) => {
      this.set({ clock: JSON.parse((ev as MessageEvent<string>).data) as ClockState });
    });
    es.addEventListener("firmware", (ev) => {
      const job = JSON.parse((ev as MessageEvent<string>).data) as FirmwareJob;
      this.set({ firmware: { ...this.state.firmware, [job.node]: job } });
    });
    es.addEventListener("links", (ev) => {
      const { links } = JSON.parse((ev as MessageEvent<string>).data) as {
        links: LinkStatus[];
      };
      this.set({ links });
    });
    es.addEventListener("frame", (ev) => {
      this.pending.push(JSON.parse((ev as MessageEvent<string>).data) as MonitorEntry);
      if (this.flushTimer === undefined) {
        this.flushTimer = window.setTimeout(() => this.flush(), 40);
      }
    });
    es.addEventListener("entity", (ev) => {
      const entity = JSON.parse((ev as MessageEvent<string>).data) as Entity;
      const entities = { ...this.state.entities };
      if (entity.removed) delete entities[entity.id];
      else entities[entity.id] = entity;
      this.set({ entities });
    });
    es.addEventListener("event_names", () => {
      // Several nodes may change at once; fetch the table once they settle.
      if (this.namesTimer !== undefined) window.clearTimeout(this.namesTimer);
      this.namesTimer = window.setTimeout(() => {
        this.namesTimer = undefined;
        void this.refreshEventNames();
      }, 300);
    });
    es.addEventListener("automation", (ev) => {
      const status = JSON.parse((ev as MessageEvent<string>).data) as AutomationStatus;
      const automations = { ...this.state.automations };
      if (status.removed) delete automations[status.id];
      else automations[status.id] = status;
      this.set({ automations });
    });
    es.addEventListener("trace", (ev) => {
      const trace = JSON.parse((ev as MessageEvent<string>).data) as Trace;
      const liveTraces = this.state.liveTraces.filter((t) => t.id !== trace.id).concat(trace);
      this.set({
        liveTraces: liveTraces.length > MAX_LIVE_TRACES ? liveTraces.slice(liveTraces.length - MAX_LIVE_TRACES) : liveTraces,
      });
    });
    es.addEventListener("train", (ev) => {
      const train = JSON.parse((ev as MessageEvent<string>).data) as Train;
      const trains = { ...this.state.trains };
      if (train.removed) delete trains[train.id];
      else trains[train.id] = train;
      this.set({ trains });
    });
    es.addEventListener("consist", (ev) => {
      const consist = JSON.parse((ev as MessageEvent<string>).data) as Consist;
      const consists = { ...this.state.consists };
      if (consist.removed) delete consists[consist.id];
      else consists[consist.id] = consist;
      this.set({ consists });
    });
    es.addEventListener("car", (ev) => {
      const car = JSON.parse((ev as MessageEvent<string>).data) as Car;
      const cars = { ...this.state.cars };
      if (car.removed) delete cars[car.id];
      else cars[car.id] = car;
      this.set({ cars });
    });
    es.addEventListener("formation", (ev) => {
      const f = JSON.parse((ev as MessageEvent<string>).data) as Formation;
      const formations = { ...this.state.formations };
      if (f.removed) delete formations[f.id];
      else formations[f.id] = f;
      this.set({ formations });
    });
    es.addEventListener("layout", (ev) => {
      this.set({
        layout: JSON.parse((ev as MessageEvent<string>).data) as LayoutState,
      });
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
      const [nodes, links, health, entityList, eventNames, automationList, trainsResponse, firmwareJobs, consistsResponse] = await Promise.all([
        api.nodes(),
        api.links(),
        api.health(),
        api.entities(),
        api.eventNames(),
        api.automations(),
        api.trains(),
        api.firmwareJobs().catch(() => [] as FirmwareJob[]),
        api.consists().catch(() => ({ consists: [] as Consist[] })),
      ]);
      const clock = await api.clock().catch(() => undefined);
      const trackPlan = await api.trackPlan().catch(() => undefined);
      const trackingResponse = await api.tracking().catch(() => ({ trains: [] as TrackedTrain[] }));
      const carsResponse = await api.cars().catch(() => ({ cars: [] as Car[] }));
      const formationsResponse = await api.formations().catch(() => ({ formations: [] as Formation[] }));
      const cars: Record<string, Car> = {};
      for (const c of carsResponse.cars) cars[c.id] = c;
      const formations: Record<string, Formation> = {};
      for (const f of formationsResponse.formations) formations[f.id] = f;
      const tracked: Record<string, TrackedTrain> = {};
      for (const t of trackingResponse.trains) tracked[t.id] = t;
      const firmware: Record<string, FirmwareJob> = {};
      for (const j of firmwareJobs) firmware[j.node] = j;
      const trains: Record<string, Train> = {};
      for (const t of trainsResponse.trains) trains[t.id] = t;
      const consists: Record<string, Consist> = {};
      for (const c of consistsResponse.consists) consists[c.id] = c;
      const automations: Record<string, AutomationStatus> = {};
      for (const a of automationList) automations[a.id] = a;
      const map: Record<string, NodeInfo> = {};
      for (const n of nodes) map[n.id] = n;
      const entities: Record<string, Entity> = {};
      for (const e of entityList) entities[e.id] = e;
      const haveHistory = this.state.lastSeq > 0 && this.state.lastSeq <= serverSeq;
      const backlog = haveHistory ? await api.monitor({ since: this.state.lastSeq, limit: 0 }) : await api.monitor({ limit: 500 });
      const entries = mergeEntries(haveHistory ? this.state.entries : [], backlog.entries);
      this.set({
        nodes: map,
        links,
        health,
        entities,
        eventNames,
        automations,
        trains,
        consists,
        cars,
        formations,
        layout: trainsResponse.layout,
        firmware,
        clock,
        trackPlan,
        tracked,
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

  async refreshEventNames() {
    try {
      this.set({ eventNames: await api.eventNames() });
    } catch (err) {
      console.error("loading event names", err);
    }
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
