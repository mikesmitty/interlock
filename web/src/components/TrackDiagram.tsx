import { type PointerEvent as ReactPointerEvent, useEffect, useRef } from "react";
import { type Entity, type TrackBackground, type TrackBlock, type TrackDirection, type TrackTile, backgroundURL } from "../api";
import { type Bounds, type Cell, classify, step, switches } from "../track";

/** How a block's track is drawn: by its detector's state when operating,
 * or by whether it is the block being worked on when editing. */
export type TrackTone = "plain" | "undetected" | "occupied" | "unknown" | "active";

interface DiagramProps {
  tiles: Iterable<TrackTile>;
  blocks: TrackBlock[];
  entities: Record<string, Entity>;
  /** The cells drawn, inclusive. */
  area: Bounds;
  /** Pixels per cell. */
  cell: number;
  editing?: boolean;
  /** The block being worked on, drawn in brass while editing. */
  activeBlock?: string;
  selected?: Cell;
  hover?: Cell;
  openEnds?: { x: number; y: number; dir: TrackDirection }[];
  /** An image drawn under everything else. */
  background?: TrackBackground;
  /** The pointer's cell, and where in the grid it is to a fraction of a
   * cell. */
  onPointerDown?: (cell: Cell, e: ReactPointerEvent<SVGSVGElement>, at: { x: number; y: number }) => void;
  onPointerMove?: (cell: Cell, e: ReactPointerEvent<SVGSVGElement>, at: { x: number; y: number }) => void;
  onPointerUp?: (e: ReactPointerEvent<SVGSVGElement>) => void;
  onPointerLeave?: () => void;
  /** Clicking a linked turnout while operating. */
  onTurnout?: (tile: TrackTile) => void;
}

const TRACK_WIDTH = 0.14;

/** Where an end leaves the cell, relative to its top left corner. */
function exitPoint(x: number, y: number, d: TrackDirection): [number, number] {
  const [dx, dy] = step(d);
  return [x + 0.5 + dx * 0.5, y + 0.5 + dy * 0.5];
}

function pathData(x: number, y: number, a: TrackDirection, b: TrackDirection): string {
  const [ax, ay] = exitPoint(x, y, a);
  const [bx, by] = exitPoint(x, y, b);
  return `M${ax} ${ay}Q${x + 0.5} ${y + 0.5} ${bx} ${by}`;
}

function bumperData(x: number, y: number, d: TrackDirection): string {
  const [ax, ay] = exitPoint(x, y, d);
  const [dx, dy] = step(d);
  const len = Math.hypot(dx, dy);
  const [px, py] = [(-dy / len) * 0.22, (dx / len) * 0.22];
  const [cx, cy] = [x + 0.5, y + 0.5];
  return `M${ax} ${ay}L${cx} ${cy}M${cx - px} ${cy - py}L${cx + px} ${cy + py}`;
}

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

function toneOf(tile: TrackTile, blocks: Map<string, TrackBlock>, entities: Record<string, Entity>, editing: boolean, activeBlock?: string): TrackTone {
  if (!tile.block) return "undetected";
  if (editing) return tile.block === activeBlock ? "active" : "plain";
  const state = blockState(blocks.get(tile.block), entities);
  if (state === "occupied") return "occupied";
  if (state === "clear" || state === "no detector linked") return "plain";
  return "unknown";
}

/** The locomotives a block's reporter sees, by name. */
export function trainsIn(block: TrackBlock | undefined, entities: Record<string, Entity>): string[] {
  const rep = block?.reporter ? entities[block.reporter] : undefined;
  return rep?.locomotives?.map((l) => l.name) ?? [];
}

/** The track diagram: track, platforms and labels on the grid, lit by the
 * layout's state. */
