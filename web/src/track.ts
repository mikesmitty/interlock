// Track plan geometry, worked out as internal/trackplan does: the eight
// ways out of a cell and the pieces of track they make, and the edits the
// track plan editor makes to cells. Keep the rules in step with the Go
// package.
import type { TrackBackground, TrackDirection, TrackTile } from "./api";

export const DIRECTIONS: readonly TrackDirection[] = ["n", "ne", "e", "se", "s", "sw", "w", "nw"];

const STEPS: Record<TrackDirection, [number, number]> = {
  n: [0, -1],
  ne: [1, -1],
  e: [1, 0],
  se: [1, 1],
  s: [0, 1],
  sw: [-1, 1],
  w: [-1, 0],
  nw: [-1, -1],
};

export const DIRECTION_NAMES: Record<TrackDirection, string> = {
  n: "north",
  ne: "northeast",
  e: "east",
  se: "southeast",
  s: "south",
  sw: "southwest",
  w: "west",
  nw: "northwest",
};

/** The offset to the neighboring cell; y grows downward. */
export const step = (d: TrackDirection): [number, number] => STEPS[d];

const indexOf = (d: TrackDirection) => DIRECTIONS.indexOf(d);

/** The direction `steps` eighths of a turn clockwise from d. */
export const turn = (d: TrackDirection, steps: number): TrackDirection => DIRECTIONS[(((indexOf(d) + steps) % 8) + 8) % 8];

export const opposite = (d: TrackDirection): TrackDirection => turn(d, 4);

/** How many eighths of a turn apart two directions are, 0 to 4. */
export function apart(a: TrackDirection, b: TrackDirection): number {
  const n = (indexOf(b) - indexOf(a) + 8) % 8;
  return n > 4 ? 8 - n : n;
}

/** The direction of a neighboring cell's offset, if it is a neighbor. */
export function directionOf(dx: number, dy: number): TrackDirection | undefined {
  return DIRECTIONS.find((d) => STEPS[d][0] === dx && STEPS[d][1] === dy);
}

/** Directions in the saved order, clockwise from north. */
export const sortDirections = (ds: readonly TrackDirection[]): TrackDirection[] => [...ds].sort((a, b) => indexOf(a) - indexOf(b));

export type Piece = "bumper" | "straight" | "curve" | "sharp_curve" | "turnout_left" | "turnout_right" | "wye" | "crossing" | "crossing_45";

export const PIECE_LABELS: Record<Piece, string> = {
  bumper: "End of track",
  straight: "Straight",
  curve: "Curve",
  sharp_curve: "Sharp curve",
  turnout_left: "Left turnout",
  turnout_right: "Right turnout",
  wye: "Wye",
  crossing: "Crossing",
  crossing_45: "45° crossing",
};

export type Position = "closed" | "thrown";

export interface TrackPath {
  a: TrackDirection;
  b: TrackDirection;
  position?: Position;
}

export interface Shape {
  piece: Piece;
  /** A turnout's single end, and the ends closed and thrown lead to. */
  points?: TrackDirection;
  closed?: TrackDirection;
  thrown?: TrackDirection;
  paths: TrackPath[];
}

export const switches = (piece: Piece): boolean => piece === "turnout_left" || piece === "turnout_right" || piece === "wye";

function turnout(piece: Piece, points: TrackDirection, closed: TrackDirection, thrown: TrackDirection): Shape {
  return {
    piece,
    points,
    closed,
    thrown,
    paths: [
      { a: points, b: closed, position: "closed" },
      { a: points, b: thrown, position: "thrown" },
    ],
  };
}

/** The piece that leaves a cell by these directions, or undefined when
 * no piece does. */
