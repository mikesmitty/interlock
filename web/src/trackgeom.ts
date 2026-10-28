// Track plan geometry, worked out as internal/trackplan does: track to
// scale in millimeters, x rightward and y downward, headings in degrees
// clockwise from rightward. Keep the rules in step with the Go package.
import type { JunctionKind, PlanJunction, PlanPoint, PlanTrack, TrackBackground, TrackPlan, Vec } from "./api";

export const add = (a: Vec, b: Vec): Vec => ({ x: a.x + b.x, y: a.y + b.y });
export const sub = (a: Vec, b: Vec): Vec => ({ x: a.x - b.x, y: a.y - b.y });
export const scale = (a: Vec, k: number): Vec => ({ x: a.x * k, y: a.y * k });
export const dist = (a: Vec, b: Vec): number => Math.hypot(a.x - b.x, a.y - b.y);
export const lerp = (a: Vec, b: Vec, f: number): Vec => ({ x: a.x + (b.x - a.x) * f, y: a.y + (b.y - a.y) * f });

const radians = (deg: number) => (deg * Math.PI) / 180;
const degrees = (rad: number) => (rad * 180) / Math.PI;

/** The heading an offset points in. */
export const headingOf = (v: Vec): number => degrees(Math.atan2(v.y, v.x));

/** One millimeter along a heading. */
export const unit = (heading: number): Vec => ({ x: Math.cos(radians(heading)), y: Math.sin(radians(heading)) });

/** a − b brought into (−180, 180]. */
export function turnDiff(a: number, b: number): number {
  let d = (a - b) % 360;
  if (d > 180) d -= 360;
  if (d <= -180) d += 360;
  return d;
}

/** An angle in degrees brought into 0 up to 360. */
export const normalizeDegrees = (deg: number): number => ((deg % 360) + 360) % 360;

export const round = (n: number, places: number) => Math.round(n * 10 ** places) / 10 ** places;

// Track: from a to b, turning by angle degrees on the way (positive
// clockwise, 0 straight).

export interface ArcInfo {
  radius: number;
  length: number;
  /** The way a train leaving a for b sets off, and the way it arrives. */
  startHeading: number;
  endHeading: number;
}

export function arcInfo(a: Vec, b: Vec, angle = 0): ArcInfo {
  const chord = dist(a, b);
  const h = headingOf(sub(b, a));
  if (!angle) return { radius: Infinity, length: chord, startHeading: h, endHeading: h };
  const radius = chord / (2 * Math.sin(radians(Math.abs(angle)) / 2));
  return { radius, length: radius * radians(Math.abs(angle)), startHeading: h - angle / 2, endHeading: h + angle / 2 };
}

/** The point a fraction f of the way along the track. */
export function arcAt(a: Vec, b: Vec, angle: number | undefined, f: number): Vec {
  if (!angle) return lerp(a, b, f);
  const { radius, startHeading } = arcInfo(a, b, angle);
  const center = add(a, scale(unit(startHeading + Math.sign(angle) * 90), radius));
  return add(center, scale(unit(headingOf(sub(a, center)) + angle * f), radius));
}

/** SVG path data for track. */
export function trackPath(a: Vec, b: Vec, angle?: number): string {
  if (!angle) return `M${a.x} ${a.y}L${b.x} ${b.y}`;
  const { radius } = arcInfo(a, b, angle);
  return `M${a.x} ${a.y}A${radius} ${radius} 0 ${Math.abs(angle) > 180 ? 1 : 0} ${angle > 0 ? 1 : 0} ${b.x} ${b.y}`;
}

/** Where track ends that sets off from p along heading for length mm. */
export const straightFrom = (p: Vec, heading: number, length: number): Vec => add(p, scale(unit(heading), length));

/** Where a curve of the radius ends that sets off from p along heading
 * and turns by sweep degrees (positive clockwise). */
export function arcFrom(p: Vec, heading: number, radius: number, sweep: number): Vec {
  const center = add(p, scale(unit(heading + Math.sign(sweep) * 90), radius));
  return add(center, scale(unit(headingOf(sub(p, center)) + sweep), radius));
}

