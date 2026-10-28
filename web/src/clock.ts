// The fast clock as the pages show it: the server sends its state on each
// change and each fast minute; between those the display runs on from the
// last state at the clock's rate.
import type { ClockState } from "./api";

/** The fast time right now, extrapolated from the last state. */
export function fastNow(state: ClockState, now: number): Date {
  const base = Date.UTC(Number(state.date.slice(0, 4)), Number(state.date.slice(5, 7)) - 1, Number(state.date.slice(8, 10)), state.hour, state.minute, state.second);
  if (!state.running) return new Date(base);
  const elapsed = now - new Date(state.updated).getTime();
  return new Date(base + Math.max(0, elapsed) * state.rate);
}

/** "06:42" */
export function fastHM(d: Date): string {
  return `${String(d.getUTCHours()).padStart(2, "0")}:${String(d.getUTCMinutes()).padStart(2, "0")}`;
}

/** "06:42:07" */
export function fastHMS(d: Date): string {
  return `${fastHM(d)}:${String(d.getUTCSeconds()).padStart(2, "0")}`;
}

/** "Friday, June 21, 1957" */
export function fastDate(d: Date): string {
  return d.toLocaleDateString(undefined, { weekday: "long", year: "numeric", month: "long", day: "numeric", timeZone: "UTC" });
}

/** "4×", "1.5×" */
export function rateLabel(rate: number): string {
  return `${Number.isInteger(rate) ? rate : rate.toFixed(2).replace(/0+$/, "")}×`;
}

export const RATES = [1, 2, 3, 4, 6, 8, 12, 24];
