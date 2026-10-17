// Typed access to the Interlock API. Shapes mirror the Go structs.

export interface SNIP {
  Manufacturer: string;
  Model: string;
  HardwareVersion: string;
  SoftwareVersion: string;
  UserName: string;
  UserDescription: string;
}

export interface CdiStatus {
  state: "pending" | "reading" | "ready" | "error";
  read?: number;
  total?: number;
  error?: string;
  fetched_at?: string;
  cached?: boolean;
  truncated?: boolean;
  segments?: number;
}

export interface NodeInfo {
  id: string;
  alias: number;
  online: boolean;
  local: boolean;
  first_seen: string;
  last_seen: string;
  snip?: SNIP;
  protocols?: string[];
  protocol_bits?: number;
  error?: string;
  cdi?: CdiStatus;
  forgotten?: boolean;
}

// Configuration description, expanded so every value has an address.

export interface Relation {
  property: string;
  value: string;
}

export interface Hints {
  hideable?: boolean;
  hidden?: boolean;
  read_only?: boolean;
  slider?: { tick_spacing?: number; immediate?: boolean; show_value?: boolean };
  radio_button?: boolean;
  checkbox?: boolean;
}

export type CdiKind = "group" | "int" | "float" | "string" | "eventid" | "action" | "blob";

export interface CdiNode {
  kind: CdiKind;
  name: string;
  description?: string;
  space: number;
  address: number;
  size: number;
  key?: string;
  min?: string;
  max?: string;
  default?: string;
  signed?: boolean;
  map?: Relation[];
  hints?: Hints;
  formatting?: string;
  mode?: string;
  button_text?: string;
  dialog_text?: string;
  value?: string;
  replication?: number;
  replicas?: { name: string; items: CdiNode[] }[];
}

export interface CdiSegment {
  space: number;
  origin: number;
  size: number;
  name: string;
  description?: string;
  items: CdiNode[];
}

export interface Layout {
  identification?: {
    manufacturer: string;
    model: string;
    hardware_version: string;
    software_version: string;
  };
  segments: CdiSegment[];
}

export type Values = Record<string, string>;

export interface ReadResult {
  values: Values;
  errors?: Record<string, string>;
  bytes: number;
}

export interface WriteResult {
  written: string[];
  invalid?: Record<string, string>;
  failed?: string;
  failed_error?: string;
}

// Entities: the layout as things with a state and commands.

export type EntityKind = "turnout" | "block" | "reporter" | "sensor" | "switch" | "button";
export type EntitySource = "profile" | "guess" | "manual";

export interface Locomotive {
  address: { type: number; number: number };
  name: string;
  facing?: string;
  since: string;
}

export interface Entity {
  id: string;
  kind: EntityKind;
  name: string;
  default_name?: string;
  node?: string;
  node_name?: string;
  group?: string;
  source: EntitySource;
  profile?: string;
  suggested?: boolean;
  hidden?: boolean;
  available: boolean;
  state: string;
  commanded?: string;
  locomotives?: Locomotive[];
  last_event?: string;
  commands?: string[];
  events?: Record<string, string>;
  keys?: Record<string, string>;
  updated: string;
  removed?: boolean;
}

export interface RoleDef {
  name: string;
  required?: boolean;
  command?: boolean;
  state?: string;
  range?: boolean;
  label: string;
}

export interface KindDef {
  kind: EntityKind;
  label: string;
  plural: string;
  roles: RoleDef[];
  states: string[];
}

export interface EntityChange {
  name?: string;
  hidden?: boolean;
  confirmed?: boolean;
  events?: Record<string, string>;
}

export interface ManualEntity {
  id?: string;
  kind: EntityKind;
  name: string;
  events: Record<string, string>;
}

/** One entry of the named event table. */
export interface EventName {
  node: string;
  node_name: string;
  key: string;
  path: string[];
  name: string;
  event: string;
  produced?: boolean;
  consumed?: boolean;
}

