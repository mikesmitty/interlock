import type { LayoutState, Train } from "./api.ts";

type Held = Pick<Train, "speed" | "direction" | "estop">;
type Layout = Pick<LayoutState, "emergency_stop">;

/**
 * The speed a train goes back to when the layout's emergency stop ends, or
 * 0 when it stays put. The hold keeps each train's speed; the train's own
 * emergency stop sets it to 0.
 */
export function resumesAt(t: Held, layout: Layout): number {
  return layout.emergency_stop && !t.estop ? t.speed : 0;
}

/** A train's speed in a few characters, saying when the layout's emergency stop holds it. */
export function speedText(t: Held, layout: Layout): string {
  if (t.estop) return "stop!";
  const speed = t.speed ? `${Math.round(t.speed)} ${t.direction === "reverse" ? "rev" : "fwd"}` : "0";
  return layout.emergency_stop ? `held ${speed}` : speed;
}

/**
 * Whether releasing a train would leave it to start again on Resume with
 * no throttle driving it. A consist member's speed is its lead's, so
 * releasing it changes nothing.
 */
export function releaseLeavesItSet(t: Held, layout: Layout, follower: boolean): boolean {
  return !follower && resumesAt(t, layout) > 0;
}
