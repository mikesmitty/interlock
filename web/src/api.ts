// Typed access to the Interlock API. Shapes mirror the Go structs.

import type {
  DecoderCandidate,
  DecoderDefinition,
  DecoderIdentity,
  DecoderIndex,
  DecoderSource,
} from "./decoder/types";

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

// The fast clock.

export interface ClockState {
  clock: string;
  running: boolean;
  rate: number;
  time: string;
  date: string;
  weekday: string;
  hour: number;
  minute: number;
  second: number;
  updated: string;
}

export interface ClockChange {
  running?: boolean;
  rate?: number;
  time?: string;
  date?: string;
}

// A firmware upgrade in progress or finished.

export type FirmwarePhase = "checking" | "freezing" | "writing" | "unfreezing" | "restarting" | "verifying" | "done" | "failed";

export interface FirmwareJob {
  id: string;
  node: string;
  node_name: string;
  file: string;
  size: number;
  written: number;
  phase: FirmwarePhase;
  message: string;
  from?: string;
  to?: string;
  started: string;
  updated: string;
  finished?: string;
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
  match: {
    manufacturer?: string;
    model?: string;
    software_version?: string;
    any?: boolean;
  };
  suggest?: boolean;
  source: string;
  entities: { kind: EntityKind; group?: string }[];
}

// Automations: when / only if / then documents and the traces of their runs.

export type AutomationMode = "single" | "restart" | "queued" | "parallel";
export type TriggerType = "state" | "event" | "locomotive" | "node" | "time" | "clock" | "interval" | "start";
export type ConditionType = "state" | "time" | "clock" | "trigger" | "and" | "or" | "not";
export type ActionType = "command" | "train" | "event" | "delay" | "wait" | "if" | "choose" | "repeat" | "stop" | "log";

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
  address?: number;
  address_type?: "long" | "short";
  speed?: number;
  direction?: Direction;
  function?: number;
  state?: "on" | "off";
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

// Trains: the roster, what the throttle set, and the layout's emergency state.

export type Direction = "forward" | "reverse";

export interface TrainEntry {
  id: string;
  name: string;
  address: number;
  long: boolean;
  functions?: Record<string, string>;
  momentary?: number[];
  road_name?: string;
  road_number?: string;
  manufacturer?: string;
  model?: string;
  owner?: string;
  comment?: string;
  /** The decoders the locomotive has, fitted or not. */
  decoders?: RosterDecoder[];
  /** The ID of the decoder fitted now. */
  installed?: string;
}

/** What a decoder reports about itself: CV 8, CV 7 and a product ID. */
export interface RosterIdentity {
  manufacturer: number;
  version: number;
  product_id?: number;
}

/** One of a locomotive's decoders. */
export interface RosterDecoder {
  id: string;
  name?: string;
  file?: string;
  family?: string;
  model?: string;
  identity?: RosterIdentity;
  /** What a decoder that is not fitted brings back when it is. */
  address?: number;
  long?: boolean;
  functions?: Record<string, string>;
  momentary?: number[];
}

/**
 * A decoder's saved CV values by the names its definition uses, those
 * imported from another roster and not read since, and the changes not
 * written to it yet.
 */
export interface SavedCVs {
  cvs: Record<string, number>;
  imported?: Record<string, number>;
  updated?: string;
  changes?: Record<string, { value: number; bits: number }>;
  /** The work on the decoder going on now. */
  session?: DecoderSession;
  /** Copies of its values kept under a name, oldest first. */
  profiles?: DecoderProfile[];
}

/** A copy of a decoder's values kept under a name, to load again as changes. */
export interface DecoderProfile {
  name: string;
  kept: string;
  cvs: Record<string, number>;
}

/**
 * A spell of work on a decoder: the CVs read from it and written to it,
 * and, for work on the programming track, how the check that it is this
 * decoder went.
 */
export interface DecoderSession {
  started: string;
  last: string;
  check?: "matched" | "accepted";
  read: string[];
  written: string[];
}

/** A roster decoder that could be the one on the programming track. */
export interface DecoderMatch {
  train: string;
  decoder?: string;
  installed: boolean;
  identity: boolean;
  /** Identify checked it and began its session. */
  checked?: boolean;
}