export interface Profile {
  name: string;
  description?: string;
  match: { manufacturer?: string; model?: string; software_version?: string; any?: boolean };
  suggest?: boolean;
  source: string;
  entities: { kind: EntityKind; group?: string }[];
}

// Automations: when / only if / then documents and the traces of their runs.

export type AutomationMode = "single" | "restart" | "queued" | "parallel";
export type TriggerType = "state" | "event" | "locomotive" | "node" | "time" | "interval" | "start";
export type ConditionType = "state" | "time" | "trigger" | "and" | "or" | "not";
export type ActionType = "command" | "event" | "delay" | "wait" | "if" | "choose" | "repeat" | "stop" | "log";

export interface Trigger {
  id?: string;
  type: TriggerType;
  entity?: string;
  from?: string;
  to?: string;
  for?: string;
  event?: string;
  address?: number;
  address_type?: "long" | "short" | "consist";
  direction?: "entered" | "left";
  node?: string;
  at?: string;
  weekdays?: string[];
  every?: string;
}

export interface Condition {
  type: ConditionType;
  entity?: string;
  state?: string;
  after?: string;
  before?: string;
  weekdays?: string[];
  trigger?: string;
  conditions?: Condition[];
}

export interface Choice {
  name?: string;
  conditions: Condition[];
  actions: Action[];
}

export interface Action {
  type: ActionType;
  disabled?: boolean;
  entity?: string;
  command?: string;
  event?: string;
  for?: string;
  triggers?: Trigger[];
  timeout?: string;
  stop_on_timeout?: boolean;
  conditions?: Condition[];
  then?: Action[];
  else?: Action[];
  choices?: Choice[];
  default?: Action[];
  count?: number;
  while?: Condition[];
  until?: Condition[];
  actions?: Action[];
  reason?: string;
  error?: boolean;
  message?: string;
}

export interface Automation {
  id: string;
  name: string;
  description?: string;
  enabled: boolean;
  mode?: AutomationMode;
  max?: number;
  triggers: Trigger[];
  conditions?: Condition[];
  actions: Action[];
}

export type TraceResult = "running" | "finished" | "not_run" | "stopped" | "error" | "canceled" | "skipped";
export type StepResult = "running" | "ok" | "false" | "skipped" | "error" | "timeout" | "canceled";

export interface AutomationStatus extends Automation {
  running: number;
  queued?: number;
  last_triggered?: string;
  last_result?: TraceResult;
  last_trace?: string;
  problems?: string[];
  removed?: boolean;
}

export interface TriggerInfo {
  index: number;
  id?: string;
  type: string;
  description: string;
  entity?: string;
  entity_name?: string;
  from?: string;
  to?: string;
  event?: string;
  locomotive?: string;
  node?: string;
  node_name?: string;
  at: string;
}

export interface TraceStep {
  path: string;
  type: string;
  description: string;
  started: string;
  finished?: string;
  result: StepResult;
  detail?: string;
  error?: string;
}

export interface Trace {
  id: string;
  automation: string;
  name: string;
  started: string;
  finished?: string;
  result: TraceResult;
  error?: string;
  trigger: TriggerInfo;
  steps: TraceStep[];
}

export interface LinkStatus {
  name: string;
  kind: string;
  target: string;
  connected: boolean;
  since?: string;
  last_error?: string;
  frames_in: number;
  frames_out: number;
  dropped: number;
  parse_errors: number;
  reconnects: number;
}

export interface Endpoint {
  alias: number;
  id?: string;
}

export interface Decoded {
  type: string;
  mti?: number;
  source?: Endpoint;
  dest?: Endpoint;
  summary?: string;
  payload?: string;
}

export interface MonitorEntry {
  seq: number;
  time: string;
  dir: "in" | "out";
  link?: string;
  frame?: string;
  kind: string;
  decoded?: Decoded;
}

