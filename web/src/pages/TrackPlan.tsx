import { type PointerEvent as ReactPointerEvent, type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router";
import { ApiError, api, type Entity, type EntityKind, type PlanJunction, type TrackBackground, type TrackBlock, type TrackPlan, type TrackPlanState, type TrackedTrain, type Train, type Vec } from "../api";
import { type LampColor, Status } from "../components/Lamp";
import { Empty, Page } from "../components/Page";
import { TrackDiagram, ZOOM_STEP, blockState, trainsIn } from "../components/TrackDiagram";
import { useBus } from "../store";
import {
  type Bounds,
  type ElementRef,
  type End,
  KATO_RADII,
  KINDS,
  PIECES,
  addJunction,
  addTrack,
  allRefs,
  arcFrom,
  arcInfo,
  backgroundBounds,
  corners,
  crossoverSide,
  dist,
  endNear,
  exists,
  freeEnds,
  headingOf,
  idMaker,
  indexPlan,
  junctionCenter,
  layerRefs,
  makeID,
  mergeTrack,
  moveSelection,
  normalizeDegrees,
  openEnds,
  placePiece,
  planBounds,
  refsInBox,
  removeElements,
  round,
  routePath,
  sameRef,
  selectionMiddle,
  setBlock,
  setLayer,
  snap,
  straightFrom,
  sub,
  tangentAngle,
  trackPath,
  turnDiff,
  turnSelection,
  union,
  unit,
  updateElement,
} from "../trackgeom";
import { MAX_ZOOM, MIN_ZOOM, type View, zoomBy } from "../trackview";

type Tool = "select" | "track" | "curve" | "piece" | "platform" | "label" | "block" | "erase" | "background";

const TOOLS: { tool: Tool; label: string; key: string; hint: string }[] = [
  { tool: "select", label: "Select", key: "v", hint: "Click to select, Shift-click to add or take away, or drag a box around what to select; ⌘A selects everything. Drag the selection to move it; Delete or Backspace removes it." },
  { tool: "track", label: "Straight", key: "t", hint: "Drag from the end of a track to carry it on straight, or from anywhere to lay a straight between grid points. Let go on another end to join it." },
  { tool: "curve", label: "Curve", key: "c", hint: "Drag from the end of a track to curve it at the radius below, a step at a time. Let go on another end to join it with the curve that fits." },
  { tool: "piece", label: "Turnout", key: "w", hint: "Click the end of a track to put the piece chosen below there. Its other ends join any track already in place. A crossover goes to the track beside, or with track on both sides, to the side of the end the pointer is on." },
  { tool: "platform", label: "Platform", key: "p", hint: "Drag along where a platform goes, from one end to the other." },
  { tool: "label", label: "Label", key: "l", hint: "Click to write a label there; change the text on the right." },
  { tool: "block", label: "Block", key: "b", hint: "Drag over track to put it in the block chosen under Blocks. Hold Option or Alt to take track out of its block." },
  { tool: "erase", label: "Erase", key: "e", hint: "Click or drag over track, platforms and labels to remove them." },
  { tool: "background", label: "Background", key: "g", hint: "Drag to move the background image. Set its size and how faint it is under Background." },
];

const HISTORY = 100;
/** How near, in pixels, the pointer must come to a track end to snap to it. */
const SNAP_PX = 12;

type Draft = TrackPlan;

interface Session {
  /** The revision the edit started from. */
  base: string;
  draft: Draft;
  past: Draft[];
  future: Draft[];
  /** What the last change was to, when it was one of a run of changes
   * (a slider moving) that undo takes back as one. */
  run?: string;
}

/** An edit in progress, kept while another page is open. */
let keptSession: Session | undefined;

const message = (err: unknown) => (err instanceof Error ? err.message : String(err));

/** The part of the plan to draw: everything on it with a margin around,
 * at least min millimeters across. */
function areaOf(plan: Draft, background: TrackBackground | undefined, margin: number, min: Vec): Bounds {
  const b = union(planBounds(plan), backgroundBounds(background));
  const area = b ? { minX: b.minX - margin, minY: b.minY - margin, maxX: b.maxX + margin, maxY: b.maxY + margin } : { minX: 0, minY: 0, maxX: 0, maxY: 0 };
  area.maxX = Math.max(area.maxX, area.minX + min.x);
  area.maxY = Math.max(area.maxY, area.minY + min.y);
  return area;
}

function entitiesOf(entities: Record<string, Entity>, kind: EntityKind): Entity[] {
  return Object.values(entities)
    .filter((e) => e.kind === kind)
    .sort((a, b) => a.name.localeCompare(b.name));
}

const COMPASS = ["east", "southeast", "south", "southwest", "west", "northwest", "north", "northeast"];
/** A heading as the nearest compass word. */
const compass = (heading: number) => COMPASS[Math.round(normalizeDegrees(heading) / 45) % 8];

const HIDDEN_KEY = "interlock.trackplan.hiddenLayers";

/** The layers hidden on this browser. */
function useHiddenLayers(): [ReadonlySet<string>, (id: string, hidden: boolean) => void] {
  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => {
    try {
      return new Set(JSON.parse(localStorage.getItem(HIDDEN_KEY) ?? "[]") as string[]);
    } catch {
      return new Set();
    }
  });
  const set = (id: string, hide: boolean) => {
    const next = new Set(hidden);
    if (hide) next.add(id);
    else next.delete(id);
    setHidden(next);
    try {
      localStorage.setItem(HIDDEN_KEY, JSON.stringify([...next]));
    } catch {
      // Kept for this visit only.
    }
  };
  return [hidden, set];
}

export function TrackPlanPage() {
  const { trackPlan, entities, loaded } = useBus();
  const [session, setSession] = useState<Session | undefined>(keptSession);
  const layers = useHiddenLayers();
  useEffect(() => {
    keptSession = session;
  }, [session]);

  if (!trackPlan) {
    return (
      <Page title="Track" lead="The layout's track plan.">
        <p className="muted">{loaded ? "The track plan is not available from this server." : "Loading the track plan."}</p>
      </Page>
    );
  }
  if (session) return <Editor saved={trackPlan} session={session} setSession={setSession} entities={entities} layers={layers} />;
  return <Viewer state={trackPlan} entities={entities} layers={layers} onEdit={() => setSession({ base: trackPlan.revision, draft: trackPlan.plan, past: [], future: [] })} />;
}

/** Zooming about the middle of the plan, and Fit to show all of it. */
function Zoom({ view, setView }: { view?: View; setView: (v: View | undefined) => void }) {
  return (
    <span className="trk-zoom" role="group" aria-label="Zoom">
      <button type="button" className="btn btn--sm btn--quiet" disabled={!view || view.zoom <= MIN_ZOOM} onClick={() => view && setView(zoomBy(view, 1 / ZOOM_STEP))} aria-label="Zoom out" title="Zoom out (-)">
        −
      </button>
      <button type="button" className="btn btn--sm btn--quiet" disabled={!view || view.zoom >= MAX_ZOOM} onClick={() => view && setView(zoomBy(view, ZOOM_STEP))} aria-label="Zoom in" title="Zoom in (+)">
        +
      </button>
      <button type="button" className="btn btn--sm btn--quiet" onClick={() => setView(undefined)} title="Show the whole plan (0)">
        Fit
      </button>
    </span>
  );
}

/** Checkboxes that show and hide the plan's layers on this browser. */
function LayerToggles({ plan, layers: [hidden, setHidden] }: { plan: TrackPlan; layers: ReturnType<typeof useHiddenLayers> }) {
  if (plan.layers.length === 0) return null;
  return (
    <div className="trk-layers" role="group" aria-label="Layers shown">
      {plan.layers.map((l) => (
        <label key={l.id} className="check">
          <input type="checkbox" checked={!hidden.has(l.id)} onChange={(e) => setHidden(l.id, !e.target.checked)} />
          {l.name}
        </label>
      ))}
    </div>
  );
}

// Operating: the plan lit by the layout, turnouts set by a click.