/** The turn of the curve that sets off from p along heading and passes
 * through target. */
export const tangentAngle = (p: Vec, heading: number, target: Vec): number => 2 * turnDiff(headingOf(sub(target, p)), heading);

// Junctions.

export interface Route {
  from: number;
  to: number;
  position?: "closed" | "thrown";
}

export const KINDS: Record<JunctionKind, { name: string; ends: number; routes: Route[]; switches: boolean; center: boolean }> = {
  turnout: {
    name: "Turnout",
    ends: 3,
    switches: true,
    center: true,
    routes: [
      { from: 0, to: 1, position: "closed" },
      { from: 0, to: 2, position: "thrown" },
    ],
  },
  wye: {
    name: "Wye",
    ends: 3,
    switches: true,
    center: true,
    routes: [
      { from: 0, to: 1, position: "closed" },
      { from: 0, to: 2, position: "thrown" },
    ],
  },
  crossing: {
    name: "Crossing",
    ends: 4,
    switches: false,
    center: false,
    routes: [
      { from: 0, to: 1 },
      { from: 2, to: 3 },
    ],
  },
  crossover: {
    name: "Crossover",
    ends: 4,
    switches: true,
    center: false,
    routes: [
      { from: 0, to: 1, position: "closed" },
      { from: 2, to: 3, position: "closed" },
      { from: 0, to: 3, position: "thrown" },
    ],
  },
  double_crossover: {
    name: "Double crossover",
    ends: 4,
    switches: true,
    center: false,
    routes: [
      { from: 0, to: 1, position: "closed" },
      { from: 2, to: 3, position: "closed" },
      { from: 0, to: 3, position: "thrown" },
      { from: 2, to: 1, position: "thrown" },
    ],
  },
};

const partner = (end: number) => end ^ 1;

function lineCross(a1: Vec, a2: Vec, b1: Vec, b2: Vec): Vec | undefined {
  const d1 = sub(a2, a1);
  const d2 = sub(b2, b1);
  const den = d1.x * d2.y - d1.y * d2.x;
  if (Math.abs(den) < 1e-9) return undefined;
  const t = ((b1.x - a1.x) * d2.y - (b1.y - a1.y) * d2.x) / den;
  return add(a1, scale(d1, t));
}

/** Where a junction's routes part or cross. */
export function junctionCenter(j: Pick<PlanJunction, "at">, ends: Vec[]): Vec {
  if (j.at) return j.at;
  if (ends.length === 4) return lineCross(ends[0], ends[1], ends[2], ends[3]) ?? lerp(ends[0], ends[3], 0.5);
  return ends[0];
}

/** SVG path data for a route through a junction. */
export function routePath(kind: JunctionKind, ends: Vec[], center: Vec, r: Route): string {
  const a = ends[r.from];
  const b = ends[r.to];
  if (kind === "turnout" || kind === "wye") return `M${a.x} ${a.y}Q${center.x} ${center.y} ${b.x} ${b.y}`;
  if ((kind === "crossover" || kind === "double_crossover") && r.to !== partner(r.from)) {
    const c1 = lerp(a, ends[partner(r.from)], 0.5);
    const c2 = lerp(b, ends[partner(r.to)], 0.5);
    return `M${a.x} ${a.y}C${c1.x} ${c1.y} ${c2.x} ${c2.y} ${b.x} ${b.y}`;
  }
  return `M${a.x} ${a.y}L${b.x} ${b.y}`;
}

/** The heading track leaves a junction by at an end. */
export function junctionOutward(kind: JunctionKind, ends: Vec[], center: Vec, end: number): number {
  if (kind === "crossover" || kind === "double_crossover") return headingOf(sub(ends[end], ends[partner(end)]));
  return headingOf(sub(ends[end], center));
}

// The plan as a graph.

export interface Attach {
  kind: "track" | "junction";
  id: string;
  end: number;
}