export function TrackDiagram(props: DiagramProps) {
  const { area, cell, editing = false, entities, activeBlock } = props;
  const svgRef = useRef<SVGSVGElement>(null);

  // A firm press on a Mac trackpad opens Look Up, a dictionary, which
  // nobody drawing track wants. Safari lets a page cancel it; Chrome
  // doesn't, but finds no word to look up on the grid.
  useEffect(() => {
    const svg = svgRef.current;
    if (!editing || !svg) return;
    const cancel = (e: Event) => e.preventDefault();
    svg.addEventListener("webkitmouseforcewillbegin", cancel);
    return () => svg.removeEventListener("webkitmouseforcewillbegin", cancel);
  }, [editing]);
  const tiles = [...props.tiles];
  const blocks = new Map(props.blocks.map((b) => [b.id, b]));
  const width = area.maxX - area.minX + 1;
  const height = area.maxY - area.minY + 1;

  const pointAt = (e: ReactPointerEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    return {
      x: area.minX + ((e.clientX - rect.left) / rect.width) * width,
      y: area.minY + ((e.clientY - rect.top) / rect.height) * height,
    };
  };
  const cellAt = (at: { x: number; y: number }): Cell => ({ x: Math.floor(at.x), y: Math.floor(at.y) });

  // Each block's middle cell carries its name tag, or its train's.
  const members = new Map<string, TrackTile[]>();
  for (const t of tiles) {
    if (t.track && t.block) members.set(t.block, [...(members.get(t.block) ?? []), t]);
  }
  const tags: { id: string; x: number; y: number; text: string; train: boolean }[] = [];
  for (const [id, list] of members) {
    const sorted = [...list].sort((a, b) => a.x - b.x || a.y - b.y);
    const mid = sorted[Math.floor((sorted.length - 1) / 2)];
    const block = blocks.get(id);
    if (editing) tags.push({ id, x: mid.x, y: mid.y, text: block?.name ?? id, train: false });
    else {
      const trains = trainsIn(block, entities);
      if (trains.length > 0) tags.push({ id, x: mid.x, y: mid.y, text: trains.join(", "), train: true });
    }
  }

  return (
    <svg
      ref={svgRef}
      className={editing ? "trk-diagram trk-diagram--editing" : "trk-diagram"}
      width={width * cell}
      height={height * cell}
      viewBox={`${area.minX} ${area.minY} ${width} ${height}`}
      role="img"
      aria-label="Track diagram"
      onPointerDown={
        props.onPointerDown &&
        ((e) => {
          const at = pointAt(e);
          props.onPointerDown?.(cellAt(at), e, at);
        })
      }
      onPointerMove={
        props.onPointerMove &&
        ((e) => {
          const at = pointAt(e);
          props.onPointerMove?.(cellAt(at), e, at);
        })
      }
      onPointerUp={props.onPointerUp}
      onPointerLeave={props.onPointerLeave}
      // The grid is a drawing surface: a right-click there opens no menu.
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
      {editing && (
        <>
          <defs>
            <pattern id="trk-grid" width="1" height="1" patternUnits="userSpaceOnUse">
              <path d="M1 0H0V1" fill="none" stroke="var(--enamel-line)" strokeWidth={0.03} />
            </pattern>
          </defs>
          <rect x={area.minX} y={area.minY} width={width} height={height} fill="url(#trk-grid)" />
        </>
      )}

      <g className="trk-platforms">
        {tiles
          .filter((t) => t.platform?.length)
          .map((t) => (
            <g key={`p${t.x},${t.y}`}>
              <circle cx={t.x + 0.5} cy={t.y + 0.5} r={0.25} stroke="none" />
              {t.platform!.map((d) => {
                const [ex, ey] = exitPoint(t.x, t.y, d);
                return <line key={d} x1={t.x + 0.5} y1={t.y + 0.5} x2={ex} y2={ey} strokeWidth={0.5} />;
              })}
            </g>
          ))}
      </g>

      <g className="trk-track" fill="none" strokeWidth={TRACK_WIDTH} strokeLinecap="round">
        {tiles
          .filter((t) => t.track?.length)
          .map((t) => (
            <TrackCell key={`t${t.x},${t.y}`} tile={t} tone={toneOf(t, blocks, entities, editing, activeBlock)} entities={entities} blocks={blocks} editing={editing} />
          ))}
      </g>

      {tiles
        .filter((t) => t.label)
        .map((t) => (
          <text key={`l${t.x},${t.y}`} className="trk-label" x={t.x + 0.1} y={t.y + 0.5} fontSize={0.45} dominantBaseline="central">
            {t.label}
          </text>
        ))}

      {tags.map((tag) => {
        const w = tag.text.length * 0.2 + 0.4;
        return tag.train ? (
          <g key={`g${tag.x},${tag.y}`} className="trk-train">
            <rect x={tag.x + 0.5 - w / 2} y={tag.y - 0.45} width={w} height={0.5} rx={0.06} />
            <text x={tag.x + 0.5} y={tag.y - 0.2} fontSize={0.36} textAnchor="middle" dominantBaseline="central">
              {tag.text}
            </text>
          </g>
        ) : (
          <text key={`g${tag.x},${tag.y}`} className={tag.id === activeBlock ? "trk-blockname trk-blockname--active" : "trk-blockname"} x={tag.x + 0.5} y={tag.y + 0.95} fontSize={0.3} textAnchor="middle">
            {tag.text}
          </text>
        );
      })}

      {editing &&
        props.openEnds?.map((o) => {
          const [ex, ey] = exitPoint(o.x, o.y, o.dir);
          return <circle key={`o${o.x},${o.y},${o.dir}`} className="trk-open" cx={ex} cy={ey} r={0.13} />;
        })}

      {!editing &&
        props.onTurnout &&
        tiles
          .filter((t) => t.turnout && entities[t.turnout])
          .map((t) => {
            const ent = entities[t.turnout!];
            const next = ent.state === "closed" ? "Throw" : "Close";
            return (
              <rect key={`s${t.x},${t.y}`} className="trk-switch" x={t.x} y={t.y} width={1} height={1} onClick={() => props.onTurnout?.(t)}>
                <title>{`${ent.name}: ${ent.state}. Click to ${next.toLowerCase()} it.`}</title>
              </rect>
            );
          })}

      {props.hover && <rect className="trk-hover" x={props.hover.x + 0.04} y={props.hover.y + 0.04} width={0.92} height={0.92} />}
      {props.selected && <rect className="trk-selected" x={props.selected.x + 0.03} y={props.selected.y + 0.03} width={0.94} height={0.94} />}
    </svg>
  );
}

