// The part of the track plan a canvas shows, and how panning and zooming
// change it. Places on the canvas are in pixels from its top left
// corner, places on the plan in millimeters.
import type { Vec } from "./api";
import type { Bounds } from "./trackgeom";

/** The plan's point at the middle of the canvas, and pixels per
 * millimeter. */
export interface View {
  x: number;
  y: number;
  zoom: number;
}

export const MIN_ZOOM = 0.02;
export const MAX_ZOOM = 40;

const clampZoom = (zoom: number) => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoom));

/** The view that shows all of area on a canvas size pixels across. */
export function fitView(area: Bounds, size: Vec): View {
  const width = Math.max(area.maxX - area.minX, 1);
  const height = Math.max(area.maxY - area.minY, 1);
  return { x: (area.minX + area.maxX) / 2, y: (area.minY + area.maxY) / 2, zoom: clampZoom(Math.min(size.x / width, size.y / height)) };
}

/** The part of the plan a view shows. */
export function viewBounds(view: View, size: Vec): Bounds {
  const w = size.x / 2 / view.zoom;
  const h = size.y / 2 / view.zoom;
  return { minX: view.x - w, minY: view.y - h, maxX: view.x + w, maxY: view.y + h };
}

/** Where on the plan a place on the canvas is. */
export function toPlan(view: View, size: Vec, at: Vec): Vec {
  return { x: view.x + (at.x - size.x / 2) / view.zoom, y: view.y + (at.y - size.y / 2) / view.zoom };
}

/** The view after the plan under from on the canvas is carried to to
 * and zoomed by factor there, as by two fingers pinching. */
export function pinchView(view: View, size: Vec, from: Vec, to: Vec, factor: number): View {
  const p = toPlan(view, size, from);
  const zoom = clampZoom(view.zoom * factor);
  return { x: p.x - (to.x - size.x / 2) / zoom, y: p.y - (to.y - size.y / 2) / zoom, zoom };
}

/** The view zoomed by factor, the plan under at staying there. */
export const zoomAt = (view: View, size: Vec, factor: number, at: Vec): View => pinchView(view, size, at, at, factor);

/** The view zoomed by factor about the middle of the canvas. */
export const zoomBy = (view: View, factor: number): View => ({ ...view, zoom: clampZoom(view.zoom * factor) });

/** The view after the plan is dragged by pixels across the canvas. */
export const panBy = (view: View, by: Vec): View => ({ ...view, x: view.x - by.x / view.zoom, y: view.y - by.y / view.zoom });
