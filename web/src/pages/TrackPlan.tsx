import { type PointerEvent as ReactPointerEvent, type ReactNode, useEffect, useRef, useState } from "react";
import { ApiError, api, type Entity, type EntityKind, type TrackBackground, type TrackBlock, type TrackPlan, type TrackPlanState, type TrackStation, type TrackTile } from "../api";
import { type LampColor, Status } from "../components/Lamp";
import { Empty, Page } from "../components/Page";
import { TrackDiagram, blockState, trainsIn } from "../components/TrackDiagram";
import { useBus } from "../store";
import { type Bounds, type Cell, DIRECTION_NAMES, PIECE_LABELS, backgroundBounds, bounds, cellKey, cellNear, classify, drawStroke, erase, extendStroke, indexTiles, lineCells, makeID, normalizeDegrees, openEnds, straightTo, switches, type Tiles } from "../track";

type Tool = "select" | "track" | "platform" | "label" | "block" | "erase" | "background";

const TOOLS: { tool: Tool; label: string; key: string; hint: string }[] = [
  { tool: "select", label: "Select", key: "v", hint: "Click a cell to see and change what it holds." },
  { tool: "track", label: "Track", key: "t", hint: "Drag to lay track; Shift keeps it straight, and going back over it takes it up. Branch off a line for a turnout, or cross one for a crossing." },
  { tool: "platform", label: "Platform", key: "p", hint: "Drag to draw a platform beside the track; hold Shift to keep it straight." },
  { tool: "label", label: "Label", key: "l", hint: "Click an empty cell to write a label there." },
  { tool: "block", label: "Block", key: "b", hint: "Drag over track to put it in the block chosen under Blocks. Hold Option or Alt to take track out of its block." },
  { tool: "erase", label: "Erase", key: "e", hint: "Drag over cells to clear them. Track either side stops short of them, so drawing across the gap joins it up again." },
  { tool: "background", label: "Background", key: "g", hint: "Drag to move the background image. Set its size and how faint it is under Background." },
];

const ZOOMS = [20, 28, 36, 44];
const HISTORY = 100;

interface Draft {
  tiles: Tiles;
  blocks: TrackBlock[];
  stations: TrackStation[];
  background?: TrackBackground;
}

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

const draftOf = (plan: TrackPlan): Draft => ({ tiles: indexTiles(plan.tiles), blocks: plan.blocks, stations: plan.stations, background: plan.background });
const planOf = (d: Draft): TrackPlan => ({ tiles: [...d.tiles.values()], blocks: d.blocks, stations: d.stations, background: d.background });

function union(a?: Bounds, b?: Bounds): Bounds | undefined {
  if (!a || !b) return a ?? b;
  return { minX: Math.min(a.minX, b.minX), minY: Math.min(a.minY, b.minY), maxX: Math.max(a.maxX, b.maxX), maxY: Math.max(a.maxY, b.maxY) };
}

const round = (n: number, places: number) => Math.round(n * 10 ** places) / 10 ** places;
const message = (err: unknown) => (err instanceof Error ? err.message : String(err));

function pad(b: Bounds | undefined, margin: number, minWidth: number, minHeight: number): Bounds {
  const area = b ? { minX: b.minX - margin, minY: b.minY - margin, maxX: b.maxX + margin, maxY: b.maxY + margin } : { minX: 0, minY: 0, maxX: 0, maxY: 0 };
  area.maxX = Math.max(area.maxX, area.minX + minWidth - 1);
  area.maxY = Math.max(area.maxY, area.minY + minHeight - 1);
  return area;
}

function entitiesOf(entities: Record<string, Entity>, kind: EntityKind): Entity[] {
  return Object.values(entities)
    .filter((e) => e.kind === kind)
    .sort((a, b) => a.name.localeCompare(b.name));
}

export function TrackPlanPage() {
  const { trackPlan, entities, loaded } = useBus();
  const [session, setSession] = useState<Session | undefined>(keptSession);
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
  if (session) return <Editor saved={trackPlan} session={session} setSession={setSession} entities={entities} />;
  return <Viewer state={trackPlan} entities={entities} onEdit={() => setSession({ base: trackPlan.revision, draft: draftOf(trackPlan.plan), past: [], future: [] })} />;
}

function Zoom({ zoom, setZoom }: { zoom: number; setZoom: (z: number) => void }) {
  return (
    <span className="trk-zoom" role="group" aria-label="Zoom">
      <button type="button" className="btn btn--sm btn--quiet" disabled={zoom === 0} onClick={() => setZoom(zoom - 1)} aria-label="Zoom out">
        −
      </button>
      <button type="button" className="btn btn--sm btn--quiet" disabled={zoom === ZOOMS.length - 1} onClick={() => setZoom(zoom + 1)} aria-label="Zoom in">
        +
      </button>
    </span>
  );
}

