import { type PointerEvent as ReactPointerEvent, useEffect, useRef } from "react";
import { type Entity, type PlanJunction, type PlanTrack, type TrackBackground, type TrackBlock, type TrackPlan, type Vec, backgroundURL } from "../api";
import { type Bounds, type ElementKind, type ElementRef, KINDS, type PlanIndex, add, arcInfo, corners, junctionCenter, outward, routePath, sameRef, scale, trackMiddle, trackPath, unit } from "../trackgeom";

/** How a block's track is drawn: by its detector's state when operating,
 * or by whether it is the block being worked on when editing. */
export type TrackTone = "plain" | "undetected" | "occupied" | "unknown" | "active";

interface DiagramProps {
  plan: TrackPlan;
  index: PlanIndex;
  entities: Record<string, Entity>;
  /** The part of the plan drawn, in millimeters. */
  area: Bounds;
  /** Pixels per millimeter. */
  zoom: number;
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
  onPointerLeave?: () => void;
  /** Clicking a linked turnout or crossover while operating. */
  onTurnout?: (j: PlanJunction) => void;
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

/** The locomotives a block's reporter sees, by name. */
export function trainsIn(block: TrackBlock | undefined, entities: Record<string, Entity>): string[] {
  const rep = block?.reporter ? entities[block.reporter] : undefined;
  return rep?.locomotives?.map((l) => l.name) ?? [];
}

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

/** The track diagram: track, platforms and labels to scale, lit by the
 * layout's state. */
export function TrackDiagram(props: DiagramProps) {
  const { plan, index, area, zoom, editing = false, entities, activeBlock, hiddenLayers } = props;
  const svgRef = useRef<SVGSVGElement>(null);

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

  const px = (n: number) => n / zoom;
  const width = area.maxX - area.minX;
  const height = area.maxY - area.minY;
  const shown = (layer?: string) => !layer || !hiddenLayers?.has(layer);
  const blocks = new Map(plan.blocks.map((b) => [b.id, b]));
  const tracks = plan.tracks.filter((t) => shown(t.layer));
  const junctions = plan.junctions.filter((j) => shown(j.layer));
  const pos = (id: string) => index.points.get(id);

  const pointAt = (e: ReactPointerEvent<SVGSVGElement>): Vec => {
    const rect = e.currentTarget.getBoundingClientRect();
    return { x: area.minX + ((e.clientX - rect.left) / rect.width) * width, y: area.minY + ((e.clientY - rect.top) / rect.height) * height };
  };

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
      const trains = trainsIn(block, entities);
      if (trains.length > 0) tags.push({ id, at, text: trains.join(", "), train: true });
    }
  }

  const bumpers = plan.points.filter((p) => p.bumper && index.at.get(p.id)?.length === 1);

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
      return p && `M${corners(p, p.length, p.width, p.angle)
        .map((c) => `${c.x} ${c.y}`)
        .join("L")}Z`;
    }
    const l = plan.labels.find((x) => x.id === ref.id);
    return l && `M${l.x} ${l.y - px(9)}h${px(l.text.length * 7 + 4)}v${px(18)}h${-px(l.text.length * 7 + 4)}Z`;
  }
  const selectedPath = props.selected?.map(pathOf).join("");
  const hoverPath = props.hover && !props.selected?.some((r) => sameRef(r, props.hover)) && pathOf(props.hover);
  const grid = editing ? gridSpacing(plan.grid, zoom) : 0;

  return (
    <svg
      ref={svgRef}
      className={editing ? "trk-diagram trk-diagram--editing" : "trk-diagram"}
      width={width * zoom}
      height={height * zoom}
      viewBox={`${area.minX} ${area.minY} ${width} ${height}`}
      role="img"
      aria-label="Track diagram"
      onPointerDown={props.onPointerDown && ((e) => props.onPointerDown?.(pointAt(e), e, targetAt(e.clientX, e.clientY)))}
      onPointerMove={props.onPointerMove && ((e) => props.onPointerMove?.(pointAt(e), e, targetAt(e.clientX, e.clientY)))}
      onPointerUp={props.onPointerUp}
      onPointerLeave={props.onPointerLeave}
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
          <text key={`g${tag.id}`} className={tag.id === activeBlock ? "trk-blockname trk-blockname--active" : "trk-blockname"} x={tag.at.x} y={tag.at.y + px(14)} fontSize={px(10)} textAnchor="middle">
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
                  {!editing && ent && <title>{`${ent.name}: ${ent.state}. Click to ${ent.state === "closed" ? "throw" : "close"} it.`}</title>}
                </path>
              );
            })}
          {editing &&
            plan.labels
              .filter((l) => shown(l.layer))
              .map((l) => <rect key={l.id} data-kind="label" data-id={l.id} x={l.x} y={l.y - px(9)} width={px(l.text.length * 7 + 4)} height={px(18)} fill="transparent" stroke="none" />)}
        </g>
      )}

      {props.marks?.map((m, i) => (
        <circle key={`m${i}`} className="trk-open" cx={m.x} cy={m.y} r={px(4)} />
      ))}
      {props.preview?.map((d, i) => (
        <path key={`v${i}`} className="trk-preview" d={d} vectorEffect="non-scaling-stroke" />
      ))}
      {props.snapTo && <circle className="trk-snap" cx={props.snapTo.x} cy={props.snapTo.y} r={px(7)} vectorEffect="non-scaling-stroke" />}
      {props.box && <rect className="trk-box" x={props.box.minX} y={props.box.minY} width={props.box.maxX - props.box.minX} height={props.box.maxY - props.box.minY} vectorEffect="non-scaling-stroke" />}
    </svg>
  );
}

function Junction({ j, index, tone, entities, blocks, editing }: { j: PlanJunction; index: PlanIndex; tone: TrackTone; entities: Record<string, Entity>; blocks: Map<string, TrackBlock>; editing: boolean }) {
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
        <path key={`${r.from}-${r.to}`} d={routePath(j.kind, ends as Vec[], center, r)} stroke={lined && r.position && r.position !== lined ? "var(--lamp-off)" : color} vectorEffect="non-scaling-stroke" />
      ))}
    </g>
  );
}
