import { type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent, type RefObject, useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  type Entity,
  type PlanJunction,
  type PlanTrack,
  type Route,
  type TrackBackground,
  type TrackBlock,
  type TrackLeg,
  type TrackPlan,
  type TrackedTrain,
  type Train,
  type Vec,
  backgroundURL,
} from "../api";
import { heldLegs } from "../routes";
import { leadArrow, lineMiddle, locomotiveName } from "../tracking";
import {
  type Bounds,
  type ElementKind,
  type ElementRef,
  KINDS,
  type PlanIndex,
  add,
  arcInfo,
  corners,
  junctionCenter,
  markerPlace,
  outward,
  routePath,
  sameRef,
  scale,
  trackMiddle,
  trackPath,
  unit,
} from "../trackgeom";
import { type View, fitView, panBy, pinchView, toPlan, viewBounds, zoomAt, zoomBy } from "../trackview";

/** How a block's track is drawn: by its detector's state when operating,
 * or by whether it is the block being worked on when editing. */
export type TrackTone = "plain" | "undetected" | "occupied" | "unknown" | "active";

interface DiagramProps {
  plan: TrackPlan;
  index: PlanIndex;
  entities: Record<string, Entity>;
  /** The part of the plan to show until there is a view. */
  fit: Bounds;
  /** The part of the plan in view; without one the diagram fits fit to
   * the canvas and reports that. */
  view?: View;
  onView: (view: View) => void;
  editing?: boolean;
  hiddenLayers?: ReadonlySet<string>;
  /** The block being worked on, drawn in brass while editing. */
  activeBlock?: string;
  selected?: ElementRef[];
  hover?: ElementRef;
  /** A selection box being dragged. */
  box?: Bounds;
  /** Points to mark in amber: track ends that need attention. */
  marks?: Vec[];
  /** Where a stroke would join existing track. */
  snapTo?: Vec;
  /** Track being drawn, as SVG path data. */
  preview?: string[];
  /** An image drawn under everything else. */
  background?: TrackBackground;
  /** The pointer's place on the plan, and what is drawn there. */
  onPointerDown?: (at: Vec, e: ReactPointerEvent<SVGSVGElement>, target?: ElementRef) => void;
  onPointerMove?: (at: Vec, e: ReactPointerEvent<SVGSVGElement>, target?: ElementRef) => void;
  onPointerUp?: (e: ReactPointerEvent<SVGSVGElement>) => void;
  /** A press given to onPointerDown became a pinch or was cancelled. */
  onPointerCancel?: () => void;
  onPointerLeave?: () => void;
  /** Clicking a linked turnout or crossover while operating. */
  onTurnout?: (j: PlanJunction) => void;
  /** The trains train tracking follows, drawn where they are while
   * operating; without them the trains are named in the blocks their
   * detectors see them in. */
  tracked?: TrackedTrain[];
  /** The roster, for the names of the locomotives detectors report. */
  trains?: Record<string, Train>;
  /** The routes set, lit along the track they hold while operating. */
  routes?: Route[];
  /** The junctions a route holds, with the route's name. */
  locked?: Map<string, string>;
  /** The stop markers trains being driven are making for. */
  stops?: ReadonlySet<string>;
}

/** Line widths in pixels, whatever the zoom. */
const TRACK = 3;
const HIT = 14;

const toneColor: Record<TrackTone, string> = {
  plain: "var(--ivory)",
  undetected: "var(--ivory-dim)",
  occupied: "var(--lamp-red)",
  unknown: "var(--lamp-amber)",
  active: "var(--brass)",
};

/** The block's state word, for titles and the block table. */
export function blockState(block: TrackBlock | undefined, entities: Record<string, Entity>): string {
  if (!block) return "not in a block";
  const ent = block.occupancy ? entities[block.occupancy] : undefined;
  if (!block.occupancy) return "no detector linked";
  if (!ent) return "detector not found";
  if (!ent.available) return "detector offline";
  return ent.state;
}

function toneOf(blockID: string | undefined, blocks: Map<string, TrackBlock>, entities: Record<string, Entity>, editing: boolean, activeBlock?: string): TrackTone {
  if (!blockID) return "undetected";
  if (editing) return blockID === activeBlock ? "active" : "plain";
  const state = blockState(blocks.get(blockID), entities);
  if (state === "occupied") return "occupied";
  if (state === "clear" || state === "no detector linked") return "plain";
  return "unknown";
}

