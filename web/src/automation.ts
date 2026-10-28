// Words for automations: what a trigger, condition or action means, what a
// result is called, which lamp it lights, and blank rows for the editor.
import {
  nodeName,
  type Action,
  type AutomationStatus,
  type Condition,
  type Entity,
  type EntityKind,
  type NodeInfo,
  type StepResult,
  type TraceResult,
  type Train,
  type Trigger,
} from "./api";
import type { LampColor } from "./components/Lamp";

export const KIND_STATES: Record<EntityKind, string[]> = {
  turnout: ["closed", "thrown", "moving", "unknown"],
  block: ["occupied", "clear", "unknown"],
  reporter: ["occupied", "clear", "unknown"],
  sensor: ["on", "off", "unknown"],
  switch: ["on", "off", "unknown"],
  button: [],
};

export const WEEKDAYS: [string, string][] = [
  ["mon", "Mon"],
  ["tue", "Tue"],
  ["wed", "Wed"],
  ["thu", "Thu"],
  ["fri", "Fri"],
  ["sat", "Sat"],
  ["sun", "Sun"],
];

export const MODES: {
  value: NonNullable<AutomationStatus["mode"]>;
  label: string;
  hint: string;
}[] = [
  {
    value: "single",
    label: "Single",
    hint: "Ignore new triggers while it is running.",
  },
  {
    value: "restart",
    label: "Restart",
    hint: "Stop the run in progress and start over.",
  },
  {
    value: "queued",
    label: "Queued",
    hint: "Run again after the current run finishes.",
  },
  {
    value: "parallel",
    label: "Parallel",
    hint: "Run each trigger alongside the others.",
  },
];

export interface Names {
  entity: (id: string) => string;
  node: (id: string) => string;
  train: (address: number, long: boolean) => string;
}

/** The roster name for an address (every name, when locomotives share it), else "locomotive 1234". */
export function trainName(trains: Record<string, Train>, address: number, long: boolean): string {
  const isLong = long || address > 127;
  const names = Object.values(trains)
    .filter((t) => t.roster && t.address === address && (t.long || t.address > 127) === isLong)
    .map((t) => t.name)
    .sort();
  if (names.length > 0) return names.join(" / ");
  if (address > 127) return `locomotive ${address}`;
  return `locomotive ${address} (${long ? "long" : "short"})`;
}

export function namesFrom(entities: Record<string, Entity>, nodes: Record<string, NodeInfo>, trains: Record<string, Train> = {}): Names {
  return {
    entity: (id) => entities[id]?.name ?? id,
    node: (id) => (nodes[id] ? nodeName(nodes[id]) : id),
    train: (address, long) => trainName(trains, address, long),
  };
}

function onDays(days?: string[]): string {
  if (!days || days.length === 0) return "";
  const labels = days.map((d) => WEEKDAYS.find(([k]) => k === d.toLowerCase())?.[1] ?? d);
  return " on " + labels.join(", ");
}

export function locomotiveName(address: number, type?: string): string {
  if (type === "short") return `${address} (short)`;
  if (type === "consist") return `consist ${address}`;
  return String(address);
}

/** "When Track 2 becomes occupied" */
export function describeTrigger(t: Trigger, n: Names): string {
  switch (t.type) {
    case "state": {
      let s = `When ${n.entity(t.entity ?? "")}`;
      if (t.from && t.to) s += ` changes from ${t.from} to ${t.to}`;
      else if (t.to) s += ` becomes ${t.to}`;
      else if (t.from) s += ` leaves ${t.from}`;
      else s += " changes state";
      if (t.for) s += ` for ${t.for}`;
      return s;
    }
    case "event":
      return `When event ${t.event ?? "…"} is received`;
    case "locomotive": {
      const who = t.address ? `locomotive ${locomotiveName(t.address, t.address_type)}` : "a locomotive";
      const verb = t.direction === "entered" ? "enters" : t.direction === "left" ? "leaves" : "enters or leaves";
      return `When ${who} ${verb} ${n.entity(t.entity ?? "")}`;
    }
    case "node": {
      const who = t.node ? n.node(t.node) : "any node";
      const verb = t.to === "online" ? "comes online" : t.to === "offline" ? "goes offline" : "comes online or goes offline";
      return `When ${who} ${verb}`;
    }
    case "time":
      return `At ${t.at ?? "…"}${onDays(t.weekdays)}`;
    case "clock":
      return `When the fast clock reads ${t.at ?? "…"}${onDays(t.weekdays)}`;
    case "interval":
      return `Every ${t.every ?? "…"}`;
    case "start":
      return "When Interlock starts";
  }
}

