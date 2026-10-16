// Typed access to the Interlock API. Shapes mirror the Go structs.

export interface SNIP {
  Manufacturer: string;
  Model: string;
  HardwareVersion: string;
  SoftwareVersion: string;
  UserName: string;
  UserDescription: string;
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

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
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
    const message = (data as { error?: string } | undefined)?.error ?? `HTTP ${res.status}`;
    throw new ApiError(res.status, message);
  }
  return data as T;
}

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