/** The locomotives a block's reporter sees, by their roster names. */
export function trainsIn(block: TrackBlock | undefined, entities: Record<string, Entity>, trains: Record<string, Train> = {}): string[] {
  const rep = block?.reporter ? entities[block.reporter] : undefined;
  return rep?.locomotives?.map((l) => locomotiveName(l, trains)) ?? [];
}

/** A tracked train's tooltip: where it is and how it goes. */
export function trainTitle(t: TrackedTrain, blocks: Map<string, TrackBlock>): string {
  const where = t.blocks.map((b) => blocks.get(b)?.name ?? b).join(", ");
  const going = t.speed > 0 ? `, going ${t.direction} at ${Math.round(t.speed)} mm/s` : "";
  return `${t.name}: ${where || "not in a block"}${going}${t.seen ? "" : " (not seen just now)"}`;
}

const COMPASS = ["east", "southeast", "south", "southwest", "west", "northwest", "north", "northeast"];

/** The compass point nearest a heading on the plan, north up. */
export const compassWord = (heading: number) => COMPASS[Math.round((((heading % 360) + 360) % 360) / 45) % 8];

/** What is drawn at a place on the screen, from its data attributes. */
function targetAt(x: number, y: number): ElementRef | undefined {
  const el = document.elementFromPoint(x, y)?.closest("[data-id]");
  const kind = el?.getAttribute("data-kind") as ElementKind | null;
  const id = el?.getAttribute("data-id");
  return kind && id ? { kind, id } : undefined;
}

/** The grid's spacing as drawn: the plan's, or a multiple of it when
 * lines would crowd closer than 8 pixels. */
function gridSpacing(grid: number, zoom: number): number {
  if (!(grid > 0)) return 0;
  let g = grid;
  while (g * zoom < 8) g *= 2;
  return g;
}

/** How far, in pixels, a press moves before it is a drag. */
const DRAG_PX = 4;
/** How far the zoom buttons and keys zoom at a time. */
export const ZOOM_STEP = 1.5;

/** Safari's pinch on a trackpad, which it sends instead of a wheel. */
interface GestureEvent extends UIEvent {
  scale: number;
  clientX: number;
  clientY: number;
}

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

/** Moving and zooming the plan on its canvas: the canvas's size, the view
 * shown, and pointer handlers that keep the presses that move the plan
 * and pass the others on. Scrolling moves the plan while editing (the
 * page scrolls while operating), pinching or scrolling with ⌘ or Ctrl
 * zooms, and a drag with the middle button or with Space held moves it,
 * as does any drag while operating or two fingers on a touch screen. */