/** "Turnout 1 is thrown" */
export function describeCondition(c: Condition, n: Names): string {
  switch (c.type) {
    case "state":
      return `${n.entity(c.entity ?? "")} is ${c.state ?? "…"}`;
    case "time":
    case "clock": {
      let s = "Only";
      if (c.after && c.before) s = `Between ${c.after} and ${c.before}`;
      else if (c.after) s = `After ${c.after}`;
      else if (c.before) s = `Before ${c.before}`;
      if (c.type === "clock") s += " on the fast clock";
      return s + onDays(c.weekdays);
    }
    case "trigger":
      return `Started by trigger ${c.trigger ?? "…"}`;
    case "and":
      return `All of ${count(c.conditions?.length ?? 0, "condition")}`;
    case "or":
      return `Any of ${count(c.conditions?.length ?? 0, "condition")}`;
    case "not":
      return c.conditions?.[0] ? `Not: ${describeCondition(c.conditions[0], n)}` : "Not";
  }
}

/** "Close Turnout 1" */
export function describeAction(a: Action, n: Names): string {
  switch (a.type) {
    case "command": {
      const cmd = a.command ? a.command[0].toUpperCase() + a.command.slice(1) : "Command";
      return `${cmd} ${n.entity(a.entity ?? "")}`;
    }
    case "train": {
      const address = a.address ?? 0;
      const long = a.address_type === "long" || address > 127;
      const who = address ? n.train(address, long) : "a locomotive";
      switch (a.command) {
        case "speed":
          return `Set ${who} to speed ${a.speed ?? 0}${a.direction ? ` ${a.direction}` : ""}`;
        case "stop":
          return `Stop ${who}`;
        case "estop":
          return `Emergency stop ${who}`;
        case "function":
          return `Turn ${a.state === "off" ? "off" : "on"} F${a.function ?? 0} of ${who}`;
        default:
          return `Drive ${who}`;
      }
    }
    case "event":
      return `Send event ${a.event ?? "…"}`;
    case "delay":
      return `Wait ${a.for ?? "…"}`;
    case "wait": {
      const parts = (a.triggers ?? []).map((t) => describeTrigger(t, n).replace(/^When /, ""));
      return `Wait until ${parts.join(", or ") || "…"}${a.timeout ? ` (up to ${a.timeout})` : ""}`;
    }
    case "if":
      return `If ${(a.conditions ?? []).map((c) => describeCondition(c, n)).join(" and ") || "…"}`;
    case "choose":
      return `Choose between ${count(a.choices?.length ?? 0, "option")}`;
    case "repeat":
      if (a.count) return `Repeat ${count(a.count, "time")}`;
      if (a.while?.length) return `Repeat while ${describeCondition(a.while[0], n)}`;
      if (a.until?.length) return `Repeat until ${describeCondition(a.until[0], n)}`;
      return "Repeat";
    case "stop":
      return a.reason ? `Stop: ${a.reason}` : "Stop";
    case "log":
      return `Log “${a.message ?? ""}”`;
  }
}

function count(n: number, one: string): string {
  return `${n} ${n === 1 ? one : one + "s"}`;
}

/** The lead sentence for an automation: its triggers, joined. */
export function summarize(a: AutomationStatus, n: Names): string {
  const parts = a.triggers.map((t) => describeTrigger(t, n));
  if (parts.length === 0) return "";
  return parts
    .map((p, i) =>
      i === 0
        ? p
        : p
            .replace(/^When /, "when ")
            .replace(/^At /, "at ")
            .replace(/^Every /, "every "),
    )
    .join(", or ");
}