function Viewer({ state, entities, layers, onEdit }: { state: TrackPlanState; entities: Record<string, Entity>; layers: ReturnType<typeof useHiddenLayers>; onEdit: () => void }) {
  const { plan } = state;
  const { tracked, trains } = useBus();
  const trackedList = useMemo(() => Object.values(tracked), [tracked]);
  const background = plan.background?.operating ? plan.background : undefined;
  const index = useMemo(() => indexPlan(plan), [plan]);
  const area = useMemo(() => areaOf(plan, background, 60, { x: 300, y: 150 }), [plan, background]);
  const [view, setView] = useState<View>();
  const [error, setError] = useState<string>();
  const lead = "The layout's track plan, lit by what the detectors and turnouts report.";

  if (plan.tracks.length === 0 && plan.junctions.length === 0) {
    return (
      <Page title="Track" lead={lead}>
        <Empty title="No track plan yet">
          <p>Bring in the layout's track from RailModeller Pro, or draw it to scale; then divide it into blocks and link the blocks to their detectors and the turnouts to their switch machines.</p>
          <p>
            <button type="button" className="btn btn--primary" onClick={onEdit}>
              Make the track plan
            </button>
          </p>
        </Empty>
      </Page>
    );
  }

  async function toggle(j: PlanJunction) {
    const ent = j.switch ? entities[j.switch] : undefined;
    if (!ent) return;
    setError(undefined);
    try {
      await api.command(ent.id, ent.state === "closed" ? "throw" : "close");
    } catch (err) {
      setError(`${ent.name}: ${message(err)}`);
    }
  }

  return (
    <Page
      title="Track"
      lead={lead}
      actions={
        <>
          <Zoom view={view} setView={setView} />
          <button type="button" className="btn" onClick={onEdit}>
            Edit track plan
          </button>
        </>
      }
    >
      {error && <p className="note note--error">{error}</p>}
      <LayerToggles plan={plan} layers={layers} />
      {/* As tall as the plan is at the page's width, up to most of the
          window. */}
      <div className="panel trk-canvas" style={{ aspectRatio: `${area.maxX - area.minX} / ${area.maxY - area.minY}` }}>
        <TrackDiagram
          plan={plan}
          index={index}
          entities={entities}
          fit={area}
          view={view}
          onView={setView}
          hiddenLayers={layers[0]}
          background={background}
          onTurnout={toggle}
          tracked={trackedList}
          trains={trains}
        />
      </div>
      <p className="trk-legend muted">
        Click a turnout or crossover to throw or close it. Drag the plan to move it; pinch, or scroll with ⌘ or Ctrl held, to zoom. A train is drawn where tracking puts it once it has run from one block into the next, pointed the way it goes, with a dot where its decoder is.
      </p>
      <TrainTable plan={plan} tracked={trackedList} />
      <BlockTable state={state} entities={entities} tracked={trackedList} trains={trains} />
    </Page>
  );
}

const stateColor = (state: string): LampColor => {
  switch (state) {
    case "occupied":
      return "red";
    case "clear":
      return "green";
    case "no detector linked":
      return "off";
  }
  return "amber";
};

/** A block's length: as measured when that was given, else as drawn. */
function lengthOf(b: TrackBlock, topology?: TrackPlanState["topology"]): ReactNode {
  if (b.length_mm) return `${b.length_mm} mm`;
  const drawn = topology?.blocks.find((x) => x.id === b.id)?.length;
  return drawn ? `${Math.round(drawn)} mm drawn` : <span className="faint">no track</span>;
}