export function classify(exits: readonly TrackDirection[]): Shape | undefined {
  if (new Set(exits).size !== exits.length) return undefined;
  const has = (d: TrackDirection) => exits.includes(d);
  const sorted = sortDirections(exits);
  switch (sorted.length) {
    case 1:
      return { piece: "bumper", paths: [] };
    case 2: {
      const [a, b] = sorted;
      const piece = ({ 4: "straight", 3: "curve", 2: "sharp_curve" } as Record<number, Piece | undefined>)[apart(a, b)];
      return piece ? { piece, paths: [{ a, b }] } : undefined;
    }
    case 3:
      for (const points of sorted) {
        const ahead = opposite(points);
        if (has(ahead)) {
          const branch = sorted.find((d) => d !== points && d !== ahead);
          if (branch === turn(ahead, -1)) return turnout("turnout_left", points, ahead, branch);
          if (branch === turn(ahead, 1)) return turnout("turnout_right", points, ahead, branch);
        }
        if (has(turn(ahead, -1)) && has(turn(ahead, 1))) return turnout("wye", points, turn(ahead, -1), turn(ahead, 1));
      }
      return undefined;
    case 4: {
      const first = sorted[0];
      const second = sorted.find((d) => d !== first && d !== opposite(first));
      if (!second || !has(opposite(first)) || !has(opposite(second))) return undefined;
      return {
        piece: apart(first, second) === 2 ? "crossing" : "crossing_45",
        paths: [
          { a: first, b: opposite(first) },
          { a: second, b: opposite(second) },
        ],
      };
    }
  }
  return undefined;
}

/** Why no piece leaves a cell by these directions. */
function misfit(exits: readonly TrackDirection[]): string {
  switch (exits.length) {
    case 2:
      return "Track can't turn that sharply.";
    case 3:
      return "Those three tracks don't make a turnout.";
    case 4:
      return "Only two straight tracks can cross in a cell.";
  }
  return "No more track fits in that cell.";
}

// Editing. The editor keeps the cells in a map by position and changes
// them by replacing the map, so each version can be put back by Undo.

export interface Cell {
  x: number;
  y: number;
}

export type Tiles = Map<string, TrackTile>;

export const cellKey = (x: number, y: number): string => `${x},${y}`;

export function indexTiles(tiles: readonly TrackTile[]): Tiles {
  return new Map(tiles.map((t) => [cellKey(t.x, t.y), t]));
}

/** The cells on a line from one cell to another, each next to the one
 * before, both ends included. */
export function lineCells(from: Cell, to: Cell): Cell[] {
  const n = Math.max(Math.abs(to.x - from.x), Math.abs(to.y - from.y));
  const cells: Cell[] = [from];
  for (let i = 1; i <= n; i++) {
    cells.push({ x: Math.round(from.x + ((to.x - from.x) * i) / n), y: Math.round(from.y + ((to.y - from.y) * i) / n) });
  }
  return cells;
}

export type Layer = "track" | "platform";

function without<K extends keyof TrackTile>(tile: TrackTile, key: K): TrackTile {
  const copy = { ...tile };
  delete copy[key];
  return copy;
}

/** Draws a stroke of track or platform through cells, each next to the
 * one before, starting from the cells as they were before the stroke.
 * Track changes piece as it gains ends: a third makes a turnout, a fourth
 * a crossing. The stroke is checked whole, so it can cross a line (which
 * is a T for a moment); where it can't go on, it stops in the cell
 * before, and the error says why. */
export function drawStroke(base: Tiles, cells: readonly Cell[], layer: Layer): { tiles: Tiles; error?: string } {
  let error: string | undefined;
  let n = cells.length;
  while (n > 1) {
    const result = applyStroke(base, cells.slice(0, n), layer);
    if (result.bad === undefined) return { tiles: result.tiles, error };
    error ??= result.reason;
    n = result.bad;
  }
  return { tiles: base, error };
}

/** Lays a whole stroke, or finds the first cell of it that can't take it. */
function applyStroke(base: Tiles, cells: readonly Cell[], layer: Layer): { tiles: Tiles; bad?: number; reason?: string } {
  const added = new Map<string, { index: number; exits: TrackDirection[] }>();
  const add = (cell: Cell, index: number, d: TrackDirection) => {
    const key = cellKey(cell.x, cell.y);
    const entry = added.get(key) ?? { index, exits: [...(base.get(key)?.[layer] ?? [])] };
    if (!entry.exits.includes(d)) entry.exits.push(d);
    added.set(key, entry);
  };
  for (let i = 1; i < cells.length; i++) {
    const d = directionOf(cells[i].x - cells[i - 1].x, cells[i].y - cells[i - 1].y);
    if (!d) continue;
    add(cells[i - 1], i - 1, d);
    add(cells[i], i, opposite(d));
  }
  const tiles = new Map(base);
  let bad: number | undefined;
  let reason: string | undefined;
  const fail = (index: number, why: string) => {
    if (bad === undefined || index < bad) [bad, reason] = [index, why];
  };
  for (const [key, { index, exits }] of added) {
    const tile = base.get(key);
    if (tile?.label) {
      fail(index, "A label is in the way.");
      continue;
    }
    if (layer === "track" && tile?.platform?.length) {
      fail(index, "A platform is in the way.");
      continue;
    }
    if (layer === "platform" && tile?.track?.length) {
      fail(index, "Track is in the way.");
      continue;
    }
    const [x, y] = key.split(",").map(Number);
    let changed: TrackTile = { ...(tile ?? { x, y }), [layer]: sortDirections(exits) };
    if (layer === "track") {
      const shape = classify(exits);
      if (!shape) {
        fail(index, misfit(exits));
        continue;
      }
      if (!switches(shape.piece)) changed = without(changed, "turnout");
    }
    tiles.set(key, changed);
  }
  return { tiles, bad, reason };
}