function useNavigation(props: DiagramProps, svgRef: RefObject<SVGSVGElement | null>) {
  const { fit, view, onView, editing = false } = props;
  const [size, setSize] = useState<Vec>();
  const [grab, setGrab] = useState(false);
  const [panning, setPanning] = useState(false);
  const shown = view ?? (size && size.x > 0 && size.y > 0 ? fitView(fit, size) : undefined);

  const live = useRef({ view: shown, size, fit, onView, editing });
  live.current = { view: shown, size, fit, onView, editing };
  // Changes wait for the next frame, so a burst of wheel events draws once.
  const pending = useRef<View | undefined>(undefined);
  const frame = useRef(0);
  const latest = () => pending.current ?? live.current.view;
  const move = (next: View) => {
    pending.current = next;
    if (frame.current) return;
    frame.current = requestAnimationFrame(() => {
      frame.current = 0;
      const v = pending.current;
      pending.current = undefined;
      if (!v) return;
      live.current.view = v;
      live.current.onView(v);
    });
  };
  useEffect(() => () => cancelAnimationFrame(frame.current), []);

  const touches = useRef(new Map<number, Vec>());
  const pinch = useRef<{ view: View; mid: Vec; dist: number } | undefined>(undefined);
  /** Two fingers are moving the plan; the rest of their touch is theirs. */
  const gesture = useRef(false);
  const pan = useRef<{ id: number; start: Vec; last: Vec; moved: boolean } | undefined>(undefined);
  /** The press passed on, which its release or cancel follows. */
  const passed = useRef<number | undefined>(undefined);
  /** A drag moved the plan, so the click ending it does nothing. */
  const dragged = useRef(false);
  const over = useRef(false);
  const space = useRef(false);

  const local = (e: { clientX: number; clientY: number }): Vec => {
    const r = svgRef.current?.getBoundingClientRect();
    return r ? { x: e.clientX - r.left, y: e.clientY - r.top } : { x: 0, y: 0 };
  };
  const pointAt = (e: { clientX: number; clientY: number }): Vec => {
    const v = latest();
    return v && size ? toPlan(v, size, local(e)) : { x: 0, y: 0 };
  };
  const pair = () => {
    const [a, b] = [...touches.current.values()];
    return { mid: { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 }, dist: Math.hypot(a.x - b.x, a.y - b.y) };
  };
  const startPinch = () => {
    const v = latest();
    pinch.current = v && touches.current.size >= 2 ? { view: v, ...pair() } : undefined;
  };

  useLayoutEffect(() => {
    const svg = svgRef.current;
    if (!svg) return;
    const measure = () => {
      const r = svg.getBoundingClientRect();
      setSize((s) => (s?.x === r.width && s?.y === r.height ? s : { x: r.width, y: r.height }));
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(svg);
    return () => observer.disconnect();
  }, [svgRef]);

  // The fitted view stays put once shown, so the plan doesn't jump as it
  // grows.
  useLayoutEffect(() => {
    if (!view && shown) onView(shown);
  });

  useEffect(() => {
    const svg = svgRef.current;
    if (!svg) return;
    const wheel = (e: WheelEvent) => {
      const { size, editing } = live.current;
      const v = latest();
      const zooming = e.ctrlKey || e.metaKey;
      if (!v || !size || (!zooming && !editing)) return;
      e.preventDefault();
      const px = e.deltaMode === WheelEvent.DOM_DELTA_LINE ? 16 : e.deltaMode === WheelEvent.DOM_DELTA_PAGE ? size.y : 1;
      if (zooming) move(zoomAt(v, size, Math.exp(-clamp(e.deltaY * px, -50, 50) / 100), local(e)));
      else move(panBy(v, { x: -e.deltaX * px, y: -e.deltaY * px }));
    };
    let scaled = 1;
    const gestureStart = (e: Event) => {
      e.preventDefault();
      scaled = 1;
    };
    const gestureChange = (e: Event) => {
      e.preventDefault();
      const g = e as GestureEvent;
      const { size } = live.current;
      const v = latest();
      if (v && size && touches.current.size < 2) move(zoomAt(v, size, g.scale / scaled, local(g)));
      scaled = g.scale;
    };
    svg.addEventListener("wheel", wheel, { passive: false });
    svg.addEventListener("gesturestart", gestureStart);
    svg.addEventListener("gesturechange", gestureChange);
    return () => {
      svg.removeEventListener("wheel", wheel);
      svg.removeEventListener("gesturestart", gestureStart);
      svg.removeEventListener("gesturechange", gestureChange);
    };
  }, [svgRef]);

  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      if (target?.closest?.("input, select, textarea")) return;
      if (e.key === " ") {
        // Held over the plan, Space is for moving it, and doesn't press
        // the button that has the focus or scroll the page.
        const down = e.type === "keydown";
        if (down ? !over.current : !space.current) return;
        e.preventDefault();
        space.current = down;
        setGrab(down);
        return;
      }
      if (e.type !== "keydown" || e.metaKey || e.ctrlKey || e.altKey) return;
      const { size, fit } = live.current;
      const v = latest();
      if (!v || !size) return;
      if (e.key === "0") move(fitView(fit, size));
      else if (e.key === "+" || e.key === "=") move(zoomBy(v, ZOOM_STEP));
      else if (e.key === "-") move(zoomBy(v, 1 / ZOOM_STEP));
    };
    const release = () => {
      space.current = false;
      setGrab(false);
    };
    window.addEventListener("keydown", key);
    window.addEventListener("keyup", key);
    window.addEventListener("blur", release);
    return () => {
      window.removeEventListener("keydown", key);
      window.removeEventListener("keyup", key);
      window.removeEventListener("blur", release);
    };
  }, []);

  const endPan = () => {
    pan.current = undefined;
    setPanning(false);
  };

  function onPointerDown(e: ReactPointerEvent<SVGSVGElement>) {
    if (e.pointerType === "touch") {
      touches.current.set(e.pointerId, local(e));
      if (touches.current.size >= 2) {
        // A second finger: the two move and zoom the plan, and what the
        // first began is called off.
        if (passed.current !== undefined) {
          passed.current = undefined;
          props.onPointerCancel?.();
        }
        endPan();
        gesture.current = true;
        startPinch();
        return;
      }
    }
    if (gesture.current) return;
    dragged.current = false;
    if (e.button === 1 || (e.button === 0 && (space.current || !editing))) {
      pan.current = { id: e.pointerId, start: { x: e.clientX, y: e.clientY }, last: { x: e.clientX, y: e.clientY }, moved: false };
      // Operating, the plan takes the pointer only once it moves, so a
      // click still reaches a turnout.
      if (editing || e.button === 1) e.currentTarget.setPointerCapture(e.pointerId);
      return;
    }
    passed.current = e.pointerId;
    props.onPointerDown?.(pointAt(e), e, targetAt(e.clientX, e.clientY));
  }

  function onPointerMove(e: ReactPointerEvent<SVGSVGElement>) {
    over.current = true;
    if (e.pointerType === "touch" && touches.current.has(e.pointerId)) {
      touches.current.set(e.pointerId, local(e));
      const p = pinch.current;
      if (p && size) {
        const now = pair();
        move(pinchView(p.view, size, p.mid, now.mid, p.dist > 0 ? now.dist / p.dist : 1));
        return;
      }
    }
    if (gesture.current) return;
    const p = pan.current;
    if (p) {
      if (p.id !== e.pointerId) return;
      if (!p.moved && Math.hypot(e.clientX - p.start.x, e.clientY - p.start.y) < DRAG_PX) return;
      if (!p.moved) {
        p.moved = true;
        dragged.current = true;
        setPanning(true);
        if (!e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.setPointerCapture(e.pointerId);
      }
      const v = latest();
      if (v) move(panBy(v, { x: e.clientX - p.last.x, y: e.clientY - p.last.y }));
      p.last = { x: e.clientX, y: e.clientY };
      return;
    }
    props.onPointerMove?.(pointAt(e), e, targetAt(e.clientX, e.clientY));
  }

  function onPointerEnd(e: ReactPointerEvent<SVGSVGElement>, cancelled: boolean) {
    if (e.pointerType === "touch") {
      touches.current.delete(e.pointerId);
      if (gesture.current) {
        if (touches.current.size === 0) gesture.current = false;
        startPinch();
        return;
      }
    }
    if (pan.current?.id === e.pointerId) {
      endPan();
      return;
    }
    if (passed.current !== e.pointerId) return;
    passed.current = undefined;
    if (cancelled) props.onPointerCancel?.();
    else props.onPointerUp?.(e);
  }

  return {
    size,
    view: shown,
    grab: grab || !editing,
    panning,
    handlers: {
      onPointerDown,
      onPointerMove,
      onPointerUp: (e: ReactPointerEvent<SVGSVGElement>) => onPointerEnd(e, false),
      onPointerCancel: (e: ReactPointerEvent<SVGSVGElement>) => onPointerEnd(e, true),
      onPointerEnter: () => {
        over.current = true;
      },
      onPointerLeave: () => {
        over.current = false;
        props.onPointerLeave?.();
      },
      // The middle button scrolls the page on its own on some systems.
      onMouseDown: (e: ReactMouseEvent) => {
        if (e.button === 1) e.preventDefault();
      },
      onClickCapture: (e: ReactMouseEvent) => {
        if (!dragged.current) return;
        dragged.current = false;
        e.stopPropagation();
      },
    },
  };
}