/** The trains tracking follows: where each is and how it goes. */
function TrainTable({ plan, tracked }: { plan: TrackPlan; tracked: TrackedTrain[] }) {
  if (tracked.length === 0) return null;
  const names = new Map(plan.blocks.map((b) => [b.id, b.name]));
  const sorted = [...tracked].sort((a, b) => a.name.localeCompare(b.name));
  return (
    <>
      <h2 className="trk-h2">Trains</h2>
      <div className="panel">
        <table className="table">
          <thead>
            <tr>
              <th>Train</th>
              <th>Where</th>
              <th>Going</th>
              <th className="num">Speed</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((t) => {
              const where = t.blocks.map((b) => names.get(b) ?? b).join(", ");
              const notes: string[] = [];
              if (!t.placed && t.block) notes.push("Where in the block isn't known until it runs into the next one.");
              if (!t.seen && t.block) notes.push("Not seen by a detector just now.");
              if (t.roster?.length && !t.length)
                notes.push(t.formation ? "Give its locomotives' and cars' lengths to place the rest of the train." : "Give its length on its roster page, or make it up into a train with its cars, to place the rest of it.");
              if (t.roster?.length && !t.momentum) notes.push("Read its decoder's CVs 3 and 4 to follow it better as it speeds up and slows down.");
              return (
                <tr key={t.id}>
                  <td>
                    <div className="cell-title">
                      {t.formation ? (
                        <Link to={`/roster/trains/${encodeURIComponent(t.formation)}`}>{t.name}</Link>
                      ) : t.roster?.length === 1 ? (
                        <Link to={`/roster/${encodeURIComponent(t.roster[0])}`}>{t.name}</Link>
                      ) : (
                        t.name
                      )}
                    </div>
                    {notes.map((n) => (
                      <div key={n} className="cell-sub">
                        {n}
                      </div>
                    ))}
                  </td>
                  <td>{where || <span className="faint">not in a block</span>}</td>
                  <td>{t.placed ? (t.direction === "reverse" ? "Reverse" : "Forward") : <span className="faint">not known</span>}</td>
                  <td className="num">
                    {t.top_speed ? `${Math.round(t.speed)} mm/s` : <span className="faint">not measured</span>}
                    {t.top_speed ? <div className="cell-sub">{Math.round(t.top_speed)} mm/s at full</div> : null}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}

/** The trains in a block: those tracking has there, front or rear,
 * else those its reporter sees. */
function trainsInBlock(b: TrackBlock, entities: Record<string, Entity>, tracked: TrackedTrain[], trains: Record<string, Train>): string {
  const here = tracked.filter((t) => t.blocks.includes(b.id)).map((t) => (t.block === b.id || !t.placed ? t.name : `${t.name} (${t.blocks[0] === b.id ? "front" : "rear"})`));
  return (here.length > 0 ? here : trainsIn(b, entities, trains)).join(", ");
}

function BlockTable({ state, entities, tracked, trains }: { state: TrackPlanState; entities: Record<string, Entity>; tracked: TrackedTrain[]; trains: Record<string, Train> }) {
  const { plan, topology } = state;
  if (plan.blocks.length === 0) return null;
  const stations = new Map(plan.stations.map((s) => [s.id, s.name]));
  return (
    <>
      <h2 className="trk-h2">Blocks</h2>
      <div className="panel">
        <table className="table">
          <thead>
            <tr>
              <th>Block</th>
              <th>State</th>
              <th>Train</th>
              <th className="num">Length</th>
            </tr>
          </thead>
          <tbody>
            {plan.blocks.map((b) => {
              const st = blockState(b, entities);
              return (
                <tr key={b.id}>
                  <td>
                    <div className="cell-title">{b.name}</div>
                    {b.station && <div className="cell-sub">{stations.get(b.station)}</div>}
                  </td>
                  <td>
                    <Status color={stateColor(st)}>{st}</Status>
                  </td>
                  <td>{trainsInBlock(b, entities, tracked, trains) || <span className="faint">none reported</span>}</td>
                  <td className="num">{lengthOf(b, topology)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}

// Editing.

interface Stroke {
  base: Draft;
  /** Where the pointer went down. */
  start: Vec;
  /** The track end it went down on, for the track tools. */
  end?: End;
  moved: boolean;
  unassign: boolean;
  /** For the block and erase tools: the plan as changed so far. */
  plan: Draft;
  done: Set<string>;
  background?: TrackBackground;
  /** For the drawing tools: what letting go adds. */
  result?: Draft;
  /** For the select tool: moving the selection, or drawing a box to
   * select what it takes in. */
  mode?: "move" | "box";
  refs?: ElementRef[];
  /** The element the pointer went down on, selected if no box is drawn. */
  clicked?: ElementRef;
  additive?: boolean;
  box?: Bounds;
}

interface Options {
  radius: number;
  step: number;
  piece: string;
  platformWidth: number;
}

function Editor({
  saved,
  session,
  setSession,
  entities,
  layers,
}: {
  saved: TrackPlanState;
  session: Session;
  setSession: (s: Session | undefined) => void;
  entities: Record<string, Entity>;
  layers: ReturnType<typeof useHiddenLayers>;
}) {
  const { draft } = session;
  const dirty = session.past.length > 0;
  const [hidden] = layers;
  const [tool, setTool] = useState<Tool>(draft.tracks.length ? "select" : "track");
  const [selection, setSelection] = useState<ElementRef[]>([]);
  const [box, setBox] = useState<Bounds>();
  const [hover, setHover] = useState<ElementRef>();
  const [activeBlock, setActiveBlock] = useState<string | undefined>(draft.blocks[0]?.id);
  const [activeLayer, setActiveLayer] = useState<string>();
  const [options, setOptions] = useState<Options>({ radius: 216, step: 15, piece: "t4r", platformWidth: 39 });
  const [preview, setPreview] = useState<{ paths: string[]; snapTo?: Vec }>({ paths: [] });
  const [note, setNote] = useState<string>();
  const [error, setError] = useState<string>();
  const [conflict, setConflict] = useState<string>();
  const [saving, setSaving] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const [view, setView] = useState<View>();
  const stroke = useRef<Stroke | undefined>(undefined);
  const sessionRef = useRef(session);
  sessionRef.current = session;

  const index = useMemo(() => indexPlan(draft), [draft]);
  // What Fit shows: the plan, with room to draw past its edges.
  const area = areaOf(draft, draft.background, 300, { x: 1500, y: 900 });
  const reach = SNAP_PX / (view?.zoom ?? 1);
  const grid = draft.grid || 33;
  const shape = PIECES.find((p) => p.id === options.piece) ?? PIECES[0];

  const update = (change: (s: Session) => Session) => setSession(change(sessionRef.current));
  /** Changes the plan as one step to undo; a change in the same run as
   * the one before joins its step. */
  const commit = (next: Draft, run?: string) =>
    update((s) => (run && s.run === run ? { ...s, draft: next } : { ...s, draft: next, past: [...s.past, s.draft].slice(-HISTORY), future: [], run }));
  const undo = () => update((s) => (s.past.length ? { ...s, draft: s.past[s.past.length - 1], past: s.past.slice(0, -1), future: [s.draft, ...s.future], run: undefined } : s));
  const redo = () => update((s) => (s.future.length ? { ...s, draft: s.future[0], past: [...s.past, s.draft], future: s.future.slice(1), run: undefined } : s));
  const tools = TOOLS.filter((t) => t.tool !== "background" || draft.background);
  const onLayer = activeLayer && draft.layers.some((l) => l.id === activeLayer) ? activeLayer : undefined;

  // Someone else saved while nothing here was changed: follow along.
  useEffect(() => {
    if (!dirty && saved.revision !== session.base) {
      setSession({ base: saved.revision, draft: saved.plan, past: [], future: [] });
    }
  }, [dirty, saved, session.base, setSession]);

  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  // What undo took away is no longer selected.
  const current = selection.filter((r) => exists(draft, index, r));
  const select = (refs: ElementRef[]) => {
    setSelection(refs);
    if (refs.length) setTool("select");
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      if (target?.closest?.("input, select, textarea")) return;
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === "z") {
        e.preventDefault();
        if (e.shiftKey) redo();
        else undo();
      } else if (mod && e.key.toLowerCase() === "y") {
        e.preventDefault();
        redo();
      } else if (mod && e.key.toLowerCase() === "a") {
        e.preventDefault();
        select(allRefs(sessionRef.current.draft, hidden));
      } else if ((e.key === "Delete" || e.key === "Backspace") && current.length) {
        e.preventDefault();
        commit(removeElements(sessionRef.current.draft, current));
        setSelection([]);
      } else if (e.key === "Escape") {
        setSelection([]);
      } else if (e.key.startsWith("Arrow") && current.length && tool === "select") {
        // A millimeter at a time, a grid space with Shift; a run of
        // nudges is one step to undo.
        e.preventDefault();
        const d = e.shiftKey ? grid : 1;
        const by = { ArrowLeft: { x: -d, y: 0 }, ArrowRight: { x: d, y: 0 }, ArrowUp: { x: 0, y: -d }, ArrowDown: { x: 0, y: d } }[e.key];
        if (by) commit(moveSelection(sessionRef.current.draft, current, by), "nudge");
      } else if (!mod && !e.altKey) {
        const t = tools.find((t) => t.key === e.key);
        if (t) setTool(t.tool);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  function show(s: Stroke, plan: Draft) {
    s.plan = plan;
    setSession({ ...sessionRef.current, draft: { ...plan, background: s.background ?? plan.background } });
  }

  /** Paints or erases what the pointer is over, once per stroke. */
  function touch(s: Stroke, target?: ElementRef) {
    if (!target || s.done.has(`${target.kind}:${target.id}`)) return;
    s.done.add(`${target.kind}:${target.id}`);
    if (tool === "erase") {
      show(s, removeElements(s.plan, [target]));
    } else if (tool === "block" && (target.kind === "track" || target.kind === "junction")) {
      if (!activeBlock && !s.unassign) {
        setNote("Add a block under Blocks first, then paint track into it.");
        return;
      }
      show(s, setBlock(s.plan, [target], s.unassign ? undefined : activeBlock));
    }
  }

  /** Where the piece tool would put its piece, at the end under the
   * pointer; a crossover goes to the track beside it. */
  function placeAt(end: End, pointer: Vec) {
    return placePiece(shape, end.point, end.heading, shape.across ? crossoverSide(index, end, pointer) : 1);
  }

  function onPointerDown(at: Vec, e: ReactPointerEvent<SVGSVGElement>, target?: ElementRef) {
    if (e.button !== 0) return;
    setNote(undefined);
    if (tool === "select") {
      const picked = target && current.some((r) => sameRef(r, target));
      if (e.shiftKey && target) {
        setSelection(picked ? current.filter((r) => !sameRef(r, target)) : [...current, target]);
        return;
      }
      // Dragging what is selected moves it; a drag from anywhere else
      // draws a box, and a click without one selects what it is on.
      e.currentTarget.setPointerCapture(e.pointerId);
      stroke.current = {
        base: draft,
        start: at,
        moved: false,
        unassign: false,
        plan: draft,
        done: new Set(),
        background: draft.background,
        mode: picked ? "move" : "box",
        refs: current,
        clicked: target,
        additive: e.shiftKey,
      };
      return;
    }
    if (tool === "label") {
      const p = snap(at, grid);
      const id = idMaker(draft)("l");
      commit({ ...draft, labels: [...draft.labels, { id, x: p.x, y: p.y, text: "Label", ...(onLayer ? { layer: onLayer } : {}) }] });
      setSelection([{ kind: "label", id }]);
      return;
    }
    if (tool === "piece") {
      const end = endNear(index, at, reach);
      if (!end) {
        setNote("Click the end of a track: the piece goes on from there.");
        return;
      }
      const placed = placeAt(end, at);
      // A left hand crossover lists the clicked end third, not first.
      const ends = placed.ends.map((v) => (dist(v, end.point) < 0.01 ? end.point.id : v));
      commit(addJunction(draft, index, shape.kind, ends, placed.at, onLayer));
      setPreview({ paths: [] });
      return;
    }
    const end = tool === "track" || tool === "curve" ? endNear(index, at, reach) : undefined;
    if (tool === "curve" && !end) {
      setNote("Start a curve at the end of a track, so it carries on from it.");
      return;
    }
    e.currentTarget.setPointerCapture(e.pointerId);
    const s: Stroke = { base: draft, start: at, end, moved: false, unassign: e.altKey, plan: draft, done: new Set(), background: draft.background };
    stroke.current = s;
    touch(s, target);
  }

  function onPointerMove(at: Vec, _e: ReactPointerEvent<SVGSVGElement>, target?: ElementRef) {
    const s = stroke.current;
    if (!s) {
      if (tool === "select" || tool === "erase" || tool === "block") {
        if (!sameRef(hover, target)) setHover(target);
        return;
      }
      // Show where the track tools would start, and the piece where it
      // would go.
      const end = tool === "track" || tool === "curve" || tool === "piece" ? endNear(index, at, reach) : undefined;
      if (tool === "piece" && end) {
        const placed = placeAt(end, at);
        const center = junctionCenter({ at: placed.at }, placed.ends);
        setPreview({ paths: KINDS[shape.kind].routes.map((r) => routePath(shape.kind, placed.ends, center, r)), snapTo: end.point });
      } else if (end?.point !== preview.snapTo || preview.paths.length) {
        setPreview({ paths: [], snapTo: end?.point });
      }
      return;
    }
    s.moved = true;
    if (s.mode === "move" && s.refs) {
      const by = { x: round(at.x - s.start.x, 1), y: round(at.y - s.start.y, 1) };
      show(s, moveSelection(s.base, s.refs, by));
      setNote(`Moving ${Math.round(by.x)}, ${Math.round(by.y)} mm`);
      return;
    }
    if (s.mode === "box") {
      s.box = { minX: Math.min(s.start.x, at.x), minY: Math.min(s.start.y, at.y), maxX: Math.max(s.start.x, at.x), maxY: Math.max(s.start.y, at.y) };
      setBox(s.box);
      return;
    }
    switch (tool) {
      case "background": {
        const bg = s.base.background;
        if (!bg) return;
        s.background = { ...bg, x: round(bg.x + at.x - s.start.x, 1), y: round(bg.y + at.y - s.start.y, 1) };
        show(s, s.plan);
        return;
      }
      case "erase":
      case "block":
        touch(s, target);
        return;
      case "track":
        return drawStraight(s, at);
      case "curve":
        return drawCurve(s, at);
      case "platform":
        return drawPlatform(s, at);
    }
  }

  function drawStraight(s: Stroke, at: Vec) {
    const join = endNear(index, at, reach, s.end?.point.id);
    let a: string | Vec;
    let from: Vec;
    let b: string | Vec;
    let to: Vec;
    if (s.end) {
      a = s.end.point.id;
      from = s.end.point;
      if (join) {
        b = join.point.id;
        to = join.point;
      } else {
        const along = (at.x - from.x) * unit(s.end.heading).x + (at.y - from.y) * unit(s.end.heading).y;
        to = straightFrom(from, s.end.heading, Math.round(along));
        b = to;
      }
    } else {
      from = snap(s.start, grid);
      a = from;
      to = join ? join.point : snap(at, grid);
      b = join ? join.point.id : to;
    }
    // A click on a track end, moving the pointer a pixel or two, would
    // lay a stub a millimeter long; only a drag or a join lays track.
    if (dist(from, to) < 1 || (!join && dist(s.start, at) < reach / 2)) {
      s.result = undefined;
      setPreview({ paths: [] });
      return;
    }
    s.result = addTrack(s.base, index, a, b, 0, onLayer);
    setPreview({ paths: [trackPath(from, to)], snapTo: join?.point });
    setNote(`${Math.round(dist(from, to))} mm straight`);
  }

  function drawCurve(s: Stroke, at: Vec) {
    const end = s.end!;
    const p = end.point;
    const join = endNear(index, at, reach, p.id);
    let angle: number;
    let to: Vec;
    if (join) {
      angle = round(tangentAngle(p, end.heading, join.point), 3);
      to = join.point;
    } else {
      const ahead = unit(end.heading);
      const off = sub(at, p);
      const side = ahead.x * off.y - ahead.y * off.x >= 0 ? 1 : -1;
      const center = { x: p.x + unit(end.heading + side * 90).x * options.radius, y: p.y + unit(end.heading + side * 90).y * options.radius };
      let sweep = turnDiff(headingOf(sub(at, center)), headingOf(sub(p, center)));
      if (side * sweep < 0) sweep = 0;
      angle = Math.round(sweep / options.step) * options.step;
      to = arcFrom(p, end.heading, options.radius, angle);
    }
    if (!angle || Math.abs(angle) >= 360 || dist(p, to) < 1) {
      s.result = join && Math.abs(angle) < 1e-3 ? addTrack(s.base, index, p.id, join.point.id, 0, onLayer) : undefined;
      setPreview({ paths: s.result && join ? [trackPath(p, join.point)] : [], snapTo: join?.point });
      return;
    }
    s.result = addTrack(s.base, index, p.id, join ? join.point.id : to, angle, onLayer);
    setPreview({ paths: [trackPath(p, to, angle)], snapTo: join?.point });
    const info = arcInfo(p, to, angle);
    setNote(`${Math.abs(angle)}° ${angle > 0 ? "right" : "left"} at ${Math.round(info.radius)} mm radius, ${Math.round(info.length)} mm long`);
  }

  function drawPlatform(s: Stroke, at: Vec) {
    const a = snap(s.start, grid);
    const b = snap(at, grid);
    const length = dist(a, b);
    if (length < 1) {
      s.result = undefined;
      setPreview({ paths: [] });
      return;
    }
    const platform = {
      id: idMaker(s.base)("f"),
      x: round((a.x + b.x) / 2, 2),
      y: round((a.y + b.y) / 2, 2),
      length: round(length, 2),
      width: options.platformWidth,
      ...(headingOf(sub(b, a)) ? { angle: round(headingOf(sub(b, a)), 2) } : {}),
      ...(onLayer ? { layer: onLayer } : {}),
    };
    s.result = { ...s.base, platforms: [...s.base.platforms, platform] };
    const c = corners(platform, platform.length, platform.width, platform.angle);
    setPreview({ paths: [`M${c.map((v) => `${v.x} ${v.y}`).join("L")}Z`] });
    setNote(`${Math.round(length)} mm platform`);
  }

  /** Calls off a stroke, as when a second finger turns it into a pinch:
   * the plan goes back to how it was before it. */
  function onPointerCancel() {
    const s = stroke.current;
    stroke.current = undefined;
    setPreview({ paths: [] });
    setBox(undefined);
    setNote(undefined);
    if (s && sessionRef.current.draft !== s.base) setSession({ ...sessionRef.current, draft: s.base });
  }

  function onPointerUp() {
    const s = stroke.current;
    stroke.current = undefined;
    setPreview({ paths: [] });
    setBox(undefined);
    if (!s) return;
    if (s.mode === "move" && !s.moved && s.clicked) {
      // A click on one of several selected elements selects it alone.
      setSelection([s.clicked]);
      return;
    }
    if (s.mode === "box") {
      // Less than a few pixels across is a click.
      const b = s.box;
      const clicked = !b || Math.max(b.maxX - b.minX, b.maxY - b.minY) < reach / 2;
      const got = clicked ? (s.clicked ? [s.clicked] : []) : refsInBox(draft, index, b, hidden);
      setSelection(s.additive ? [...current, ...got.filter((r) => !current.some((c) => sameRef(c, r)))] : got);
      return;
    }
    const plan = s.result ?? (s.plan !== s.base || s.background !== s.base.background ? { ...s.plan, background: s.background } : undefined);
    if (plan) {
      setSession({ ...sessionRef.current, draft: plan, past: [...sessionRef.current.past, s.base].slice(-HISTORY), future: [], run: undefined });
    }
  }

  async function save(revision: string) {
    setSaving(true);
    setError(undefined);
    setConflict(undefined);
    try {
      const st = await api.saveTrackPlan(draft, revision);
      setSession({ base: st.revision, draft: st.plan, past: [], future: [] });
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) setConflict(typeof err.body?.revision === "string" ? err.body.revision : "");
      else setError(message(err));
    } finally {
      setSaving(false);
    }
  }

  async function loadTheirs() {
    setConflict(undefined);
    try {
      const st = await api.trackPlan();
      setSession({ base: st.revision, draft: st.plan, past: [], future: [] });
    } catch (err) {
      setError(message(err));
    }
  }

  const ends = openEnds(index);
  const hint = TOOLS.find((t) => t.tool === tool)?.hint;
  const set = (change: Partial<Options>) => setOptions({ ...options, ...change });

  return (
    <Page
      title="Track"
      lead="Editing the track plan. Nothing reaches the layout until you save."
      actions={
        <>
          <button type="button" className="btn" onClick={() => (dirty ? setConfirmDiscard(true) : setSession(undefined))}>
            {dirty ? "Discard changes" : "Done"}
          </button>
          <button type="button" className="btn btn--primary" disabled={!dirty || saving} onClick={() => void save(session.base)}>
            {saving ? "Saving" : "Save"}
          </button>
        </>
      }
    >
      {confirmDiscard && (
        <div className="note trk-note">
          <p>Discard the changes made since the plan was last saved? The saved plan stays as it is.</p>
          <p className="trk-note__actions">
            <button
              type="button"
              className="btn btn--sm"
              onClick={() => {
                setConfirmDiscard(false);
                setSession(undefined);
              }}
            >
              Discard changes
            </button>
            <button type="button" className="btn btn--sm btn--quiet" onClick={() => setConfirmDiscard(false)}>
              Keep editing
            </button>
          </p>
        </div>
      )}
      {conflict !== undefined && (
        <div className="note note--error trk-note">
          <p>Someone else saved the track plan since you started editing.</p>
          <p className="trk-note__actions">
            <button type="button" className="btn btn--sm" disabled={saving} onClick={() => void save(conflict)}>
              Save mine over theirs
            </button>
            <button type="button" className="btn btn--sm btn--quiet" onClick={() => void loadTheirs()}>
              Load theirs and lose mine
            </button>
          </p>
        </div>
      )}
      {error && <p className="note note--error">{error}</p>}

      <div className="trk-tools" role="toolbar" aria-label="Drawing tools">
        <span className="trk-tools__group" role="group" aria-label="Tool">
          {tools.map((t) => (
            <button key={t.tool} type="button" className="btn btn--sm btn--quiet" aria-pressed={tool === t.tool} onClick={() => setTool(t.tool)} title={`${t.label} (${t.key.toUpperCase()})`}>
              {t.label}
            </button>
          ))}
        </span>
        <span className="trk-tools__group" role="group" aria-label="History">
          <button type="button" className="btn btn--sm btn--quiet" disabled={!session.past.length} onClick={undo} title="Undo (⌘Z)">
            Undo
          </button>
          <button type="button" className="btn btn--sm btn--quiet" disabled={!session.future.length} onClick={redo} title="Redo (⇧⌘Z)">
            Redo
          </button>
        </span>
        <Zoom view={view} setView={setView} />
      </div>
      <div className="trk-options">
        {tool === "curve" && (
          <>
            <label className="trk-option">
              <span>Radius</span>
              <select className="input input--select" value={options.radius} onChange={(e) => set({ radius: Number(e.target.value) })}>
                {[...new Set([...KATO_RADII, options.radius])]
                  .sort((a, b) => a - b)
                  .map((r) => (
                    <option key={r} value={r}>
                      R{r}
                    </option>
                  ))}
              </select>
            </label>
            <NumberOption label="or mm" value={options.radius} min={50} onChange={(radius) => set({ radius })} />
            <label className="trk-option">
              <span>Steps of</span>
              <select className="input input--select" value={options.step} onChange={(e) => set({ step: Number(e.target.value) })}>
                {[45, 15, 7.5, 1].map((d) => (
                  <option key={d} value={d}>
                    {d}°
                  </option>
                ))}
              </select>
            </label>
          </>
        )}
        {tool === "piece" && (
          <>
            <label className="trk-option">
              <span>Piece</span>
              <select className="input input--select" value={shape.id} onChange={(e) => set({ piece: e.target.value })}>
                {PIECES.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.label}
                  </option>
                ))}
              </select>
            </label>
          </>
        )}
        {tool === "platform" && <NumberOption label="Width in mm" value={options.platformWidth} min={1} onChange={(platformWidth) => set({ platformWidth })} />}
        {(tool === "track" || tool === "platform" || tool === "label") && (
          <NumberOption label="Grid in mm" value={grid} min={1} onChange={(g) => commit({ ...draft, grid: g }, "grid")} />
        )}
        {draft.layers.length > 0 && tool !== "select" && tool !== "erase" && tool !== "block" && tool !== "background" && (
          <label className="trk-option">
            <span>On layer</span>
            <select className="input input--select" value={onLayer ?? ""} onChange={(e) => setActiveLayer(e.target.value || undefined)}>
              <option value="">None</option>
              {draft.layers.map((l) => (
                <option key={l.id} value={l.id}>
                  {l.name}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>
      <p className="trk-hint muted" aria-live="polite">
        {note ?? hint}
      </p>

      <div className="trk-editor">
        <div>
          <div className="panel trk-canvas trk-canvas--work">
            <TrackDiagram
              plan={draft}
              index={index}
              entities={entities}
              fit={area}
              view={view}
              onView={setView}
              editing
              hiddenLayers={hidden}
              activeBlock={tool === "block" ? activeBlock : undefined}
              selected={current}
              box={box}
              hover={hover}
              marks={ends}
              snapTo={preview.snapTo}
              preview={preview.paths}
              background={draft.background}
              onPointerDown={onPointerDown}
              onPointerMove={onPointerMove}
              onPointerUp={onPointerUp}
              onPointerCancel={onPointerCancel}
              onPointerLeave={() => {
                setHover(undefined);
                if (!stroke.current) setPreview({ paths: [] });
              }}
            />
          </div>
          <p className="trk-legend muted">Scroll, or drag with Space held, to move the plan; pinch, or scroll with ⌘ or Ctrl held, to zoom. 0 fits the whole plan.</p>
        </div>
        <aside className="trk-side">
          {current.length === 1 && (
            <ElementDetails
              item={current[0]}
              draft={draft}
              index={index}
              entities={entities}
              commit={commit}
              onRemove={() => {
                commit(removeElements(draft, current));
                setSelection([]);
              }}
            />
          )}
          {current.length > 1 && (
            <SelectionDetails
              refs={current}
              draft={draft}
              commit={commit}
              onRemove={() => {
                commit(removeElements(draft, current));
                setSelection([]);
              }}
            />
          )}
          {current.length > 0 && <MoveTurn refs={current} draft={draft} index={index} commit={commit} />}
          <Blocks draft={draft} entities={entities} active={activeBlock} setActive={setActiveBlock} commit={commit} topology={dirty ? undefined : saved.topology} />
          <Stations draft={draft} commit={commit} />
          <Layers draft={draft} commit={commit} layers={layers} onSelect={(id) => select(layerRefs(draft, id))} />
          <Problems ends={ends.length} saved={dirty ? undefined : saved} />
          <Import
            draft={draft}
            commit={commit}
            onImported={(refs) => {
              select(refs);
              setView(undefined);
            }}
          />
          <Background draft={draft} area={area} commit={commit} onAdded={() => setTool("background")} onRemoved={() => tool === "background" && setTool("select")} />
        </aside>
      </div>
    </Page>
  );
}

function NumberOption({ label, value, min, onChange }: { label: string; value: number; min: number; onChange: (v: number) => void }) {
  const [text, setText] = useState(String(value));
  useEffect(() => setText(String(value)), [value]);
  const done = () => {
    const v = Number(text);
    if (Number.isFinite(v) && v >= min && v !== value) onChange(v);
    else setText(String(value));
  };
  return (
    <label className="trk-option">
      <span>{label}</span>
      <input
        className="input input--num"
        type="number"
        min={min}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onBlur={done}
        onKeyDown={(e) => {
          if (e.key === "Enter") e.currentTarget.blur();
        }}
      />
    </label>
  );
}

/** A text field that changes the plan when it loses focus or on Enter,
 * so typing makes one step to undo, not one per letter. */
function CommitInput({ value, onCommit, label, type = "text", placeholder }: { value: string; onCommit: (v: string) => void; label: string; type?: "text" | "number"; placeholder?: string }) {
  const [text, setText] = useState(value);
  useEffect(() => setText(value), [value]);
  return (
    <label className="trk-field">
      <span>{label}</span>
      <input
        className={type === "number" ? "input input--num" : "input"}
        type={type}
        min={type === "number" ? 0 : undefined}
        value={text}
        placeholder={placeholder}
        onChange={(e) => setText(e.target.value)}
        onBlur={() => text !== value && onCommit(text)}
        onKeyDown={(e) => {
          if (e.key === "Enter") e.currentTarget.blur();
        }}
      />
    </label>
  );
}

function EntitySelect({ label, kind, value, entities, onChange }: { label: string; kind: EntityKind; value?: string; entities: Record<string, Entity>; onChange: (id: string | undefined) => void }) {
  const options = entitiesOf(entities, kind);
  return (
    <label className="trk-field">
      <span>{label}</span>
      <select className="input input--select" value={value ?? ""} onChange={(e) => onChange(e.target.value || undefined)}>
        <option value="">None</option>
        {value && !entities[value] && <option value={value}>{value} (not found)</option>}
        {options.map((e) => (
          <option key={e.id} value={e.id}>
            {e.name}
          </option>
        ))}
      </select>
    </label>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="panel trk-section">
      <h2>{title}</h2>
      {children}
    </section>
  );
}

function Choice({ label, value, none, options, onChange }: { label: string; value?: string; none: string; options: { id: string; name: string }[]; onChange: (v: string | undefined) => void }) {
  return (
    <label className="trk-field">
      <span>{label}</span>
      <select className="input input--select" value={value ?? ""} onChange={(e) => onChange(e.target.value || undefined)}>
        <option value="">{none}</option>
        {options.map((o) => (
          <option key={o.id} value={o.id}>
            {o.name}
          </option>
        ))}
      </select>
    </label>
  );
}

function ElementDetails({
  item,
  draft,
  index,
  entities,
  commit,
  onRemove,
}: {
  item: ElementRef;
  draft: Draft;
  index: ReturnType<typeof indexPlan>;
  entities: Record<string, Entity>;
  commit: (d: Draft) => void;
  onRemove: () => void;
}) {
  const change = (fields: Record<string, unknown>) => commit(updateElement(draft, item, fields));
  const layer = (value?: string) =>
    draft.layers.length > 0 && <Choice label="Layer" value={value} none="Not on a layer" options={draft.layers} onChange={(v) => change({ layer: v })} />;
  const block = (value?: string) => <Choice label="Block" value={value} none="Not in a block" options={draft.blocks} onChange={(v) => change({ block: v })} />;
  const remove = (
    <p>
      <button type="button" className="btn btn--sm btn--quiet" onClick={onRemove}>
        Remove it
      </button>
    </p>
  );
  /** Buffer stop checkboxes for the element's ends that lead nowhere. */
  const bumpers = (points: string[]) =>
    points
      .filter((id) => index.at.get(id)?.length === 1)
      .map((id) => {
        const p = index.points.get(id)!;
        return (
          <label key={id} className="check">
            <input type="checkbox" checked={p.bumper ?? false} onChange={(e) => commit({ ...draft, points: draft.points.map((q) => (q.id === id ? { ...q, bumper: e.target.checked || undefined } : q)) })} />
            Buffer stop at {Math.round(p.x)}, {Math.round(p.y)}
          </label>
        );
      });

  if (item.kind === "track") {
    const t = index.tracks.get(item.id)!;
    const info = arcInfo(index.points.get(t.a)!, index.points.get(t.b)!, t.angle);
    return (
      <Section title={t.angle ? "Curve" : "Straight"}>
        <p className="trk-sub muted">
          {Math.round(info.length)} mm long
          {t.angle ? `, turning ${Math.abs(t.angle)}° ${t.angle > 0 ? "right" : "left"} at ${Math.round(info.radius)} mm radius` : ""}
        </p>
        {block(t.block)}
        {layer(t.layer)}
        {bumpers([t.a, t.b])}
        {remove}
      </Section>
    );
  }
  if (item.kind === "junction") {
    const j = index.junctions.get(item.id)!;
    const kind = KINDS[j.kind];
    return (
      <Section title={kind.name}>
        {kind.switches && <EntitySelect label="Switch machine" kind="turnout" value={j.switch} entities={entities} onChange={(id) => change({ switch: id })} />}
        {(j.kind === "turnout" || j.kind === "wye") && <p className="trk-sub muted">Closed is the {j.kind === "turnout" ? "straight way" : "left leg seen from the points"}, thrown the {j.kind === "turnout" ? "branch" : "right"}.</p>}
        {j.kind === "crossover" && (
          <p>
            <button type="button" className="btn btn--sm btn--quiet" onClick={() => change({ ends: [j.ends[2], j.ends[3], j.ends[0], j.ends[1]] })}>
              Cross the other way
            </button>
          </p>
        )}
        {block(j.block)}
        {layer(j.layer)}
        {bumpers(j.ends)}
        {remove}
      </Section>
    );
  }
  if (item.kind === "platform") {
    const p = draft.platforms.find((x) => x.id === item.id)!;
    const num = (v: string) => Math.max(1, Math.round(Number(v) || 0));
    return (
      <Section title="Platform">
        <CommitInput label="Length in mm" type="number" value={String(p.length)} onCommit={(v) => change({ length: num(v) })} />
        <CommitInput label="Width in mm" type="number" value={String(p.width)} onCommit={(v) => change({ width: num(v) })} />
        {layer(p.layer)}
        {remove}
      </Section>
    );
  }
  const l = draft.labels.find((x) => x.id === item.id)!;
  return (
    <Section title="Label">
      <CommitInput label="Text" value={l.text} onCommit={(v) => v.trim() && change({ text: v.trim() })} />
      {layer(l.layer)}
      {remove}
    </Section>
  );
}

const KIND_WORDS: Record<ElementRef["kind"], [string, string]> = {
  track: ["piece of track", "pieces of track"],
  junction: ["turnout or crossing", "turnouts and crossings"],
  platform: ["platform", "platforms"],
  label: ["label", "labels"],
};

/** What a selection of many elements holds, and what can be done to all of them at once. */
function SelectionDetails({ refs, draft, commit, onRemove }: { refs: ElementRef[]; draft: Draft; commit: (d: Draft) => void; onRemove: () => void }) {
  const counts = (Object.keys(KIND_WORDS) as ElementRef["kind"][])
    .map((k) => [k, refs.filter((r) => r.kind === k).length] as const)
    .filter(([, n]) => n > 0)
    .map(([k, n]) => `${n} ${KIND_WORDS[k][n === 1 ? 0 : 1]}`);
  const trackRefs = refs.filter((r) => r.kind === "track" || r.kind === "junction");
  const valueOf = (r: ElementRef, key: "block" | "layer") => {
    const list: { id: string; block?: string; layer?: string }[] = { track: draft.tracks, junction: draft.junctions, platform: draft.platforms, label: draft.labels }[r.kind];
    return list.find((x) => x.id === r.id)?.[key] ?? "";
  };
  /** The value all of them share, or "mixed". */
  const common = (list: ElementRef[], key: "block" | "layer") => {
    const values = new Set(list.map((r) => valueOf(r, key)));
    return values.size === 1 ? [...values][0] : "mixed";
  };
  const block = common(trackRefs, "block");
  const layer = common(refs, "layer");
  return (
    <Section title={`${refs.length} selected`}>
      <p className="trk-sub muted">{counts.join(", ")}</p>
      {trackRefs.length > 0 && (
        <label className="trk-field">
          <span>Block</span>
          <select className="input input--select" value={block} onChange={(e) => commit(setBlock(draft, trackRefs, e.target.value || undefined))}>
            {block === "mixed" && (
              <option value="mixed" disabled>
                Several
              </option>
            )}
            <option value="">Not in a block</option>
            {draft.blocks.map((b) => (
              <option key={b.id} value={b.id}>
                {b.name}
              </option>
            ))}
          </select>
        </label>
      )}
      {draft.layers.length > 0 && (
        <label className="trk-field">
          <span>Layer</span>
          <select className="input input--select" value={layer} onChange={(e) => commit(setLayer(draft, refs, e.target.value || undefined))}>
            {layer === "mixed" && (
              <option value="mixed" disabled>
                Several
              </option>
            )}
            <option value="">Not on a layer</option>
            {draft.layers.map((l) => (
              <option key={l.id} value={l.id}>
                {l.name}
              </option>
            ))}
          </select>
        </label>
      )}
      <p>
        <button type="button" className="btn btn--sm btn--quiet" onClick={onRemove}>
          Remove all {refs.length}
        </button>
      </p>
    </Section>
  );
}

/** Moving and turning the selection by numbers, for what dragging can't
 * do exactly. */
function MoveTurn({ refs, draft, index, commit }: { refs: ElementRef[]; draft: Draft; index: ReturnType<typeof indexPlan>; commit: (d: Draft) => void }) {
  const [degrees, setDegrees] = useState("90");
  const [dx, setDx] = useState("0");
  const [dy, setDy] = useState("0");
  const about = selectionMiddle(draft, index, refs);
  if (!about) return null;
  const turn = (deg: number) => Number.isFinite(deg) && deg % 360 !== 0 && commit(turnSelection(draft, refs, deg, about));
  const by = { x: Number(dx) || 0, y: Number(dy) || 0 };
  return (
    <Section title="Move and turn">
      <p className="trk-sub muted">
        Drag the selection to move it, or nudge it with the arrow keys: a millimeter at a time, {Math.round(draft.grid)} mm with Shift. It turns about its middle; what it was joined to stays where it is.
      </p>
      <div className="trk-rotate">
        <label className="trk-field">
          <span>Degrees, clockwise</span>
          <input className="input input--num" type="number" step={0.1} value={degrees} onChange={(e) => setDegrees(e.target.value)} />
        </label>
        <span className="trk-rotate__turns">
          <button type="button" className="btn btn--sm" onClick={() => turn(Number(degrees))}>
            Turn
          </button>
        </span>
      </div>
      <p className="trk-note__actions">
        <button type="button" className="btn btn--sm btn--quiet" onClick={() => turn(-90)} title="Turn a quarter left">
          ↺ 90°
        </button>
        <button type="button" className="btn btn--sm btn--quiet" onClick={() => turn(90)} title="Turn a quarter right">
          ↻ 90°
        </button>
        <button type="button" className="btn btn--sm btn--quiet" onClick={() => turn(180)} title="Turn it round">
          180°
        </button>
      </p>
      <div className="trk-move">
        <label className="trk-field">
          <span>Right, mm</span>
          <input className="input input--num" type="number" value={dx} onChange={(e) => setDx(e.target.value)} />
        </label>
        <label className="trk-field">
          <span>Down, mm</span>
          <input className="input input--num" type="number" value={dy} onChange={(e) => setDy(e.target.value)} />
        </label>
        <button type="button" className="btn btn--sm" disabled={!by.x && !by.y} onClick={() => commit(moveSelection(draft, refs, by))}>
          Move
        </button>
      </div>
    </Section>
  );
}

function Blocks({
  draft,
  entities,
  active,
  setActive,
  commit,
  topology,
}: {
  draft: Draft;
  entities: Record<string, Entity>;
  active?: string;
  setActive: (id: string | undefined) => void;
  commit: (d: Draft) => void;
  topology?: TrackPlanState["topology"];
}) {
  const [detector, setDetector] = useState("");
  const [name, setName] = useState("");
  const block = draft.blocks.find((b) => b.id === active);
  const used = new Set(draft.blocks.map((b) => b.occupancy).filter(Boolean));
  const free = entitiesOf(entities, "block").filter((e) => !used.has(e.id));

  /** The reporter on the same node and track as a detector. */
  const reporterFor = (occupancy?: string) => {
    const occ = occupancy ? entities[occupancy] : undefined;
    return occ && entitiesOf(entities, "reporter").find((r) => r.node === occ.node && r.group === occ.group)?.id;
  };

  function add() {
    const occ = detector ? entities[detector] : undefined;
    const blockName = name.trim() || occ?.name || `Block ${draft.blocks.length + 1}`;
    const added: TrackBlock = { id: makeID(blockName, new Set(draft.blocks.map((b) => b.id)), "block"), name: blockName };
    if (occ) added.occupancy = occ.id;
    const rep = reporterFor(occ?.id);
    if (rep) added.reporter = rep;
    commit({ ...draft, blocks: [...draft.blocks, added] });
    setActive(added.id);
    setName("");
    setDetector("");
  }

  function change(b: TrackBlock) {
    commit({ ...draft, blocks: draft.blocks.map((x) => (x.id === b.id ? b : x)) });
  }

  function remove(id: string) {
    const members: ElementRef[] = [...draft.tracks.filter((t) => t.block === id).map((t) => ({ kind: "track" as const, id: t.id })), ...draft.junctions.filter((j) => j.block === id).map((j) => ({ kind: "junction" as const, id: j.id }))];
    const blocks = draft.blocks.filter((b) => b.id !== id);
    commit({ ...setBlock(draft, members, undefined), blocks });
    setActive(blocks[0]?.id);
  }

  const set = <K extends keyof TrackBlock>(b: TrackBlock, key: K, value: TrackBlock[K] | undefined) => {
    const copy = { ...b };
    if (value === undefined || value === "" || value === 0) delete copy[key];
    else copy[key] = value;
    change(copy);
  };
  const drawn = block && topology?.blocks.find((b) => b.id === block.id)?.length;

  return (
    <Section title="Blocks">
      {draft.blocks.length > 0 && (
        <div className="trk-list" role="group" aria-label="Block to work on">
          {draft.blocks.map((b) => (
            <button key={b.id} type="button" className="btn btn--sm btn--quiet" aria-pressed={b.id === active} onClick={() => setActive(b.id)}>
              {b.name}
            </button>
          ))}
        </div>
      )}
      {block && (
        <div className="trk-block">
          <CommitInput label="Name" value={block.name} onCommit={(v) => v.trim() && change({ ...block, name: v.trim() })} />
          <EntitySelect
            label="Detector"
            kind="block"
            value={block.occupancy}
            entities={entities}
            onChange={(id) => {
              const copy = { ...block };
              if (id) copy.occupancy = id;
              else delete copy.occupancy;
              const rep = reporterFor(id);
              if (rep && !copy.reporter) copy.reporter = rep;
              change(copy);
            }}
          />
          <EntitySelect label="Reporter" kind="reporter" value={block.reporter} entities={entities} onChange={(id) => set(block, "reporter", id)} />
          <CommitInput
            label="Measured length in mm"
            type="number"
            placeholder={drawn ? `${Math.round(drawn)} as drawn` : undefined}
            value={block.length_mm ? String(block.length_mm) : ""}
            onCommit={(v) => set(block, "length_mm", Math.max(0, Math.round(Number(v) || 0)))}
          />
          <label className="trk-field">
            <span>Station</span>
            <select className="input input--select" value={block.station ?? ""} onChange={(e) => set(block, "station", e.target.value)}>
              <option value="">Not a platform track</option>
              {draft.stations.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </label>
          <Connections block={block} draft={draft} entities={entities} topology={topology} />
          <p>
            <button type="button" className="btn btn--sm btn--quiet" onClick={() => remove(block.id)}>
              Delete block
            </button>
          </p>
        </div>
      )}
      <form
        className="trk-add"
        onSubmit={(e) => {
          e.preventDefault();
          add();
        }}
      >
        <h3>New block</h3>
        <label className="trk-field">
          <span>Detector</span>
          <select className="input input--select" value={detector} onChange={(e) => setDetector(e.target.value)}>
            <option value="">None yet</option>
            {free.map((e) => (
              <option key={e.id} value={e.id}>
                {e.name}
              </option>
            ))}
          </select>
        </label>
        <label className="trk-field">
          <span>Name</span>
          <input className="input" value={name} placeholder={detector ? entities[detector]?.name : `Block ${draft.blocks.length + 1}`} onChange={(e) => setName(e.target.value)} />
        </label>
        <button type="submit" className="btn btn--sm">
          Add block
        </button>
      </form>
    </Section>
  );
}

/** Where a block's ends lead, from the saved plan's topology. */
function Connections({ block, draft, entities, topology }: { block: TrackBlock; draft: Draft; entities: Record<string, Entity>; topology?: TrackPlanState["topology"] }) {
  if (!topology) return <p className="trk-sub muted">Save to see where this block connects.</p>;
  const names = new Map(draft.blocks.map((b) => [b.id, b.name]));
  const kinds = new Map(draft.junctions.map((j) => [j.id, KINDS[j.kind].name]));
  const links = topology.links.filter((l) => l.from.block === block.id || l.to.block === block.id);
  if (links.length === 0) return <p className="trk-sub muted">Not connected to another block yet.</p>;
  return (
    <div className="trk-links">
      <h3>Connects to</h3>
      <ul>
        {links.map((l) => {
          const [here, there] = l.from.block === block.id ? [l.from, l.to] : [l.to, l.from];
          const via = (l.turnouts ?? []).map((t) => `${(t.switch && entities[t.switch]?.name) || `${kinds.get(t.junction) ?? "Junction"} ${t.junction}`} ${t.position}`).join(", ");
          return (
            <li key={`${l.from.point}>${l.to.point}:${via}`}>
              {names.get(there.block) ?? there.block} from its {compass(here.heading)} end
              {via && <span className="muted"> · {via}</span>}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function Stations({ draft, commit }: { draft: Draft; commit: (d: Draft) => void }) {
  const [name, setName] = useState("");
  function add() {
    const stationName = name.trim();
    if (!stationName) return;
    const id = makeID(stationName, new Set(draft.stations.map((s) => s.id)), "station");
    commit({ ...draft, stations: [...draft.stations, { id, name: stationName }] });
    setName("");
  }
  function remove(id: string) {
    commit({
      ...draft,
      stations: draft.stations.filter((s) => s.id !== id),
      blocks: draft.blocks.map((b) => {
        if (b.station !== id) return b;
        const copy = { ...b };
        delete copy.station;
        return copy;
      }),
    });
  }
  return (
    <Section title="Stations">
      {draft.stations.map((s) => (
        <div key={s.id} className="trk-station">
          <CommitInput label="Name" value={s.name} onCommit={(v) => v.trim() && commit({ ...draft, stations: draft.stations.map((x) => (x.id === s.id ? { ...x, name: v.trim() } : x)) })} />
          <button type="button" className="btn btn--sm btn--quiet" onClick={() => remove(s.id)}>
            Delete
          </button>
        </div>
      ))}
      <form
        className="trk-add"
        onSubmit={(e) => {
          e.preventDefault();
          add();
        }}
      >
        <label className="trk-field">
          <span>New station</span>
          <input className="input" value={name} placeholder="Times Sq–42 St" onChange={(e) => setName(e.target.value)} />
        </label>
        <button type="submit" className="btn btn--sm" disabled={!name.trim()}>
          Add station
        </button>
      </form>
      <p className="trk-sub muted">A block's station makes it one of the station's platform tracks.</p>
    </Section>
  );
}

function Layers({
  draft,
  commit,
  layers: [hidden, setHidden],
  onSelect,
}: {
  draft: Draft;
  commit: (d: Draft) => void;
  layers: ReturnType<typeof useHiddenLayers>;
  onSelect: (id: string) => void;
}) {
  const [name, setName] = useState("");
  function add() {
    const layerName = name.trim();
    if (!layerName) return;
    const id = makeID(layerName, new Set(draft.layers.map((l) => l.id)), "layer");
    commit({ ...draft, layers: [...draft.layers, { id, name: layerName }] });
    setName("");
  }
  function remove(id: string) {
    const off = <T extends { layer?: string }>(x: T): T => {
      if (x.layer !== id) return x;
      const copy = { ...x };
      delete copy.layer;
      return copy;
    };
    commit({
      ...draft,
      layers: draft.layers.filter((l) => l.id !== id),
      tracks: draft.tracks.map(off),
      junctions: draft.junctions.map(off),
      platforms: draft.platforms.map(off),
      labels: draft.labels.map(off),
    });
  }
  return (
    <Section title="Layers">
      {draft.layers.length === 0 && <p className="trk-sub muted">Layers divide a plan you want to see a part at a time, such as the decks of a layout on two levels.</p>}
      {draft.layers.map((l) => (
        <div key={l.id} className="trk-layer">
          <input type="checkbox" aria-label={`Show ${l.name}`} checked={!hidden.has(l.id)} onChange={(e) => setHidden(l.id, !e.target.checked)} />
          <CommitInput label="Name" value={l.name} onCommit={(v) => v.trim() && commit({ ...draft, layers: draft.layers.map((x) => (x.id === l.id ? { ...x, name: v.trim() } : x)) })} />
          <span className="trk-layer__actions">
            <button type="button" className="btn btn--sm btn--quiet" onClick={() => onSelect(l.id)} title={`Select everything on ${l.name}`}>
              Select
            </button>
            <button type="button" className="btn btn--sm btn--quiet" onClick={() => remove(l.id)}>
              Delete
            </button>
          </span>
        </div>
      ))}
      {draft.layers.length > 0 && <p className="trk-sub muted">The boxes show and hide a layer on this browser only. Deleting a layer keeps what is on it.</p>}
      <form
        className="trk-add"
        onSubmit={(e) => {
          e.preventDefault();
          add();
        }}
      >
        <label className="trk-field">
          <span>New layer</span>
          <input className="input" value={name} placeholder="Upper deck" onChange={(e) => setName(e.target.value)} />
        </label>
        <button type="submit" className="btn btn--sm" disabled={!name.trim()}>
          Add layer
        </button>
      </form>
    </Section>
  );
}

function Import({ draft, commit, onImported }: { draft: Draft; commit: (d: Draft) => void; onImported: (refs: ElementRef[]) => void }) {
  const [scale, setScale] = useState(10);
  const [layer, setLayer] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [result, setResult] = useState<{ summary: string; notes: string[] }>();

  async function choose(file: File | undefined) {
    if (!file) return;
    setBusy(true);
    setError(undefined);
    setResult(undefined);
    try {
      const imp = await api.importTrackPlan(file, scale);
      const on = layer && draft.layers.some((l) => l.id === layer) ? layer : undefined;
      const merged = mergeTrack(draft, imp.plan, on);
      commit(merged);
      // Select what came in, ready to be moved and turned into place.
      const had = new Set([...draft.tracks.map((t) => `t:${t.id}`), ...draft.junctions.map((j) => `j:${j.id}`)]);
      onImported([
        ...merged.tracks.filter((t) => !had.has(`t:${t.id}`)).map((t) => ({ kind: "track" as const, id: t.id })),
        ...merged.junctions.filter((j) => !had.has(`j:${j.id}`)).map((j) => ({ kind: "junction" as const, id: j.id })),
      ]);
      const free = freeEnds(indexPlan(imp.plan)).filter((p) => !p.bumper).length;
      setResult({
        summary: `Added ${imp.plan.tracks.length} pieces of track and ${imp.plan.junctions.length} turnouts and crossings${free ? `, with ${free} track ends that lead nowhere yet` : ""}. They're selected: drag them, or move and turn them by numbers, to put them in place.`,
        notes: imp.notes ?? [],
      });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Section title="Import">
      <p className="trk-sub muted">Bring in track from RailModeller Pro: export the layout for JMRI's PanelPro there, then choose the file here. The track is added to the plan where it lies in the export.</p>
      <label className="trk-field">
        <span>Millimeters to a panel unit</span>
        <input className="input input--num" type="number" min={0.01} step="any" value={scale} onChange={(e) => setScale(Number(e.target.value) || 10)} />
      </label>
      {draft.layers.length > 0 && <Choice label="Put it on" value={layer} none="No layer" options={draft.layers} onChange={setLayer} />}
      <p className="trk-note__actions">
        <label className={busy ? "btn btn--sm trk-file is-busy" : "btn btn--sm trk-file"}>
          {busy ? "Reading" : "Choose a panel file"}
          <input
            type="file"
            className="sr-only"
            accept=".xml,application/xml,text/xml"
            disabled={busy}
            onChange={(e) => {
              void choose(e.target.files?.[0]);
              e.target.value = "";
            }}
          />
        </label>
      </p>
      {error && <p className="note note--error">{error}</p>}
      {result && (
        <div className="trk-sub">
          <p>{result.summary}</p>
          {result.notes.length > 0 && (
            <ul className="trk-problems">
              {result.notes.map((n) => (
                <li key={n}>{n}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </Section>
  );
}

/** The size an image is drawn at, as its height over its width. SVGs
 * without a size of their own count as square. */
function aspectOf(file: File): Promise<number> {
  return new Promise((resolve) => {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.onload = () => {
      URL.revokeObjectURL(url);
      resolve(img.naturalWidth > 0 && img.naturalHeight > 0 ? img.naturalHeight / img.naturalWidth : 1);
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      resolve(1);
    };
    img.src = url;
  });
}

function Background({ draft, area, commit, onAdded, onRemoved }: { draft: Draft; area: Bounds; commit: (d: Draft, run?: string) => void; onAdded: () => void; onRemoved: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const bg = draft.background;
  const set = (change: Partial<TrackBackground>, run?: string) => bg && commit({ ...draft, background: { ...bg, ...change } }, run);

  async function choose(file: File | undefined) {
    if (!file) return;
    setBusy(true);
    setError(undefined);
    try {
      const [aspect, { image }] = await Promise.all([aspectOf(file), api.uploadBackground(file)]);
      if (bg) {
        // A new image keeps the old one's place and width.
        commit({ ...draft, background: { ...bg, image, height: round(bg.width * aspect, 1) } });
      } else {
        const placed = planBounds(draft);
        const width = placed ? Math.max(placed.maxX - placed.minX, 600) : area.maxX - area.minX;
        commit({ ...draft, background: { image, x: placed?.minX ?? area.minX, y: placed?.minY ?? area.minY, width, height: round(width * aspect, 1), opacity: 0.4 } });
        onAdded();
      }
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const picker = (label: string) => (
    <label className={busy ? "btn btn--sm trk-file is-busy" : "btn btn--sm trk-file"}>
      {busy ? "Uploading" : label}
      <input
        type="file"
        className="sr-only"
        accept="image/png,image/jpeg,image/gif,image/webp,image/svg+xml"
        disabled={busy}
        onChange={(e) => {
          void choose(e.target.files?.[0]);
          e.target.value = "";
        }}
      />
    </label>
  );

  return (
    <Section title="Background">
      {!bg && <p className="trk-sub muted">Put a plan from your track planning software under the track to draw over it: export it as an image (PNG, JPEG, WebP, GIF or SVG) and choose it here.</p>}
      {bg && (
        <>
          <label className="trk-field">
            <span>Width in mm</span>
            <input
              className="input input--num"
              type="number"
              min={1}
              step={1}
              value={bg.width}
              onChange={(e) => {
                const width = Number(e.target.value);
                if (width > 0) set({ width: round(width, 1), height: round((bg.height / bg.width) * width, 1) }, "background-width");
              }}
            />
          </label>
          <div className="trk-rotate">
            <label className="trk-field">
              <span>Rotation in degrees, clockwise</span>
              <input
                className="input input--num"
                type="number"
                step={0.1}
                value={bg.rotation ?? 0}
                onChange={(e) => {
                  const deg = Number(e.target.value);
                  if (Number.isFinite(deg)) set({ rotation: round(normalizeDegrees(deg), 2) || undefined }, "background-rotation");
                }}
              />
            </label>
            <span className="trk-rotate__turns" role="group" aria-label="Quarter turns">
              <button type="button" className="btn btn--sm btn--quiet" onClick={() => set({ rotation: normalizeDegrees((bg.rotation ?? 0) - 90) || undefined })} title="Turn a quarter left">
                ↺ 90°
              </button>
              <button type="button" className="btn btn--sm btn--quiet" onClick={() => set({ rotation: normalizeDegrees((bg.rotation ?? 0) + 90) || undefined })} title="Turn a quarter right">
                ↻ 90°
              </button>
            </span>
          </div>
          <label className="trk-field">
            <span>Opacity {Math.round(bg.opacity * 100)}%</span>
            <input type="range" className="trk-range" min={5} max={100} step={5} value={Math.round(bg.opacity * 100)} onChange={(e) => set({ opacity: Number(e.target.value) / 100 }, "background-opacity")} />
          </label>
          <label className="check">
            <input type="checkbox" checked={bg.invert ?? false} onChange={(e) => set({ invert: e.target.checked || undefined })} />
            Swap light and dark, for a plan drawn dark on white
          </label>
          <label className="check">
            <input type="checkbox" checked={bg.operating ?? false} onChange={(e) => set({ operating: e.target.checked || undefined })} />
            Show it while operating too
          </label>
          <p className="trk-sub muted">Move it with the Background tool. Set its width so that a known length on it, such as a straight piece of track, matches the plan.</p>
        </>
      )}
      {error && <p className="note note--error">{error}</p>}
      <p className="trk-note__actions">
        {picker(bg ? "Replace the image" : "Choose an image")}
        {bg && (
          <button
            type="button"
            className="btn btn--sm btn--quiet"
            onClick={() => {
              commit({ ...draft, background: undefined });
              onRemoved();
            }}
          >
            Remove it
          </button>
        )}
      </p>
    </Section>
  );
}

function Problems({ ends, saved }: { ends: number; saved?: TrackPlanState }) {
  const problems = (saved?.topology.problems ?? []).filter((p) => p.kind !== "open_end");
  if (ends === 0 && problems.length === 0) return null;
  return (
    <Section title="To fix">
      <ul className="trk-problems">
        {ends > 0 && <li>{ends === 1 ? "One track end leads" : `${ends} track ends lead`} nowhere and have no buffer stop; they're marked in amber.</li>}
        {problems.map((p) => (
          <li key={`${p.kind}${p.block ?? ""}${p.point ?? ""}`}>{p.message}</li>
        ))}
      </ul>
    </Section>
  );
}