/** Something the decoder on the programming track and the roster disagree on. */
export interface CheckDifference {
  what: "manufacturer" | "product" | "address" | "cv";
  cv?: string;
  /** The roster's value and the decoder's: IDs, addresses or CV values. */
  roster: number;
  track: number;
  roster_long?: boolean;
  track_long?: boolean;
  /** The roster's value was imported and has not been read since. */
  imported?: boolean;
  roster_name?: string;
  track_name?: string;
}

/** Whether the decoder on the programming track is a roster decoder. */
export interface DecoderCheck {
  match: boolean;
  differences: CheckDifference[];
  /** The CVs read to tell it from other roster decoders. */
  told?: string[];
  /** Roster decoders of the same model at the same address that nothing saved tells it from. */
  alike: DecoderMatch[];
  /** Roster decoders it could be instead. */
  others: DecoderMatch[];
  /** The values read for the check, by CV. */
  read: Record<string, number>;
  identity: DecoderIdentity;
  manufacturer?: string;
  model?: string;
  address: { address: number; long: boolean };
  /** The session begun when it matched. */
  session?: DecoderSession;
}

export type ImportAction = "new" | "decoder" | "skip";

export interface ImportStep {
  key: string;
  action: ImportAction;
  into?: string;
  install?: boolean;
  name?: string;
  decoder_name?: string;
  note?: string;
}

/** A locomotive found in a JMRI roster. */
export interface ImportItem {
  key: string;
  entry: TrainEntry;
  decoder?: RosterDecoder;
  updated?: string;
  cv_count: number;
  /** Whether the decoder definitions have its decoder. */
  definition: boolean;
}

export interface ImportPreview {
  locomotives: ImportItem[];
  plan: ImportStep[];
  problems: string[];
}

export interface ImportResult {
  result: { trains: Train[]; decoders: number; cvs: number; skipped: number };
  problems: string[];
  error?: string;
}

/** What Identify found on the programming track. */
export interface IdentifyResult {
  identity: DecoderIdentity;
  manufacturer?: string;
  candidates: DecoderCandidate[];
  note?: string;
  address?: { address: number; long: boolean };
  roster: DecoderMatch[];
}

export interface Train extends TrainEntry {
  roster: boolean;
  node?: string;
  online: boolean;
  controlled: boolean;
  controller?: string;
  speed: number;
  direction: Direction;
  estop: boolean;
  on: number[];
  known: boolean;
  updated: string;
  removed?: boolean;
}

export interface FoundTrain {
  id: string;
  name: string;
  node: string;
  address: number;
  long: boolean;
  roster: boolean;
}

export interface LayoutState {
  power_off: boolean;
  emergency_stop: boolean;
  updated?: string;
}

export interface ProgrammingTrack {
  node?: string;
  name?: string;
  online: boolean;
  checked?: string;
}