/** The track diagram: track, platforms and labels to scale, lit by the
 * layout's state. */
export function TrackDiagram(props: DiagramProps) {
  const { plan, index, editing = false, entities, activeBlock, hiddenLayers, tracked } = props;
  const svgRef = useRef<SVGSVGElement>(null);
  const nav = useNavigation(props, svgRef);

  // A firm press on a Mac trackpad opens Look Up, a dictionary, which
  // nobody drawing track wants. Safari lets a page cancel it; Chrome
  // doesn't, but finds no word to look up on the plan.
  useEffect(() => {
    const svg = svgRef.current;
    if (!editing || !svg) return;
    const cancel = (e: Event) => e.preventDefault();
    svg.addEventListener("webkitmouseforcewillbegin", cancel);
    return () => svg.removeEventListener("webkitmouseforcewillbegin", cancel);
  }, [editing]);

  const className = ["trk-diagram", editing && "trk-diagram--editing", nav.grab && "trk-diagram--grab", nav.panning && "trk-diagram--panning"].filter(Boolean).join(" ");
  // The canvas is measured before anything is drawn on it.
  if (!nav.view || !nav.size) return <svg ref={svgRef} className={className} role="img" aria-label="Track diagram" />;
  const { zoom } = nav.view;
  const area = viewBounds(nav.view, nav.size);
  const px = (n: number) => n / zoom;
  const width = area.maxX - area.minX;
  const height = area.maxY - area.minY;
  const shown = (layer?: string) => !layer || !hiddenLayers?.has(layer);
  const blocks = new Map(plan.blocks.map((b) => [b.id, b]));
  const tracks = plan.tracks.filter((t) => shown(t.layer));
  const junctions = plan.junctions.filter((j) => shown(j.layer));
  const pos = (id: string) => index.points.get(id);

  // Each block's longest track carries its name tag, or its train's.
  const longest = new Map<string, { track: PlanTrack; length: number }>();
  for (const t of tracks) {
    const a = pos(t.a);
    const b = pos(t.b);
    if (!t.block || !a || !b) continue;
    const length = arcInfo(a, b, t.angle).length;
    if (length > (longest.get(t.block)?.length ?? -1)) longest.set(t.block, { track: t, length });
  }
  const tags: { id: string; at: Vec; text: string; train: boolean }[] = [];
  for (const [id, { track }] of longest) {
    const at = trackMiddle(index, track);
    const block = blocks.get(id);
    if (!at) continue;
    if (editing) tags.push({ id, at, text: block?.name ?? id, train: false });
    else {
      // A tracked train is named where it is; one whose place in its
      // block isn't known yet, in its block.
      const names = tracked ? tracked.filter((t) => !t.line?.length && t.block === id).map((t) => t.name) : trainsIn(block, entities, props.trains);
      if (names.length > 0) tags.push({ id, at, text: names.join(", "), train: true });
    }
  }
  const placed = editing ? [] : (tracked ?? []).filter((t) => t.line?.length);
  for (const t of placed) {
    const at = lineMiddle(t.line!);
    if (at) tags.push({ id: `train-${t.id}`, at, text: t.name, train: true });
  }

  const bumpers = plan.points.filter((p) => p.bumper && index.at.get(p.id)?.length === 1);

  // Each stop marker: a line across the track and, beside it on the
  // right of the trains it is for, a flag pointing the way they run.
  const markers = (plan.markers ?? []).flatMap((m) => {
    const place = markerPlace(index, m);
    if (!place || !shown(index.tracks.get(m.track)?.layer)) return [];
    const across = unit(place.heading + 90);
    const ahead = unit(place.heading);
    const flag = add(place.at, scale(across, px(11)));
    const line = [add(place.at, scale(across, -px(6))), add(place.at, scale(across, px(7)))];
    const tip = add(flag, scale(ahead, px(5)));
    const back = add(flag, scale(ahead, -px(3)));
    const triangle = [tip, add(back, scale(across, px(3.5))), add(back, scale(across, -px(3.5)))];
    return [{ m, at: place.at, flag, line, triangle, word: compassWord(place.heading) }];
  });

  function pathOf(ref: ElementRef): string | undefined {
    if (ref.kind === "track") {
      const t = index.tracks.get(ref.id);
      const a = t && pos(t.a);
      const b = t && pos(t.b);
      return a && b ? trackPath(a, b, t.angle) : undefined;
    }
    if (ref.kind === "junction") {
      const j = index.junctions.get(ref.id);
      const ends = j?.ends.map(pos);
      if (!j || !ends || ends.some((e) => !e)) return undefined;
      const center = junctionCenter(j, ends as Vec[]);
      return KINDS[j.kind].routes.map((r) => routePath(j.kind, ends as Vec[], center, r)).join("");
    }
    if (ref.kind === "platform") {
      const p = plan.platforms.find((x) => x.id === ref.id);
      return (
        p &&
        `M${corners(p, p.length, p.width, p.angle)
          .map((c) => `${c.x} ${c.y}`)
          .join("L")}Z`
      );
    }
    if (ref.kind === "marker") {
      const mk = markers.find((x) => x.m.id === ref.id);
      const r = px(9);
      return mk && `M${mk.flag.x - r} ${mk.flag.y}a${r} ${r} 0 1 0 ${2 * r} 0a${r} ${r} 0 1 0 ${-2 * r} 0`;
    }
    const l = plan.labels.find((x) => x.id === ref.id);
    return l && `M${l.x} ${l.y - px(9)}h${px(l.text.length * 7 + 4)}v${px(18)}h${-px(l.text.length * 7 + 4)}Z`;
  }
  function legPath(leg: TrackLeg): string | undefined {
    if (!leg.junction) return pathOf({ kind: "track", id: leg.piece });
    const j = index.junctions.get(leg.piece);
    const ends = j?.ends.map(pos);
    const r = j && KINDS[j.kind].routes[leg.route ?? 0];
    if (!j || !r || !ends || ends.some((e) => !e)) return undefined;
    return routePath(j.kind, ends as Vec[], junctionCenter(j, ends as Vec[]), r);
  }
  const lit = editing ? [] : (props.routes ?? []).map((r) => ({ id: r.id, state: r.state, d: heldLegs(r).map(legPath).join("") })).filter((r) => r.d);

  const selectedPath = props.selected?.map(pathOf).join("");
  const hoverPath = props.hover && !props.selected?.some((r) => sameRef(r, props.hover)) && pathOf(props.hover);
  const grid = editing ? gridSpacing(plan.grid, zoom) : 0;

  return (
    <svg
      ref={svgRef}
      className={className}
      viewBox={`${area.minX} ${area.minY} ${width} ${height}`}
      role="img"
      aria-label="Track diagram"
      {...nav.handlers}
      // The plan is a drawing surface while editing: a right-click there
      // opens no menu.
      onContextMenu={editing ? (e) => e.preventDefault() : undefined}
    >
      {props.background && (
        <image
          className="trk-background"
          href={backgroundURL(props.background.image)}
          x={props.background.x}
          y={props.background.y}
          width={props.background.width}
          height={props.background.height}
          opacity={props.background.opacity}
          preserveAspectRatio="none"
          transform={
            props.background.rotation
              ? `rotate(${props.background.rotation} ${props.background.x + props.background.width / 2} ${props.background.y + props.background.height / 2})`
              : undefined
          }
          // Swapping light and dark, then turning the hues back round,
          // keeps colored lines their color.
          style={props.background.invert ? { filter: "invert(1) hue-rotate(180deg)" } : undefined}
        />
      )}
      {grid > 0 && (
        <>
          <defs>
            <pattern id="trk-grid" x={0} y={0} width={grid} height={grid} patternUnits="userSpaceOnUse">
              <path d={`M${grid} 0H0V${grid}`} fill="none" stroke="var(--enamel-line)" strokeWidth={px(1)} />
            </pattern>
          </defs>
          <rect x={area.minX} y={area.minY} width={width} height={height} fill="url(#trk-grid)" />
        </>
      )}

      <g className="trk-platforms">
        {plan.platforms
          .filter((p) => shown(p.layer))
          .map((p) => (
            <polygon
              key={p.id}
              data-kind="platform"
              data-id={p.id}
              points={corners(p, p.length, p.width, p.angle)
                .map((c) => `${c.x},${c.y}`)
                .join(" ")}
            />
          ))}
      </g>

      {lit.map((r) => (
        <path key={r.id} className={`trk-route trk-route--${r.state}`} d={r.d} vectorEffect="non-scaling-stroke" />
      ))}
      {hoverPath && <path className="trk-halo trk-halo--hover" d={hoverPath} vectorEffect="non-scaling-stroke" />}
      {selectedPath && <path className="trk-halo" d={selectedPath} vectorEffect="non-scaling-stroke" />}

      <g className="trk-track" fill="none" strokeWidth={TRACK} strokeLinecap="round">
        {tracks.map((t) => {
          const a = pos(t.a);
          const b = pos(t.b);
          if (!a || !b) return null;
          const block = t.block ? blocks.get(t.block) : undefined;
          return (
            <path key={t.id} d={trackPath(a, b, t.angle)} stroke={toneColor[toneOf(t.block, blocks, entities, editing, activeBlock)]} vectorEffect="non-scaling-stroke">
              <title>{block ? `${block.name}: ${blockState(block, entities)}` : "Not in a block"}</title>
            </path>
          );
        })}
        {junctions.map((j) => (
          <Junction key={j.id} j={j} index={index} tone={toneOf(j.block, blocks, entities, editing, activeBlock)} entities={entities} blocks={blocks} editing={editing} />
        ))}
        {bumpers.map((p) => {
          const h = outward(index, index.at.get(p.id)![0]);
          if (h === undefined) return null;
          const across = scale(unit(h + 90), px(6));
          const a = add(p, across);
          const b = add(p, scale(across, -1));
          return <path key={p.id} d={`M${a.x} ${a.y}L${b.x} ${b.y}`} stroke="var(--ivory-dim)" vectorEffect="non-scaling-stroke" />;
        })}
      </g>

      <g className="trk-markers">
        {markers.map(({ m, line, triangle, word }) => (
          <g key={m.id} className={props.stops?.has(m.id) ? "trk-marker trk-marker--target" : "trk-marker"}>
            <title>{`Stop marker for trains running ${word}${m.length ? `, up to ${m.length} mm long` : ""}`}</title>
            <path d={`M${line[0].x} ${line[0].y}L${line[1].x} ${line[1].y}`} strokeWidth={2} vectorEffect="non-scaling-stroke" />
            <polygon points={triangle.map((p) => `${p.x},${p.y}`).join(" ")} />
          </g>
        ))}
      </g>

      <g className="trk-trains">
        {placed.map((t) => {
          const points = t.line!.map((p) => `${p.x},${p.y}`).join(" ");
          const arrow = leadArrow(t, px(14));
          return (
            <g key={t.id} className={t.seen ? "trk-trainline" : "trk-trainline trk-trainline--lost"}>
              <title>{trainTitle(t, blocks)}</title>
              <polyline className="trk-trainline__edge" points={points} strokeWidth={9} vectorEffect="non-scaling-stroke" />
              <polyline className="trk-trainline__body" points={points} strokeWidth={5} vectorEffect="non-scaling-stroke" />
              {arrow && <polygon className="trk-trainline__lead" points={arrow.map((p) => `${p.x},${p.y}`).join(" ")} />}
              {t.decoder && <circle className="trk-trainline__decoder" cx={t.decoder.x} cy={t.decoder.y} r={px(2.5)} />}
            </g>
          );
        })}
      </g>

      {plan.labels
        .filter((l) => shown(l.layer))
        .map((l) => (
          <text key={l.id} className="trk-label" x={l.x} y={l.y} fontSize={px(13)} dominantBaseline="central">
            {l.text}
          </text>
        ))}

      {tags.map((tag) => {
        const w = px(tag.text.length * 6.5 + 10);
        return tag.train ? (
          <g key={`g${tag.id}`} className="trk-train">
            <rect x={tag.at.x - w / 2} y={tag.at.y - px(22)} width={w} height={px(16)} rx={px(3)} />
            <text x={tag.at.x} y={tag.at.y - px(14)} fontSize={px(11)} textAnchor="middle" dominantBaseline="central">
              {tag.text}
            </text>
          </g>
        ) : (
          <text
            key={`g${tag.id}`}
            className={tag.id === activeBlock ? "trk-blockname trk-blockname--active" : "trk-blockname"}
            x={tag.at.x}
            y={tag.at.y + px(14)}
            fontSize={px(10)}
            textAnchor="middle"
          >
            {tag.text}
          </text>
        );
      })}

      {/* What the pointer picks: wide lines along the track, over
          everything drawn but the marks. */}
      {(editing || props.onTurnout) && (
        <g className={editing ? "trk-hits" : "trk-hits trk-hits--switches"} fill="none" stroke="transparent" strokeWidth={HIT}>
          {editing &&
            tracks.map((t) => {
              const a = pos(t.a);
              const b = pos(t.b);
              return a && b && <path key={t.id} data-kind="track" data-id={t.id} d={trackPath(a, b, t.angle)} vectorEffect="non-scaling-stroke" />;
            })}
          {junctions
            .filter((j) => editing || (j.switch && entities[j.switch]))
            .map((j) => {
              const ends = j.ends.map(pos);
              if (ends.some((e) => !e)) return null;
              const center = junctionCenter(j, ends as Vec[]);
              const ent = j.switch ? entities[j.switch] : undefined;
              return (
                <path
                  key={j.id}
                  data-kind="junction"
                  data-id={j.id}
                  d={KINDS[j.kind].routes.map((r) => routePath(j.kind, ends as Vec[], center, r)).join("")}
                  vectorEffect="non-scaling-stroke"
                  onClick={!editing && ent ? () => props.onTurnout?.(j) : undefined}
                >
                  {!editing && ent && (
                    <title>
                      {props.locked?.has(j.id)
                        ? `${ent.name}: ${ent.state}, locked by the route ${props.locked.get(j.id)}.`
                        : `${ent.name}: ${ent.state}. Click to ${ent.state === "closed" ? "throw" : "close"} it.`}
                    </title>
                  )}
                </path>
              );
            })}
          {editing && markers.map(({ m, flag }) => <circle key={m.id} data-kind="marker" data-id={m.id} cx={flag.x} cy={flag.y} r={px(8)} fill="transparent" stroke="none" />)}
          {editing &&
            plan.labels
              .filter((l) => shown(l.layer))
              .map((l) => (
                <rect key={l.id} data-kind="label" data-id={l.id} x={l.x} y={l.y - px(9)} width={px(l.text.length * 7 + 4)} height={px(18)} fill="transparent" stroke="none" />
              ))}
        </g>
      )}

      {props.marks?.map((m, i) => (
        <circle key={`m${i}`} className="trk-open" cx={m.x} cy={m.y} r={px(4)} />
      ))}
      {props.preview?.map((d, i) => (
        <path key={`v${i}`} className="trk-preview" d={d} vectorEffect="non-scaling-stroke" />
      ))}
      {props.snapTo && <circle className="trk-snap" cx={props.snapTo.x} cy={props.snapTo.y} r={px(7)} vectorEffect="non-scaling-stroke" />}
      {props.box && (
        <rect
          className="trk-box"
          x={props.box.minX}
          y={props.box.minY}
          width={props.box.maxX - props.box.minX}
          height={props.box.maxY - props.box.minY}
          vectorEffect="non-scaling-stroke"
        />
      )}
    </svg>
  );
}