export interface Health {
  status: string;
  version: string;
  commit?: string;
  node_id: string;
  alias: number;
  node_state: string;
  uptime_seconds: number;
  nodes: number;
  links_total: number;
  links_connected: number;
}

export class ApiError extends Error {
  status: number;
  /** The decoded error body, when the server sent more than a message. */
  body?: Record<string, unknown>;

  constructor(status: number, message: string, body?: Record<string, unknown>) {
    super(message);
    this.status = status;
    this.body = body;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (init?.body) headers["Content-Type"] = "application/json";
  const res = await fetch(path, { ...init, headers });
  const text = await res.text();
  let data: unknown;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = undefined;
    }
  }
  if (!res.ok) {
    const body = (data ?? undefined) as Record<string, unknown> | undefined;
    const message = typeof body?.error === "string" ? body.error : `HTTP ${res.status}`;
    throw new ApiError(res.status, message, body);
  }
  return data as T;
}

async function requestText(path: string): Promise<string> {
  const res = await fetch(path);
  const text = await res.text();
  if (!res.ok) {
    let message = `HTTP ${res.status}`;
    try {
      message = (JSON.parse(text) as { error?: string }).error ?? message;
    } catch {
      // not JSON; keep the status
    }
    throw new ApiError(res.status, message);
  }
  return text;
}

const nodePath = (id: string) => `/api/v1/nodes/${encodeURIComponent(id)}`;
const automationPath = (id: string) => `/api/v1/automations/${encodeURIComponent(id)}`;

