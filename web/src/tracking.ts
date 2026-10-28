// Train tracking as the pages show it: roster names for what detectors
// report, and where to draw a tracked train.
import type { Locomotive, TrackedTrain, Train, Vec } from "./api";

/** Detector address types, as the server sends them. */
const LONG = 0;
const SHORT = 1;

/** How a locomotive a detector reports is called: the roster's names for
 * its address, else how the address reads. */
export function locomotiveName(loco: Locomotive, trains: Record<string, Train>): string {
  const { type, number } = loco.address;
  if (type !== LONG && type !== SHORT) return loco.name;
  const names = Object.values(trains)
    .filter((t) => t.roster && t.address === number && t.long === (type === LONG))
    .map((t) => t.name);
  return names.length > 0 ? [...new Set(names)].sort().join(" / ") : loco.name;
}

const sub = (a: Vec, b: Vec): Vec => ({ x: a.x - b.x, y: a.y - b.y });
const len = (v: Vec) => Math.hypot(v.x, v.y);

/** The point halfway along a line, by length. */
export function lineMiddle(line: Vec[]): Vec | undefined {
  if (line.length === 0) return undefined;
  let total = 0;
  for (let i = 1; i < line.length; i++) total += len(sub(line[i], line[i - 1]));
  let left = total / 2;
  for (let i = 1; i < line.length; i++) {
    const step = len(sub(line[i], line[i - 1]));
    if (step >= left && step > 0) {
      const f = left / step;
      return { x: line[i - 1].x + (line[i].x - line[i - 1].x) * f, y: line[i - 1].y + (line[i].y - line[i - 1].y) * f };
    }
    left -= step;
  }
  return line[line.length - 1];
}

/** A triangle pointing the way a train goes, at its leading end: the
 * line's last point going forward, its first in reverse. size is the
 * triangle's length in plan units. */
export function leadArrow(t: TrackedTrain, size: number): Vec[] | undefined {
  const line = t.line ?? [];
  if (line.length === 0) return undefined;
  const points = t.direction === "reverse" ? [...line].reverse() : line;
  const tip = points[points.length - 1];
  // The way the line runs at its end, from the last point far enough back.
  let from: Vec | undefined;
  for (let i = points.length - 2; i >= 0; i--) {
    if (len(sub(tip, points[i])) > 1e-6) {
      from = points[i];
      break;
    }
  }
  if (!from) return undefined;
  const d = sub(tip, from);
  const u = { x: d.x / len(d), y: d.y / len(d) };
  const n = { x: -u.y, y: u.x };
  const base = { x: tip.x - u.x * size * 0.4, y: tip.y - u.y * size * 0.4 };
  return [
    { x: tip.x + u.x * size * 0.6, y: tip.y + u.y * size * 0.6 },
    { x: base.x + n.x * size * 0.45, y: base.y + n.y * size * 0.45 },
    { x: base.x - n.x * size * 0.45, y: base.y - n.y * size * 0.45 },
  ];
}