function TrackCell({ tile, tone, entities, blocks, editing }: { tile: TrackTile; tone: TrackTone; entities: Record<string, Entity>; blocks: Map<string, TrackBlock>; editing: boolean }) {
  const { x, y } = tile;
  const shape = classify(tile.track ?? []);
  const color = toneColor[tone];
  const block = tile.block ? blocks.get(tile.block) : undefined;
  const title = block ? `${block.name}: ${blockState(block, entities)}` : "Not in a block";
  if (!shape) {
    // Saved plans are checked, so this is only a cell mid-edit.
    return (
      <g stroke="var(--lamp-amber)">
        {(tile.track ?? []).map((d) => {
          const [ex, ey] = exitPoint(x, y, d);
          return <line key={d} x1={x + 0.5} y1={y + 0.5} x2={ex} y2={ey} />;
        })}
      </g>
    );
  }
  if (shape.piece === "bumper") {
    return (
      <path d={bumperData(x, y, tile.track![0])} stroke={color}>
        <title>{title}</title>
      </path>
    );
  }
  // A turnout shows the way it is set lit and the other way unlit, as on
  // a panel; until its position is known both ways stay lit.
  const turnout = switches(shape.piece) && tile.turnout ? entities[tile.turnout] : undefined;
  const lined = !editing && turnout && (turnout.state === "closed" || turnout.state === "thrown") ? turnout.state : undefined;
  const paths = [...shape.paths].sort((a, b) => Number(a.position === lined) - Number(b.position === lined));
  return (
    <g>
      <title>{turnout ? `${title}. ${turnout.name}: ${turnout.state}` : title}</title>
      {paths.map((p) => (
        <path key={`${p.a}${p.b}`} d={pathData(x, y, p.a, p.b)} stroke={lined && p.position !== lined ? "var(--lamp-off)" : color} />
      ))}
    </g>
  );
}