function Junction({
  j,
  index,
  tone,
  entities,
  blocks,
  editing,
}: {
  j: PlanJunction;
  index: PlanIndex;
  tone: TrackTone;
  entities: Record<string, Entity>;
  blocks: Map<string, TrackBlock>;
  editing: boolean;
}) {
  const ends = j.ends.map((e) => index.points.get(e));
  if (ends.some((e) => !e)) return null;
  const center = junctionCenter(j, ends as Vec[]);
  const color = toneColor[tone];
  const block = j.block ? blocks.get(j.block) : undefined;
  const title = block ? `${block.name}: ${blockState(block, entities)}` : "Not in a block";
  // A turnout shows the way it is set lit and the other way unlit, as on
  // a panel; until its position is known every way stays lit.
  const ent = j.switch ? entities[j.switch] : undefined;
  const lined = !editing && ent && (ent.state === "closed" || ent.state === "thrown") ? ent.state : undefined;
  const routes = [...KINDS[j.kind].routes].sort((a, b) => Number(a.position === lined) - Number(b.position === lined));
  return (
    <g>
      <title>{ent ? `${title}. ${ent.name}: ${ent.state}` : `${title}. ${KINDS[j.kind].name}`}</title>
      {routes.map((r) => (
        <path
          key={`${r.from}-${r.to}`}
          d={routePath(j.kind, ends as Vec[], center, r)}
          stroke={lined && r.position && r.position !== lined ? "var(--lamp-off)" : color}
          vectorEffect="non-scaling-stroke"
        />
      ))}
    </g>
  );
}