/** Clears a cell. Track and platform that led into it from the
 * neighboring cells stop at their edge; a neighbor left with nothing goes
 * too, and one whose remaining track makes no piece keeps its open end. */
export function erase(tiles: Tiles, cell: Cell): Tiles {
  const key = cellKey(cell.x, cell.y);
  if (!tiles.has(key)) return tiles;
  const next = new Map(tiles);
  next.delete(key);
  for (const d of DIRECTIONS) {
    const [dx, dy] = step(d);
    const nkey = cellKey(cell.x + dx, cell.y + dy);
    const neighbor = next.get(nkey);
    const back = opposite(d);
    if (!neighbor) continue;
    for (const layer of ["track", "platform"] as const) {
      const exits = neighbor[layer];
      if (!exits?.includes(back)) continue;
      const rest = exits.filter((e) => e !== back);
      if (rest.length === 0) {
        next.delete(nkey);
      } else if (layer === "platform") {
        next.set(nkey, { ...neighbor, platform: rest });
      } else {
        const shape = classify(rest);
        if (!shape) continue;
        const changed = { ...neighbor, track: rest };
        next.set(nkey, switches(shape.piece) ? changed : without(changed, "turnout"));
      }
    }
  }
  return next;
}

/** Track ends that lead into a cell whose track doesn't lead back. */
export function openEnds(tiles: Tiles): { x: number; y: number; dir: TrackDirection }[] {
  const out: { x: number; y: number; dir: TrackDirection }[] = [];
  for (const t of tiles.values()) {
    for (const d of t.track ?? []) {
      const [dx, dy] = step(d);
      const n = tiles.get(cellKey(t.x + dx, t.y + dy));
      if (!n?.track?.includes(opposite(d))) out.push({ x: t.x, y: t.y, dir: d });
    }
  }
  return out;
}

export interface Bounds {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

/** The smallest box around the cells, or undefined for none. */
export function bounds(tiles: Iterable<TrackTile>): Bounds | undefined {
  let b: Bounds | undefined;
  for (const t of tiles) {
    if (!b) b = { minX: t.x, minY: t.y, maxX: t.x, maxY: t.y };
    else {
      b.minX = Math.min(b.minX, t.x);
      b.minY = Math.min(b.minY, t.y);
      b.maxX = Math.max(b.maxX, t.x);
      b.maxY = Math.max(b.maxY, t.y);
    }
  }
  return b;
}

/** The cells a background covers, turned as it is drawn. */
export function backgroundBounds(b: TrackBackground | undefined): Bounds | undefined {
  if (!b) return undefined;
  const turn = ((b.rotation ?? 0) * Math.PI) / 180;
  const [cos, sin] = [Math.cos(turn), Math.sin(turn)];
  const [cx, cy] = [b.x + b.width / 2, b.y + b.height / 2];
  const xs: number[] = [];
  const ys: number[] = [];
  for (const [dx, dy] of [
    [-1, -1],
    [1, -1],
    [1, 1],
    [-1, 1],
  ]) {
    const [px, py] = [(dx * b.width) / 2, (dy * b.height) / 2];
    xs.push(cx + px * cos - py * sin);
    ys.push(cy + px * sin + py * cos);
  }
  // A hair of slack, so a corner on a cell's edge doesn't claim the next.
  const eps = 1e-9;
  return {
    minX: Math.floor(Math.min(...xs) + eps),
    minY: Math.floor(Math.min(...ys) + eps),
    maxX: Math.ceil(Math.max(...xs) - eps) - 1,
    maxY: Math.ceil(Math.max(...ys) - eps) - 1,
  };
}

/** An angle in degrees brought into 0 up to 360. */
export const normalizeDegrees = (deg: number): number => ((deg % 360) + 360) % 360;

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
