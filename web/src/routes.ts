// Routes as the Track page shows them: their names, which can be set
// from a block, and what a route set is doing.
import type { Route, TrackLeg } from "./api";

/** How a route is named: "East outer → Main St siding", with the
 * turnouts it throws when another way joins the same two blocks. */
export function routeName(r: Route, all: Route[], blockName: (id: string) => string, turnoutName: (junction: string) => string): string {
  const name = `${blockName(r.from.block)} → ${blockName(r.to.block)}`;
  const twins = all.filter((o) => o.id !== r.id && o.from.block === r.from.block && o.to.block === r.to.block);
  if (twins.length === 0) return name;
  const thrown = (r.turnouts ?? []).filter((t) => t.position === "thrown").map((t) => turnoutName(t.junction));
  if (thrown.length > 0) return `${name}, ${thrown.join(" and ")} thrown`;
  if (twins.every((o) => (o.turnouts ?? []).some((t) => t.position === "thrown"))) return `${name}, straight through`;
  return `${name} (${r.id})`;
}

/** The routes a train in a block can be given, by where they lead. */
export function routesFrom(block: string, all: Route[], blockName: (id: string) => string): Route[] {
  return all.filter((r) => r.from.block === block).sort((a, b) => blockName(a.to.block).localeCompare(blockName(b.to.block)) || a.id.localeCompare(b.id));
}

/** The routes set, the longest set first. */
export function setRoutes(all: Route[]): Route[] {
  return all.filter((r) => r.state).sort((a, b) => (a.since ?? "").localeCompare(b.since ?? "") || a.id.localeCompare(b.id));
}

/** The track a route still holds: what hasn't been released behind its train. */
export function heldLegs(r: Route): TrackLeg[] {
  return r.state ? r.legs.slice(r.released ?? 0) : [];
}

/** What a route set is doing, in words. */
export function routeStateText(r: Route, trainName: (id: string) => string, turnoutName: (junction: string) => string): string {
  const waiting = (r.waiting ?? []).map(turnoutName);
  switch (r.state) {
    case "occupied":
      return waiting.length > 0 ? `${trainName(r.train ?? "")} on it, but ${waiting.join(" and ")} moved` : `${trainName(r.train ?? "")} on it`;
    case "setting":
      return waiting.length > 0 ? `Waiting for ${waiting.join(" and ")}` : "Setting";
    case "set":
      return "Set";
  }
  return "Not set";
}