export interface LampWord {
  color: LampColor;
  pulse?: boolean;
  word: string;
}

export function automationLamp(a: AutomationStatus): LampWord {
  if (a.running > 0)
    return {
      color: "amber",
      pulse: true,
      word: a.running > 1 ? `Running ×${a.running}` : "Running",
    };
  if (!a.enabled) return { color: "off", word: "Off" };
  if (a.problems?.length) return { color: "amber", word: "Needs attention" };
  if (a.last_result === "error") return { color: "red", word: "Last run failed" };
  return { color: "green", word: "On" };
}

export function resultLamp(r: TraceResult): LampWord {
  switch (r) {
    case "running":
      return { color: "amber", pulse: true, word: "Running" };
    case "finished":
      return { color: "green", word: "Finished" };
    case "not_run":
      return { color: "off", word: "Conditions not met" };
    case "stopped":
      return { color: "amber", word: "Stopped" };
    case "error":
      return { color: "red", word: "Failed" };
    case "canceled":
      return { color: "off", word: "Canceled" };
    case "skipped":
      return { color: "off", word: "Skipped" };
  }
}

export function stepLamp(r: StepResult): LampWord {
  switch (r) {
    case "running":
      return { color: "amber", pulse: true, word: "running" };
    case "ok":
      return { color: "green", word: "done" };
    case "false":
      return { color: "off", word: "not met" };
    case "skipped":
      return { color: "off", word: "skipped" };
    case "error":
      return { color: "red", word: "failed" };
    case "timeout":
      return { color: "amber", word: "timed out" };
    case "canceled":
      return { color: "off", word: "canceled" };
  }
}

export function newTrigger(type: Trigger["type"] = "state"): Trigger {
  switch (type) {
    case "locomotive":
      return { type, direction: "entered" };
    case "node":
      return { type, to: "online" };
    case "time":
    case "clock":
      return { type, at: "07:00" };
    case "interval":
      return { type, every: "10m" };
    default:
      return { type };
  }
}

export function newCondition(type: Condition["type"] = "state"): Condition {
  switch (type) {
    case "and":
    case "or":
      return { type, conditions: [newCondition("state")] };
    case "not":
      return { type, conditions: [newCondition("state")] };
    case "time":
    case "clock":
      return { type, after: "06:00", before: "22:00" };
    default:
      return { type };
  }
}

export function newAction(type: Action["type"] = "command"): Action {
  switch (type) {
    case "train":
      return { type, command: "speed", speed: 0, direction: "forward" };
    case "delay":
      return { type, for: "3s" };
    case "wait":
      return { type, triggers: [newTrigger("state")], timeout: "30s" };
    case "if":
      return {
        type,
        conditions: [newCondition("state")],
        then: [newAction("command")],
      };
    case "choose":
      return {
        type,
        choices: [
          {
            conditions: [newCondition("state")],
            actions: [newAction("command")],
          },
        ],
      };
    case "repeat":
      return { type, count: 2, actions: [newAction("command")] };
    case "log":
      return { type, message: "" };
    default:
      return { type };
  }
}

/** Drops empty fields so the server sees a tidy document. */
export function tidy<T>(value: T): T {
  if (Array.isArray(value)) return value.map((v) => tidy(v)) as T;
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      if (v === "" || v === undefined || v === null) continue;
      if (Array.isArray(v) && v.length === 0 && k !== "triggers" && k !== "actions") continue;
      out[k] = tidy(v);
    }
    return out as T;
  }
  return value;
}

/** "triggers[0].entity" reads as "Trigger 1, entity". */
export function pathLabel(path: string): string {
  if (!path) return "";
  const words: Record<string, string> = {
    triggers: "Trigger",
    conditions: "Condition",
    actions: "Action",
    then: "Then",
    else: "Else",
    choices: "Option",
    default: "Default",
    while: "While",
    until: "Until",
  };
  const parts = path.split(".").map((seg) => {
    const m = /^(\w+)\[(\d+)\]$/.exec(seg);
    if (m) return `${words[m[1]] ?? m[1]} ${Number(m[2]) + 1}`;
    return words[seg] ?? seg;
  });
  return parts.join(" › ");
}