// Operating: the plan lit by the layout, turnouts set by a click.

function Viewer({ state, entities, onEdit }: { state: TrackPlanState; entities: Record<string, Entity>; onEdit: () => void }) {
  const { plan } = state;
  const background = plan.background?.operating ? plan.background : undefined;
  const [zoom, setZoom] = useState(1);
  const [error, setError] = useState<string>();
  const lead = "The layout's track plan, lit by what the detectors and turnouts report.";

  if (plan.tiles.length === 0) {
    return (
      <Page title="Track" lead={lead}>
        <Empty title="No track plan yet">
          <p>Draw the layout's track on a grid, divide it into blocks and link the blocks to their detectors and the turnouts to their switch machines.</p>
          <p>
            <button type="button" className="btn btn--primary" onClick={onEdit}>
              Draw the track plan
            </button>
          </p>
        </Empty>
      </Page>
    );
  }

  async function toggle(tile: TrackTile) {
    const ent = tile.turnout ? entities[tile.turnout] : undefined;
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
          <Zoom zoom={zoom} setZoom={setZoom} />
          <button type="button" className="btn" onClick={onEdit}>
            Edit track plan
          </button>
        </>
      }
    >
      {error && <p className="note note--error">{error}</p>}
      <div className="panel trk-canvas">
        <TrackDiagram
          tiles={plan.tiles}
          blocks={plan.blocks}
          entities={entities}
          area={pad(union(bounds(plan.tiles), backgroundBounds(background)), 1, 1, 1)}
          cell={ZOOMS[zoom]}
          background={background}
          onTurnout={toggle}
        />
      </div>
      <p className="trk-legend muted">Click a turnout to throw or close it.</p>
      <BlockTable plan={plan} entities={entities} />
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

function BlockTable({ plan, entities }: { plan: TrackPlan; entities: Record<string, Entity> }) {
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
              const state = blockState(b, entities);
              return (
                <tr key={b.id}>
                  <td>
                    <div className="cell-title">{b.name}</div>
                    {b.station && <div className="cell-sub">{stations.get(b.station)}</div>}
                  </td>
                  <td>
                    <Status color={stateColor(state)}>{state}</Status>
                  </td>
                  <td>{trainsIn(b, entities).join(", ") || <span className="faint">none reported</span>}</td>
                  <td className="num">{b.length_mm ? `${b.length_mm} mm` : <span className="faint">not set</span>}</td>
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
  cells: Cell[];
  /** The stroke as it was when Shift went down; a straight line goes on
   * from its end while Shift is held. */
  straight?: Cell[];
  tiles: Tiles;
  background?: TrackBackground;
  /** Where a background move started, to a fraction of a cell. */
  from: { x: number; y: number };
  moved: boolean;
  unassign: boolean;
  error?: string;
}

function Editor({ saved, session, setSession, entities }: { saved: TrackPlanState; session: Session; setSession: (s: Session | undefined) => void; entities: Record<string, Entity> }) {
  const { draft } = session;
  const dirty = session.past.length > 0;
  const [tool, setTool] = useState<Tool>("track");
  const [zoom, setZoom] = useState(1);
  const [selected, setSelected] = useState<Cell>();
  const [hover, setHover] = useState<Cell>();
  const [activeBlock, setActiveBlock] = useState<string | undefined>(draft.blocks[0]?.id);
  const [note, setNote] = useState<string>();
  const [error, setError] = useState<string>();
  const [conflict, setConflict] = useState<string>();
  const [saving, setSaving] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const [frozenArea, setFrozenArea] = useState<Bounds>();
  const stroke = useRef<Stroke | undefined>(undefined);
  const sessionRef = useRef(session);
  sessionRef.current = session;

  const area = frozenArea ?? pad(union(bounds(draft.tiles.values()), backgroundBounds(draft.background)), 6, 40, 24);
  const update = (change: (s: Session) => Session) => setSession(change(sessionRef.current));
  /** Changes the plan as one step to undo; a change in the same run as
   * the one before joins its step. */
  const commit = (next: Draft, run?: string) =>
    update((s) => (run && s.run === run ? { ...s, draft: next } : { ...s, draft: next, past: [...s.past, s.draft].slice(-HISTORY), future: [], run }));
  const undo = () => update((s) => (s.past.length ? { ...s, draft: s.past[s.past.length - 1], past: s.past.slice(0, -1), future: [s.draft, ...s.future], run: undefined } : s));
  const redo = () => update((s) => (s.future.length ? { ...s, draft: s.future[0], past: [...s.past, s.draft], future: s.future.slice(1), run: undefined } : s));
  const tools = TOOLS.filter((t) => t.tool !== "background" || draft.background);
  const selectedTile = selected ? draft.tiles.get(cellKey(selected.x, selected.y)) : undefined;

  // Someone else saved while nothing here was changed: follow along.
  useEffect(() => {
    if (!dirty && saved.revision !== session.base) {
      setSession({ base: saved.revision, draft: draftOf(saved.plan), past: [], future: [] });
    }
  }, [dirty, saved, session.base, setSession]);

  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      if (target?.closest("input, select, textarea")) return;
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === "z") {
        e.preventDefault();
        if (e.shiftKey) redo();
        else undo();
      } else if (mod && e.key.toLowerCase() === "y") {
        e.preventDefault();
        redo();
      } else if ((e.key === "Delete" || e.key === "Backspace") && selected && selectedTile) {
        e.preventDefault();
        commit({ ...sessionRef.current.draft, tiles: erase(sessionRef.current.draft.tiles, selected) });
        setSelected(undefined);
      } else if (e.key === "Escape") {
        setSelected(undefined);
      } else if (!mod && !e.altKey) {
        const t = tools.find((t) => t.key === e.key);
        if (t) setTool(t.tool);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  function setTiles(s: Stroke, tiles: Tiles) {
    s.tiles = tiles;
    setSession({ ...sessionRef.current, draft: { ...s.base, tiles, background: s.background } });
  }

  function paint(s: Stroke, cells: Cell[]) {
    if (!activeBlock && !s.unassign) {
      setNote("Add a block under Blocks first, then paint track into it.");
      return;
    }
    let tiles = s.tiles;
    for (const c of cells) {
      const key = cellKey(c.x, c.y);
      const t = tiles.get(key);
      if (!t?.track || t.block === (s.unassign ? undefined : activeBlock)) continue;
      if (tiles === s.tiles) tiles = new Map(tiles);
      const changed = { ...t };
      if (s.unassign) delete changed.block;
      else changed.block = activeBlock;
      tiles.set(key, changed);
    }
    if (tiles !== s.tiles) setTiles(s, tiles);
  }

  function onPointerDown(cell: Cell, e: ReactPointerEvent<SVGSVGElement>, at: { x: number; y: number }) {
    if (e.button !== 0) return;
    setNote(undefined);
    const key = cellKey(cell.x, cell.y);
    if (tool === "select") {
      setSelected(draft.tiles.has(key) ? cell : undefined);
      return;
    }
    if (tool === "label") {
      if (!draft.tiles.has(key)) commit({ ...draft, tiles: new Map(draft.tiles).set(key, { x: cell.x, y: cell.y, label: "Label" }) });
      setSelected(cell);
      return;
    }
    e.currentTarget.setPointerCapture(e.pointerId);
    setFrozenArea(area);
    const s: Stroke = { base: draft, cells: [cell], tiles: draft.tiles, background: draft.background, from: at, moved: false, unassign: e.altKey };
    stroke.current = s;
    if (tool === "erase") setTiles(s, erase(s.tiles, cell));
    if (tool === "block") paint(s, [cell]);
  }

  function onPointerMove(cell: Cell, e: ReactPointerEvent<SVGSVGElement>, at: { x: number; y: number }) {
    if (!hover || hover.x !== cell.x || hover.y !== cell.y) setHover(cell);
    const s = stroke.current;
    if (!s) return;
    if (tool === "background" && s.base.background) {
      const bg = s.base.background;
      s.background = { ...bg, x: round(bg.x + at.x - s.from.x, 2), y: round(bg.y + at.y - s.from.y, 2) };
      s.moved = true;
      setTiles(s, s.tiles);
      return;
    }
    const last = s.cells[s.cells.length - 1];
    if (last.x === cell.x && last.y === cell.y) return;
    s.moved = true;
    if (tool === "track" || tool === "platform") {
      if (e.shiftKey) {
        s.straight ??= s.cells;
        const from = s.straight[s.straight.length - 1];
        s.cells = extendStroke(s.base.tiles, s.straight, lineCells(from, straightTo(from, cell)).slice(1), tool);
      } else {
        const near = cellNear(at);
        if (!near || (near.x === last.x && near.y === last.y)) return;
        s.straight = undefined;
        s.cells = extendStroke(s.base.tiles, s.cells, lineCells(last, near).slice(1), tool);
      }
      const result = drawStroke(s.base.tiles, s.cells, tool);
      s.error = result.error;
      setTiles(s, result.tiles);
      return;
    }
    const added = lineCells(last, cell).slice(1);
    s.cells.push(...added);
    if (tool === "erase") {
      let tiles = s.tiles;
      for (const c of added) tiles = erase(tiles, c);
      setTiles(s, tiles);
    } else if (tool === "block") {
      paint(s, added);
    }
  }

  function onPointerUp() {
    const s = stroke.current;
    stroke.current = undefined;
    setFrozenArea(undefined);
    if (!s) return;
    if (!s.moved && (tool === "track" || tool === "platform" || tool === "background")) {
      const key = cellKey(s.cells[0].x, s.cells[0].y);
      setSelected(draft.tiles.has(key) ? s.cells[0] : undefined);
      return;
    }
    if (s.error) setNote(s.error);
    if (s.tiles !== s.base.tiles || s.background !== s.base.background) {
      setSession({ ...sessionRef.current, draft: { ...s.base, tiles: s.tiles, background: s.background }, past: [...sessionRef.current.past, s.base].slice(-HISTORY), future: [], run: undefined });
    }
  }

  async function save(revision: string) {
    setSaving(true);
    setError(undefined);
    setConflict(undefined);
    try {
      const st = await api.saveTrackPlan(planOf(draft), revision);
      setSession({ base: st.revision, draft: draftOf(st.plan), past: [], future: [] });
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
      setSession({ base: st.revision, draft: draftOf(st.plan), past: [], future: [] });
    } catch (err) {
      setError(message(err));
    }
  }

  const ends = openEnds(draft.tiles);
  const hint = TOOLS.find((t) => t.tool === tool)?.hint;

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
        <Zoom zoom={zoom} setZoom={setZoom} />
      </div>
      <p className="trk-hint muted" aria-live="polite">
        {note ?? hint}
      </p>

      <div className="trk-editor">
        <div className="panel trk-canvas">
          <TrackDiagram
            tiles={draft.tiles.values()}
            blocks={draft.blocks}
            entities={entities}
            area={area}
            cell={ZOOMS[zoom]}
            editing
            activeBlock={activeBlock}
            selected={selected}
            hover={hover}
            openEnds={ends}
            background={draft.background}
            onPointerDown={onPointerDown}
            onPointerMove={onPointerMove}
            onPointerUp={onPointerUp}
            onPointerLeave={() => setHover(undefined)}
          />
        </div>
        <aside className="trk-side">
          {selected && selectedTile && (
            <TileDetails
              tile={selectedTile}
              draft={draft}
              entities={entities}
              onChange={(t) => commit({ ...draft, tiles: new Map(draft.tiles).set(cellKey(t.x, t.y), t) })}
              onRemove={() => {
                commit({ ...draft, tiles: erase(draft.tiles, selected) });
                setSelected(undefined);
              }}
            />
          )}
          <Blocks draft={draft} entities={entities} active={activeBlock} setActive={setActiveBlock} commit={commit} topology={dirty ? undefined : saved.topology} />
          <Stations draft={draft} commit={commit} />
          <Background
            draft={draft}
            area={area}
            commit={commit}
            onAdded={() => setTool("background")}
            onRemoved={() => tool === "background" && setTool("select")}
          />
          <Problems ends={ends.length} saved={dirty ? undefined : saved} />
        </aside>
      </div>
    </Page>
  );
}

/** A text field that changes the plan when it loses focus or on Enter,
 * so typing makes one step to undo, not one per letter. */
function CommitInput({ value, onCommit, label, type = "text" }: { value: string; onCommit: (v: string) => void; label: string; type?: "text" | "number" }) {
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

function TileDetails({ tile, draft, entities, onChange, onRemove }: { tile: TrackTile; draft: Draft; entities: Record<string, Entity>; onChange: (t: TrackTile) => void; onRemove: () => void }) {
  const shape = tile.track ? classify(tile.track) : undefined;
  const what = shape ? PIECE_LABELS[shape.piece] : tile.platform ? "Platform" : "Label";
  return (
    <Section title={what}>
      <p className="trk-sub muted">
        Cell {tile.x}, {tile.y}
        {tile.track && ` · track leaves ${tile.track.map((d) => DIRECTION_NAMES[d]).join(", ")}`}
      </p>
      {shape && switches(shape.piece) && (
        <EntitySelect
          label="Switch machine"
          kind="turnout"
          value={tile.turnout}
          entities={entities}
          onChange={(id) => {
            const t = { ...tile };
            if (id) t.turnout = id;
            else delete t.turnout;
            onChange(t);
          }}
        />
      )}
      {shape?.closed && shape.thrown && (
        <p className="trk-sub muted">
          Closed leads {DIRECTION_NAMES[shape.closed]}, thrown leads {DIRECTION_NAMES[shape.thrown]}.
        </p>
      )}
      {tile.track && (
        <label className="trk-field">
          <span>Block</span>
          <select
            className="input input--select"
            value={tile.block ?? ""}
            onChange={(e) => {
              const t = { ...tile };
              if (e.target.value) t.block = e.target.value;
              else delete t.block;
              onChange(t);
            }}
          >
            <option value="">Not in a block</option>
            {draft.blocks.map((b) => (
              <option key={b.id} value={b.id}>
                {b.name}
              </option>
            ))}
          </select>
        </label>
      )}
      {tile.label !== undefined && <CommitInput label="Text" value={tile.label} onCommit={(v) => v.trim() && onChange({ ...tile, label: v.trim() })} />}
      <p>
        <button type="button" className="btn btn--sm btn--quiet" onClick={onRemove}>
          Clear the cell
        </button>
      </p>
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
    const tiles = new Map(draft.tiles);
    for (const [key, t] of tiles) {
      if (t.block !== id) continue;
      const copy = { ...t };
      delete copy.block;
      tiles.set(key, copy);
    }
    const blocks = draft.blocks.filter((b) => b.id !== id);
    commit({ ...draft, tiles, blocks });
    setActive(blocks[0]?.id);
  }

  const set = <K extends keyof TrackBlock>(b: TrackBlock, key: K, value: TrackBlock[K] | undefined) => {
    const copy = { ...b };
    if (value === undefined || value === "" || value === 0) delete copy[key];
    else copy[key] = value;
    change(copy);
  };

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
          <CommitInput label="Length in mm" type="number" value={block.length_mm ? String(block.length_mm) : ""} onCommit={(v) => set(block, "length_mm", Math.max(0, Math.round(Number(v) || 0)))} />
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
  const links = topology.links.filter((l) => l.from.block === block.id || l.to.block === block.id);
  if (links.length === 0) return <p className="trk-sub muted">Not connected to another block yet.</p>;
  return (
    <div className="trk-links">
      <h3>Connects to</h3>
      <ul>
        {links.map((l) => {
          const [here, there] = l.from.block === block.id ? [l.from, l.to] : [l.to, l.from];
          const via = (l.turnouts ?? []).map((t) => `${(t.turnout && entities[t.turnout]?.name) || `turnout at ${t.x}, ${t.y}`} ${t.position}`).join(", ");
          return (
            <li key={`${l.from.x},${l.from.y},${l.from.dir}>${l.to.x},${l.to.y},${l.to.dir}:${via}`}>
              {names.get(there.block) ?? there.block} from the {DIRECTION_NAMES[here.dir]} end
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
        commit({ ...draft, background: { ...bg, image, height: round(bg.width * aspect, 3) } });
      } else {
        const placed = bounds(draft.tiles.values());
        const width = placed ? Math.max(placed.maxX - placed.minX + 1, 20) : area.maxX - area.minX + 1;
        commit({ ...draft, background: { image, x: placed?.minX ?? area.minX, y: placed?.minY ?? area.minY, width, height: round(width * aspect, 3), opacity: 0.4 } });
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
      {!bg && <p className="trk-sub muted">Put a plan from your track planning software under the grid to draw over it: export it as an image (PNG, JPEG, WebP, GIF or SVG) and choose it here.</p>}
      {bg && (
        <>
          <label className="trk-field">
            <span>Width in cells</span>
            <input
              className="input input--num"
              type="number"
              min={0.1}
              step={0.1}
              value={bg.width}
              onChange={(e) => {
                const width = Number(e.target.value);
                if (width > 0) set({ width: round(width, 2), height: round((bg.height / bg.width) * width, 3) }, "background-width");
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
          <p className="trk-sub muted">Move it with the Background tool. Match a known length on the plan, such as the distance between two parallel tracks, to whole cells.</p>
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
  const problems = (saved?.topology.problems ?? []).filter((p) => p.kind === "block_empty" || p.kind === "block_split");
  if (ends === 0 && problems.length === 0) return null;
  return (
    <Section title="To fix">
      <ul className="trk-problems">
        {ends > 0 && <li>{ends === 1 ? "One track end leads" : `${ends} track ends lead`} into a cell with no track; they're marked in amber.</li>}
        {problems.map((p) => (
          <li key={`${p.kind}${p.block}`}>{p.message}</li>
        ))}
      </ul>
    </Section>
  );
}