export interface PlanIndex {
  points: Map<string, PlanPoint>;
  tracks: Map<string, PlanTrack>;
  junctions: Map<string, PlanJunction>;
  /** The track ends at each point. */
  at: Map<string, Attach[]>;
}

export function indexPlan(plan: Pick<TrackPlan, "points" | "tracks" | "junctions">): PlanIndex {
  const index: PlanIndex = {
    points: new Map(plan.points.map((p) => [p.id, p])),
    tracks: new Map(plan.tracks.map((t) => [t.id, t])),
    junctions: new Map(plan.junctions.map((j) => [j.id, j])),
    at: new Map(),
  };
  const attach = (point: string, a: Attach) => index.at.set(point, [...(index.at.get(point) ?? []), a]);
  for (const t of plan.tracks) {
    attach(t.a, { kind: "track", id: t.id, end: 0 });
    attach(t.b, { kind: "track", id: t.id, end: 1 });
  }
  for (const j of plan.junctions) j.ends.forEach((e, i) => attach(e, { kind: "junction", id: j.id, end: i }));
  return index;
}

/** The heading the track at a piece's end leaves by. */
export function outward(index: PlanIndex, a: Attach): number | undefined {
  if (a.kind === "track") {
    const t = index.tracks.get(a.id);
    const pa = t && index.points.get(t.a);
    const pb = t && index.points.get(t.b);
    if (!t || !pa || !pb) return undefined;
    const info = arcInfo(pa, pb, t.angle);
    return a.end === 0 ? info.startHeading + 180 : info.endHeading;
  }
  const j = index.junctions.get(a.id);
  if (!j) return undefined;
  const ends = j.ends.map((e) => index.points.get(e));
  if (ends.some((e) => !e)) return undefined;
  return junctionOutward(j.kind, ends as Vec[], junctionCenter(j, ends as Vec[]), a.end);
}

/** The points where track ends without a buffer stop. */
export function openEnds(index: PlanIndex): PlanPoint[] {
  const out: PlanPoint[] = [];
  for (const [id, ats] of index.at) {
    const p = index.points.get(id);
    if (p && ats.length === 1 && !p.bumper) out.push(p);
  }
  return out;
}

/** The points track can be continued from: open ends, and buffer stops. */
export function freeEnds(index: PlanIndex): PlanPoint[] {
  const out: PlanPoint[] = [];
  for (const [id, ats] of index.at) {
    const p = index.points.get(id);
    if (p && ats.length === 1) out.push(p);
  }
  return out;
}

/** The middle of a track. */
export function trackMiddle(index: PlanIndex, t: PlanTrack): Vec | undefined {
  const a = index.points.get(t.a);
  const b = index.points.get(t.b);
  return a && b ? arcAt(a, b, t.angle, 0.5) : undefined;
}

// Kato Unitrack N, the pieces that aren't plain track. Each is given
// with its first end at the origin, the track leading off along +x and
// +y to the right of it.

export interface PieceShape {
  id: string;
  label: string;
  kind: JunctionKind;
  ends: Vec[];
  at?: Vec;
}

function turnoutShape(id: string, label: string, kind: JunctionKind, closed: number, radius: number, deg: number, side: 1 | -1): PieceShape {
  const leg = (s: number): Vec => ({ x: radius * Math.sin(radians(deg)), y: s * radius * (1 - Math.cos(radians(deg))) });
  const at = { x: radius * Math.tan(radians(deg / 2)), y: 0 };
  if (kind === "wye") return { id, label, kind, ends: [{ x: 0, y: 0 }, leg(-1), leg(1)], at };
  return { id, label, kind, ends: [{ x: 0, y: 0 }, { x: closed, y: 0 }, leg(side)], at };
}

function crossoverShape(id: string, label: string, kind: JunctionKind, length: number, spacing: number, side: 1 | -1): PieceShape {
  return {
    id,
    label,
    kind,
    ends: [
      { x: 0, y: 0 },
      { x: length, y: 0 },
      { x: 0, y: side * spacing },
      { x: length, y: side * spacing },
    ],
  };
}