export const api = {
  health: () => request<Health>("/api/v1/health"),
  nodes: () => request<{ nodes: NodeInfo[] }>("/api/v1/nodes").then((r) => r.nodes),
  node: (id: string) => request<NodeInfo>(`/api/v1/nodes/${encodeURIComponent(id)}`),
  refreshNodes: () => request<{ status: string }>("/api/v1/nodes/refresh", { method: "POST" }),
  refreshNode: (id: string) =>
    request<{ status: string }>(`/api/v1/nodes/${encodeURIComponent(id)}/refresh`, { method: "POST" }),
  links: () => request<{ links: LinkStatus[] }>("/api/v1/links").then((r) => r.links),
  monitor: (params: { since?: number; limit?: number }) => {
    const q = new URLSearchParams();
    if (params.since !== undefined) q.set("since", String(params.since));
    if (params.limit !== undefined) q.set("limit", String(params.limit));
    return request<{ entries: MonitorEntry[]; last_seq: number }>(`/api/v1/monitor?${q.toString()}`);
  },
  sendFrame: (frame: string) =>
    request<{ frame: string }>("/api/v1/monitor/send", { method: "POST", body: JSON.stringify({ frame }) }),
  serialPorts: () => request<{ ports: string[] }>("/api/v1/serial-ports").then((r) => r.ports),
  forgetNode: (id: string) => request<void>(nodePath(id), { method: "DELETE" }),
  cdi: (id: string) => request<{ status: CdiStatus; layout: Layout }>(`${nodePath(id)}/cdi`),
  cdiXML: (id: string) => requestText(`${nodePath(id)}/cdi?format=xml`),
  refreshCdi: (id: string) => request<{ status: string }>(`${nodePath(id)}/cdi/refresh`, { method: "POST" }),
  configRead: (id: string, range: { space: number; address: number; size: number }) =>
    request<ReadResult>(`${nodePath(id)}/config/read`, { method: "POST", body: JSON.stringify(range) }),
  configWrite: (id: string, values: Values) =>
    request<WriteResult>(`${nodePath(id)}/config/write`, { method: "POST", body: JSON.stringify({ values }) }),
  reboot: (id: string) => request<{ status: string }>(`${nodePath(id)}/config/reboot`, { method: "POST" }),
  factoryReset: (id: string) =>
    request<{ status: string }>(`${nodePath(id)}/config/factory-reset`, { method: "POST", body: JSON.stringify({ confirm: id }) }),
  entities: () => request<{ entities: Entity[] }>("/api/v1/entities").then((r) => r.entities),
  entity: (id: string) => request<Entity>(`/api/v1/entities/${encodeURIComponent(id)}`),
  entityKinds: () => request<{ kinds: KindDef[] }>("/api/v1/entities/kinds").then((r) => r.kinds),
  command: (id: string, command: string) =>
    request<Entity>(`/api/v1/entities/${encodeURIComponent(id)}/command`, { method: "POST", body: JSON.stringify({ command }) }),
  updateEntity: (id: string, change: EntityChange) =>
    request<Entity>(`/api/v1/entities/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(change) }),
  createEntity: (manual: ManualEntity) => request<Entity>("/api/v1/entities", { method: "POST", body: JSON.stringify(manual) }),
  deleteEntity: (id: string) => request<void>(`/api/v1/entities/${encodeURIComponent(id)}`, { method: "DELETE" }),
  refreshEntities: () => request<{ status: string }>("/api/v1/entities/refresh", { method: "POST" }),
  eventNames: (node?: string) =>
    request<{ events: EventName[] }>(node ? `/api/v1/event-names?node=${encodeURIComponent(node)}` : "/api/v1/event-names").then((r) => r.events),
  refreshEventNames: (node: string) => request<{ status: string }>(`${nodePath(node)}/event-names/refresh`, { method: "POST" }),
  profiles: () => request<{ profiles: Profile[] }>("/api/v1/profiles").then((r) => r.profiles),
  automations: () => request<{ automations: AutomationStatus[] }>("/api/v1/automations").then((r) => r.automations),
  automation: (id: string) => request<AutomationStatus>(automationPath(id)),
  createAutomation: (doc: Automation) => request<AutomationStatus>("/api/v1/automations", { method: "POST", body: JSON.stringify(doc) }),
  updateAutomation: (id: string, doc: Automation) => request<AutomationStatus>(automationPath(id), { method: "PUT", body: JSON.stringify(doc) }),
  setAutomationEnabled: (id: string, enabled: boolean) =>
    request<AutomationStatus>(automationPath(id), { method: "PATCH", body: JSON.stringify({ enabled }) }),
  deleteAutomation: (id: string) => request<void>(automationPath(id), { method: "DELETE" }),
  automationYAML: (id: string) => requestText(`${automationPath(id)}/yaml`),
  runAutomation: (id: string, checkConditions = false) =>
    request<Trace>(`${automationPath(id)}/run`, { method: "POST", body: JSON.stringify({ check_conditions: checkConditions }) }),
  stopAutomation: (id: string) => request<{ status: string }>(`${automationPath(id)}/stop`, { method: "POST" }),
  traces: (id: string, limit?: number) =>
    request<{ traces: Trace[] }>(`${automationPath(id)}/traces${limit ? `?limit=${limit}` : ""}`).then((r) => r.traces),
  trace: (id: string, traceId: string) => request<Trace>(`${automationPath(id)}/traces/${encodeURIComponent(traceId)}`),
};

/** A short label for an event ID: the entity or named event it belongs to. */
export function eventLabel(id: string, names: Map<string, string>): string | undefined {
  return names.get(id.toUpperCase());
}

/** The friendliest name we have for a node. */
export function nodeName(n: NodeInfo): string {
  if (n.snip?.UserName) return n.snip.UserName;
  if (n.snip?.Model) return `${n.snip.Manufacturer} ${n.snip.Model}`.trim();
  return n.id;
}

/** Manufacturer and model, or an empty string. */
export function nodeModel(n: NodeInfo): string {
  if (!n.snip) return "";
  return `${n.snip.Manufacturer} ${n.snip.Model}`.trim();
}

export function aliasHex(alias: number): string {
  return "0x" + alias.toString(16).toUpperCase().padStart(3, "0");
}