export interface CVOp {
  id: number;
  kind: "read" | "write" | "pom";
  train?: string;
  train_name?: string;
  cv: number;
  value: number;
  ok: boolean;
  error?: string;
  started: string;
  finished: string;
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
  /** The server's operating system, as Go names it: linux, darwin, windows. */
  os: string;
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

/** Sends a file as a multipart form; the server answers with JSON. */
async function upload<T>(path: string, field: string, file: File): Promise<T> {
  const form = new FormData();
  form.append(field, file, file.name);
  return sendForm<T>(path, form);
}

async function sendForm<T>(path: string, form: FormData): Promise<T> {
  const res = await fetch(path, { method: "POST", body: form, headers: { Accept: "application/json" } });
  const text = await res.text();
  let data: unknown;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = undefined;
  }
  if (!res.ok) {
    const body = data as { error?: string } | undefined;
    throw new ApiError(res.status, body?.error ?? `HTTP ${res.status}`);
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
const trainPath = (id: string) => `/api/v1/trains/${encodeURIComponent(id)}`;

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
    request<{ frame: string }>("/api/v1/monitor/send", {
      method: "POST",
      body: JSON.stringify({ frame }),
    }),
  serialPorts: () => request<{ ports: string[] }>("/api/v1/serial-ports").then((r) => r.ports),
  forgetNode: (id: string) => request<void>(nodePath(id), { method: "DELETE" }),
  restore: (file: File) => upload<{ restarting: boolean }>("/api/v1/restore", "file", file),
  restart: () => request<{ restarting: boolean }>("/api/v1/restart", { method: "POST" }),
  clock: () => request<ClockState>("/api/v1/clock"),
  setClock: (change: ClockChange) => request<ClockState>("/api/v1/clock", { method: "POST", body: JSON.stringify(change) }),
  firmwareJobs: () => request<{ jobs: FirmwareJob[] }>("/api/v1/firmware").then((r) => r.jobs),
  firmwareJob: (id: string) => request<FirmwareJob>(`${nodePath(id)}/firmware`),
  installFirmware: (id: string, file: File) => upload<FirmwareJob>(`${nodePath(id)}/firmware`, "file", file),
  cancelFirmware: (id: string) => request<FirmwareJob>(`${nodePath(id)}/firmware`, { method: "DELETE" }),
  cdi: (id: string) => request<{ status: CdiStatus; layout: Layout }>(`${nodePath(id)}/cdi`),
  cdiXML: (id: string) => requestText(`${nodePath(id)}/cdi?format=xml`),
  refreshCdi: (id: string) =>
    request<{ status: string }>(`${nodePath(id)}/cdi/refresh`, {
      method: "POST",
    }),
  configRead: (id: string, range: { space: number; address: number; size: number }) =>
    request<ReadResult>(`${nodePath(id)}/config/read`, {
      method: "POST",
      body: JSON.stringify(range),
    }),
  configWrite: (id: string, values: Values) =>
    request<WriteResult>(`${nodePath(id)}/config/write`, {
      method: "POST",
      body: JSON.stringify({ values }),
    }),
  reboot: (id: string) =>
    request<{ status: string }>(`${nodePath(id)}/config/reboot`, {
      method: "POST",
    }),
  factoryReset: (id: string) =>
    request<{ status: string }>(`${nodePath(id)}/config/factory-reset`, {
      method: "POST",
      body: JSON.stringify({ confirm: id }),
    }),
  entities: () => request<{ entities: Entity[] }>("/api/v1/entities").then((r) => r.entities),
  entity: (id: string) => request<Entity>(`/api/v1/entities/${encodeURIComponent(id)}`),
  entityKinds: () => request<{ kinds: KindDef[] }>("/api/v1/entities/kinds").then((r) => r.kinds),
  command: (id: string, command: string) =>
    request<Entity>(`/api/v1/entities/${encodeURIComponent(id)}/command`, {
      method: "POST",
      body: JSON.stringify({ command }),
    }),
  updateEntity: (id: string, change: EntityChange) =>
    request<Entity>(`/api/v1/entities/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(change),
    }),
  createEntity: (manual: ManualEntity) =>
    request<Entity>("/api/v1/entities", {
      method: "POST",
      body: JSON.stringify(manual),
    }),
  deleteEntity: (id: string) =>
    request<void>(`/api/v1/entities/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  refreshEntities: () => request<{ status: string }>("/api/v1/entities/refresh", { method: "POST" }),
  eventNames: (node?: string) =>
    request<{ events: EventName[] }>(
      node ? `/api/v1/event-names?node=${encodeURIComponent(node)}` : "/api/v1/event-names",
    ).then((r) => r.events),
  refreshEventNames: (node: string) =>
    request<{ status: string }>(`${nodePath(node)}/event-names/refresh`, {
      method: "POST",
    }),
  profiles: () => request<{ profiles: Profile[] }>("/api/v1/profiles").then((r) => r.profiles),
  automations: () => request<{ automations: AutomationStatus[] }>("/api/v1/automations").then((r) => r.automations),
  automation: (id: string) => request<AutomationStatus>(automationPath(id)),
  createAutomation: (doc: Automation) =>
    request<AutomationStatus>("/api/v1/automations", {
      method: "POST",
      body: JSON.stringify(doc),
    }),
  updateAutomation: (id: string, doc: Automation) =>
    request<AutomationStatus>(automationPath(id), {
      method: "PUT",
      body: JSON.stringify(doc),
    }),
  setAutomationEnabled: (id: string, enabled: boolean) =>
    request<AutomationStatus>(automationPath(id), {
      method: "PATCH",
      body: JSON.stringify({ enabled }),
    }),
  deleteAutomation: (id: string) => request<void>(automationPath(id), { method: "DELETE" }),
  automationYAML: (id: string) => requestText(`${automationPath(id)}/yaml`),
  runAutomation: (id: string, checkConditions = false) =>
    request<Trace>(`${automationPath(id)}/run`, {
      method: "POST",
      body: JSON.stringify({ check_conditions: checkConditions }),
    }),
  stopAutomation: (id: string) =>
    request<{ status: string }>(`${automationPath(id)}/stop`, {
      method: "POST",
    }),
  traces: (id: string, limit?: number) =>
    request<{ traces: Trace[] }>(`${automationPath(id)}/traces${limit ? `?limit=${limit}` : ""}`).then((r) => r.traces),
  trace: (id: string, traceId: string) => request<Trace>(`${automationPath(id)}/traces/${encodeURIComponent(traceId)}`),
  trains: () => request<{ trains: Train[]; layout: LayoutState }>("/api/v1/trains"),
  train: (id: string) => request<Train>(trainPath(id)),
  createTrain: (entry: Partial<TrainEntry>) =>
    request<Train>("/api/v1/trains", {
      method: "POST",
      body: JSON.stringify(entry),
    }),
  updateTrain: (id: string, entry: Partial<TrainEntry>) =>
    request<Train>(trainPath(id), {
      method: "PATCH",
      body: JSON.stringify(entry),
    }),
  deleteTrain: (id: string) => request<void>(trainPath(id), { method: "DELETE" }),
  searchTrains: (query: string) =>
    request<{ trains: FoundTrain[] }>("/api/v1/trains/search", {
      method: "POST",
      body: JSON.stringify({ query }),
    }).then((r) => r.trains),
  acquireTrain: (id: string) => request<Train>(`${trainPath(id)}/acquire`, { method: "POST" }),
  releaseTrain: (id: string) => request<Train>(`${trainPath(id)}/release`, { method: "POST" }),
  refreshTrain: (id: string) => request<Train>(`${trainPath(id)}/refresh`, { method: "POST" }),
  trainSpeed: (id: string, speed: number, direction?: Direction) =>
    request<Train>(`${trainPath(id)}/speed`, {
      method: "POST",
      body: JSON.stringify({ speed, direction }),
    }),
  trainEStop: (id: string) => request<Train>(`${trainPath(id)}/estop`, { method: "POST" }),
  trainFunction: (id: string, fn: number, on: boolean) =>
    request<Train>(`${trainPath(id)}/function`, {
      method: "POST",
      body: JSON.stringify({ function: fn, on }),
    }),
  addDecoder: (id: string, d: Partial<RosterDecoder> & { install?: boolean }) =>
    request<{ train: Train; decoder: RosterDecoder }>(`${trainPath(id)}/decoders`, {
      method: "POST",
      body: JSON.stringify(d),
    }),
  updateDecoder: (id: string, decoder: string, d: Partial<RosterDecoder>) =>
    request<Train>(`${trainPath(id)}/decoders/${encodeURIComponent(decoder)}`, {
      method: "PATCH",
      body: JSON.stringify(d),
    }),
  removeDecoder: (id: string, decoder: string) =>
    request<Train>(`${trainPath(id)}/decoders/${encodeURIComponent(decoder)}`, { method: "DELETE" }),
  installDecoder: (id: string, decoder: string) =>
    request<Train>(`${trainPath(id)}/install`, {
      method: "POST",
      body: JSON.stringify({ decoder }),
    }),
  savedCVs: (id: string, decoder: string) =>
    request<SavedCVs>(`${trainPath(id)}/decoders/${encodeURIComponent(decoder)}/cvs`),
  saveCVs: (id: string, decoder: string, cvs: Record<string, number>, replace = false) =>
    request<SavedCVs>(`${trainPath(id)}/decoders/${encodeURIComponent(decoder)}/cvs`, {
      method: replace ? "PUT" : "PATCH",
      body: JSON.stringify({ cvs }),
    }),
  /** Checks that the decoder on the programming track is this one, beginning its session if it is. */
  checkDecoder: (id: string, decoder: string) =>
    request<DecoderCheck>(`${trainPath(id)}/decoders/${encodeURIComponent(decoder)}/check`, { method: "POST" }),
  /** Keeps a copy of a decoder's values; a name kept already is replaced only with replace. */
  keepProfile: (id: string, decoder: string, name: string, replace = false) =>
    request<DecoderProfile>(`${trainPath(id)}/decoders/${encodeURIComponent(decoder)}/profiles`, {
      method: "POST",
      body: JSON.stringify({ name, replace }),
    }),
  deleteProfile: (id: string, decoder: string, name: string) =>
    request<void>(`${trainPath(id)}/decoders/${encodeURIComponent(decoder)}/profiles/${encodeURIComponent(name)}`, { method: "DELETE" }),
  /** Begins the session on a decoder though its check did not match. */
  startSession: (id: string, decoder: string) =>
    request<DecoderSession>(`${trainPath(id)}/decoders/${encodeURIComponent(decoder)}/session`, { method: "POST" }),
  endSession: (id: string, decoder: string) =>
    request<void>(`${trainPath(id)}/decoders/${encodeURIComponent(decoder)}/session`, { method: "DELETE" }),
  /** Keeps the changes not written yet; keepalive lets it finish as the page closes. */
  saveChanges: (id: string, decoder: string, changes: Record<string, { value: number; bits: number }>, keepalive = false) =>
    request<void>(`${trainPath(id)}/decoders/${encodeURIComponent(decoder)}/changes`, {
      method: "PUT",
      body: JSON.stringify({ changes }),
      keepalive,
    }),
  importRoster: (files: File[]) => {
    const form = new FormData();
    for (const f of files) form.append("file", f, f.name);
    return sendForm<ImportPreview>("/api/v1/trains/import", form);
  },
  applyImport: (files: File[], plan: ImportStep[]) => {
    const form = new FormData();
    for (const f of files) form.append("file", f, f.name);
    form.append("plan", JSON.stringify(plan));
    return sendForm<ImportResult>("/api/v1/trains/import", form);
  },
  writePOM: (id: string, cv: number, value: number) =>
    request<CVOp>(`${trainPath(id)}/cv`, {
      method: "POST",
      body: JSON.stringify({ cv, value }),
    }),
  layout: () => request<LayoutState>("/api/v1/trains/layout"),
  setLayout: (change: { power?: boolean; emergency_stop?: boolean }) =>
    request<LayoutState>("/api/v1/trains/layout", {
      method: "POST",
      body: JSON.stringify(change),
    }),
  programming: () => request<{ track: ProgrammingTrack; history: CVOp[] }>("/api/v1/programming"),
  findProgrammingTrack: () => request<ProgrammingTrack>("/api/v1/programming/find", { method: "POST" }),
  readCV: (cv: number) =>
    request<CVOp>("/api/v1/programming/read", {
      method: "POST",
      body: JSON.stringify({ cv }),
    }),
  writeCV: (cv: number, value: number) =>
    request<CVOp>("/api/v1/programming/write", {
      method: "POST",
      body: JSON.stringify({ cv, value }),
    }),
  decoderSource: () => request<DecoderSource>("/api/v1/decoders"),
  decoderIndex: () => request<DecoderIndex>("/api/v1/decoders/index"),
  decoderDefinition: (file: string, model: string) =>
    request<DecoderDefinition>(`/api/v1/decoders/definition?${new URLSearchParams({ file, model }).toString()}`),
  identifyDecoder: () => request<IdentifyResult>("/api/v1/programming/identify", { method: "POST" }),
  decoderCV: (body: {
    file: string;
    model: string;
    cv: string;
    write?: boolean;
    value?: number;
    train?: string;
    locomotive?: string;
    decoder?: string;
  }) =>
    request<{ cv: string; value: number; ok: boolean; error?: string }>("/api/v1/programming/decoder-cv", {
      method: "POST",
      body: JSON.stringify(body),
    }),
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