/** The junction pieces offered for placing, as Kato makes them. */
export function pieceShapes(spacing: number, crossoverLength: number): PieceShape[] {
  return [
    turnoutShape("t4r", "#4 turnout, right", "turnout", 126, 481, 15, 1),
    turnoutShape("t4l", "#4 turnout, left", "turnout", 126, 481, 15, -1),
    turnoutShape("t6r", "#6 turnout, right", "turnout", 186, 718, 15, 1),
    turnoutShape("t6l", "#6 turnout, left", "turnout", 186, 718, 15, -1),
    turnoutShape("wye", "Wye", "wye", 0, 481, 15, 1),
    crossoverShape("xdr", "Double crossover, track to the right", "double_crossover", crossoverLength, spacing, 1),
    crossoverShape("xdl", "Double crossover, track to the left", "double_crossover", crossoverLength, spacing, -1),
    crossoverShape("xsr", "Crossover to the track on the right", "crossover", crossoverLength, spacing, 1),
    crossoverShape("xsl", "Crossover to the track on the left", "crossover", crossoverLength, spacing, -1),
  ];
}

/** The curve radii of Kato Unitrack N, in millimeters. */
export const KATO_RADII = [216, 249, 282, 315, 348, 381, 414, 481, 718];

/** Places a piece with its first end at origin, leading off along heading. */
export function placePiece(piece: PieceShape, origin: Vec, heading: number): { ends: Vec[]; at?: Vec } {
  const along = unit(heading);
  const right = unit(heading + 90);
  // To a thousandth of a millimeter, and never -0.
  const tidy = (n: number) => round(n, 3) + 0;
  const place = (v: Vec) => {
    const p = add(origin, add(scale(along, v.x), scale(right, v.y)));
    return { x: tidy(p.x), y: tidy(p.y) };
  };
  return { ends: piece.ends.map(place), at: piece.at && place(piece.at) };
}

// IDs and merging.

/** Makes IDs with a prefix and a number not yet used in the plan. */
export function idMaker(plan: Pick<TrackPlan, "points" | "tracks" | "junctions" | "platforms" | "labels">): (prefix: string) => string {
  const next = new Map<string, number>();
  for (const list of [plan.points, plan.tracks, plan.junctions, plan.platforms, plan.labels]) {
    for (const { id } of list) {
      const m = /^([a-z]+)(\d+)$/.exec(id);
      if (m) next.set(m[1], Math.max(next.get(m[1]) ?? 1, Number(m[2]) + 1));
    }
  }
  return (prefix) => {
    const n = next.get(prefix) ?? 1;
    next.set(prefix, n + 1);
    return `${prefix}${n}`;
  };
}

/** A name made into an ID: lowercase letters, digits, - and _, not one
 * of those taken. */
export function makeID(name: string, taken: ReadonlySet<string>, fallback: string): string {
  const base =
    name
      .normalize("NFKD")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "_")
      .replace(/^_+|_+$/g, "")
      .slice(0, 56) || fallback;
  let id = base;
  for (let n = 2; taken.has(id); n++) id = `${base}_${n}`;
  return id;
}

/** Adds imported track to a plan under new IDs, on a layer. */
export function mergeTrack(plan: TrackPlan, imported: Pick<TrackPlan, "points" | "tracks" | "junctions">, layer?: string): TrackPlan {
  const id = idMaker(plan);
  const points = new Map(imported.points.map((p) => [p.id, id("p")]));
  const on = <T extends { layer?: string }>(x: T): T => {
    const copy = { ...x };
    if (layer) copy.layer = layer;
    else delete copy.layer;
    return copy;
  };
  return {
    ...plan,
    points: [...plan.points, ...imported.points.map((p) => ({ ...p, id: points.get(p.id)! }))],
    tracks: [...plan.tracks, ...imported.tracks.map((t) => on({ ...t, id: id("t"), a: points.get(t.a)!, b: points.get(t.b)! }))],
    junctions: [...plan.junctions, ...imported.junctions.map((j) => on({ ...j, id: id("j"), ends: j.ends.map((e) => points.get(e)!) }))],
  };
}

