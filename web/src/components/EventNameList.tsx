import type { EventName } from "../api";

/** A datalist of every named event, for inputs with list="event-names". */
export function EventNameList({ names }: { names: EventName[] }) {
  return (
    <datalist id="event-names">
      {names.map((n) => (
        <option key={`${n.node}-${n.key}`} value={n.event}>
          {n.node_name} / {n.path.slice(1).join(" / ")}
        </option>
      ))}
    </datalist>
  );
}
