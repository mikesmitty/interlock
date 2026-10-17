// Names for event IDs, for the monitor and anywhere an event ID shows:
// the entity and role it belongs to, else its name in a node's
// configuration.
import { useMemo } from "react";
import { useBus } from "./store";

const ROLE_WORDS: Record<string, string> = {
  throw: "throw",
  close: "close",
  center: "center",
  thrown: "thrown",
  closed: "closed",
  occupied: "occupied",
  clear: "clear",
  on: "on",
  off: "off",
  press: "press",
  range: "reporter",
};

/** Decodes the low two bytes of a DCC detector event: who entered or left. */
export function detectorReport(suffix: number): string {
  const code = suffix & 0x3fff;
  const address =
    (code & 0x3f80) === 0x3800 ? `${code & 0x7f} (short)` : (code & 0x3f80) === 0x3900 ? `consist ${code & 0x7f}` : String(code);
  switch (suffix >> 14) {
    case 0:
      return `${address} left`;
    case 1:
      return `${address} entered facing west`;
    case 2:
      return `${address} entered facing east`;
    default:
      return `${address} entered`;
  }
}

export type EventLabeler = (id: string) => string | undefined;

/** Names event IDs: the entity and role they belong to, a reporter's
 * detector report, else the event's name in a node's configuration. */
export function useEventLabels(): EventLabeler {
  const { entities, eventNames } = useBus();
  return useMemo(() => {
    const labels = new Map<string, string>();
    const ranges: { prefix: string; name: string }[] = [];
    for (const n of eventNames) {
      labels.set(n.event.toUpperCase(), `${n.node_name}: ${n.path.slice(-2).join(" / ")}`);
    }
    for (const e of Object.values(entities)) {
      for (const [role, id] of Object.entries(e.events ?? {})) {
        if (role === "range") {
          ranges.push({ prefix: id.toUpperCase().slice(0, 17), name: e.name });
          continue;
        }
        labels.set(id.toUpperCase(), `${e.name} ${ROLE_WORDS[role] ?? role}`);
      }
    }
    return (id: string) => {
      const key = id.toUpperCase();
      const exact = labels.get(key);
      if (exact) return exact;
      for (const r of ranges) {
        if (key.startsWith(r.prefix)) {
          const suffix = parseInt(key.slice(18, 20) + key.slice(21, 23), 16);
          if (!Number.isNaN(suffix)) return `${r.name} reporter: ${detectorReport(suffix)}`;
        }
      }
      return undefined;
    };
  }, [entities, eventNames]);
}
