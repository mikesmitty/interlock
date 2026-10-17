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
};

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