// Extents.

export interface Bounds {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

export function boundsOf(vs: Iterable<Vec>): Bounds | undefined {
  let b: Bounds | undefined;
  for (const v of vs) {
    if (!b) b = { minX: v.x, minY: v.y, maxX: v.x, maxY: v.y };
    else {
      b.minX = Math.min(b.minX, v.x);
      b.minY = Math.min(b.minY, v.y);
      b.maxX = Math.max(b.maxX, v.x);
      b.maxY = Math.max(b.maxY, v.y);
    }
  }
  return b;
}

export function union(a?: Bounds, b?: Bounds): Bounds | undefined {
  if (!a || !b) return a ?? b;
  return { minX: Math.min(a.minX, b.minX), minY: Math.min(a.minY, b.minY), maxX: Math.max(a.maxX, b.maxX), maxY: Math.max(a.maxY, b.maxY) };
}

/** The corners of a rectangle about its middle, turned by angle. */
export function corners(c: Vec, length: number, width: number, angle = 0): Vec[] {
  const along = scale(unit(angle), length / 2);
  const across = scale(unit(angle + 90), width / 2);
  return [add(add(c, along), across), add(sub(c, along), across), sub(sub(c, along), across), sub(add(c, along), across)];
}

/** The extent of everything drawn: track, platforms and labels. */
export function planBounds(plan: Pick<TrackPlan, "points" | "platforms" | "labels">): Bounds | undefined {
  return boundsOf([...plan.points, ...plan.platforms.flatMap((p) => corners(p, p.length, p.width, p.angle)), ...plan.labels]);
}

/** The extent of a background, turned as it is drawn. */
export function backgroundBounds(b: TrackBackground | undefined): Bounds | undefined {
  if (!b) return undefined;
  return boundsOf(corners({ x: b.x + b.width / 2, y: b.y + b.height / 2 }, b.width, b.height, b.rotation ?? 0));
}

/** The nearest point on a grid of the spacing. */
export const snap = (v: Vec, grid: number): Vec => ({ x: Math.round(v.x / grid) * grid, y: Math.round(v.y / grid) * grid });

// Editing: the changes the track plan editor makes. Each returns a new
// plan, so every version can be put back by Undo.

export type ElementKind = "track" | "junction" | "platform" | "label";

/** Something drawn on the plan, by kind and ID. */
export interface ElementRef {
  kind: ElementKind;
  id: string;
}

export const sameRef = (a?: ElementRef, b?: ElementRef) => !!a && !!b && a.kind === b.kind && a.id === b.id;

/** A track end new track can continue from, and the way it leads. */
export interface End {
  point: PlanPoint;
  heading: number;
}

/** The free track end nearest to at, within reach millimeters. */
export function endNear(index: PlanIndex, at: Vec, reach: number, except?: string): End | undefined {
  let best: End | undefined;
  let bestDist = reach;
  for (const p of freeEnds(index)) {
    const d = dist(p, at);
    if (p.id === except || d > bestDist) continue;
    const heading = outward(index, index.at.get(p.id)![0]);
    if (heading === undefined) continue;
    best = { point: p, heading };
    bestDist = d;
  }
  return best;
}

/** How close new track must come to a free end to join it. */
const JOIN = 1;

/** A point for new track to end at: the given one, a free end already
 * where pos is, or a new point. Track continuing from a buffer stop
 * takes the buffer stop away. */
function pointFor(plan: TrackPlan, index: PlanIndex, id: (prefix: string) => string, at: string | Vec): { plan: TrackPlan; point: string } {
  if (typeof at === "string") {
    return { plan: { ...plan, points: plan.points.map((p) => (p.id === at && p.bumper ? { id: p.id, x: p.x, y: p.y } : p)) }, point: at };
  }
  const found = freeEnds(index).find((p) => dist(p, at) < JOIN && plan.points.some((q) => q.id === p.id));
  if (found) return pointFor(plan, index, id, found.id);
  const point = id("p");
  return { plan: { ...plan, points: [...plan.points, { id: point, x: at.x, y: at.y }] }, point };
}

/** Adds track from a to b, each an existing point or a place. */
export function addTrack(plan: TrackPlan, index: PlanIndex, a: string | Vec, b: string | Vec, angle: number, layer?: string): TrackPlan {
  const id = idMaker(plan);
  const pa = pointFor(plan, index, id, a);
  const pb = pointFor(pa.plan, index, id, b);
  const track = { id: id("t"), a: pa.point, b: pb.point, ...(angle ? { angle } : {}), ...(layer ? { layer } : {}) };
  return { ...pb.plan, tracks: [...pb.plan.tracks, track] };
}

/** Adds a junction whose ends are existing points or places; ends that
 * land on free ends join them. */
export function addJunction(plan: TrackPlan, index: PlanIndex, kind: JunctionKind, ends: (string | Vec)[], at?: Vec, layer?: string): TrackPlan {
  const id = idMaker(plan);
  let next = plan;
  const ids: string[] = [];
  for (const e of ends) {
    const r = pointFor(next, index, id, e);
    next = r.plan;
    ids.push(r.point);
  }
  const junction = { id: id("j"), kind, ends: ids, ...(at ? { at } : {}), ...(layer ? { layer } : {}) };
  return { ...next, junctions: [...next.junctions, junction] };
}

/** Drops points no track or junction ends at. */
export function prunePoints(plan: TrackPlan): TrackPlan {
  const used = new Set<string>();
  for (const t of plan.tracks) used.add(t.a).add(t.b);
  for (const j of plan.junctions) for (const e of j.ends) used.add(e);
  const points = plan.points.filter((p) => used.has(p.id));
  return points.length === plan.points.length ? plan : { ...plan, points };
}

/** Deletes elements, and the points left with nothing at them. */
export function removeElements(plan: TrackPlan, refs: ElementRef[]): TrackPlan {
  const gone = (kind: ElementKind, id: string) => refs.some((r) => r.kind === kind && r.id === id);
  return prunePoints({
    ...plan,
    tracks: plan.tracks.filter((t) => !gone("track", t.id)),
    junctions: plan.junctions.filter((j) => !gone("junction", j.id)),
    platforms: plan.platforms.filter((p) => !gone("platform", p.id)),
    labels: plan.labels.filter((l) => !gone("label", l.id)),
  });
}

/** Puts track and junctions in a block, or takes them out of theirs. */
export function setBlock(plan: TrackPlan, refs: ElementRef[], block: string | undefined): TrackPlan {
  const set = <T extends { id: string; block?: string }>(kind: ElementKind, x: T): T => {
    if (!refs.some((r) => r.kind === kind && r.id === x.id) || x.block === block) return x;
    const copy = { ...x };
    if (block) copy.block = block;
    else delete copy.block;
    return copy;
  };
  return { ...plan, tracks: plan.tracks.map((t) => set("track", t)), junctions: plan.junctions.map((j) => set("junction", j)) };
}

/** Changes one element's fields. */
export function updateElement(plan: TrackPlan, ref: ElementRef, change: Record<string, unknown>): TrackPlan {
  const apply = <T extends { id: string }>(x: T): T => {
    if (x.id !== ref.id) return x;
    const copy: Record<string, unknown> = { ...x, ...change };
    for (const [k, v] of Object.entries(change)) if (v === undefined || v === "") delete copy[k];
    return copy as T;
  };
  switch (ref.kind) {
    case "track":
      return { ...plan, tracks: plan.tracks.map(apply) };
    case "junction":
      return { ...plan, junctions: plan.junctions.map(apply) };
    case "platform":
      return { ...plan, platforms: plan.platforms.map(apply) };
    case "label":
      return { ...plan, labels: plan.labels.map(apply) };
  }
}
