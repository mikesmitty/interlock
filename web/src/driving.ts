// Drives as the Track page shows them: what a train being driven is
// doing, in words, and the steps a speed measuring run measures.
import type { Drive } from "./api";

/** Whether a drive is still going. */
export const driveActive = (d?: Drive): boolean => !!d && (d.state === "starting" || d.state === "running" || d.state === "braking" || d.state === "stopping");

const mm = (n: number) => `${Math.round(n)} mm`;

/** What a drive is doing, or how it ended, in words. */
export function driveText(d: Drive, blockName: (id: string) => string): string {
  const to = d.to ? blockName(d.to) : "";
  if (d.kind === "measure") {
    const m = d.measuring;
    const done = m ? `${m.done.length} of ${m.steps.length} measured` : "";
    switch (d.state) {
      case "starting":
      case "running":
      case "braking":
        return `Measuring its speed at step ${m?.at ?? d.speed}, ${done}`;
      case "stopping":
        return "Stopping after measuring its speeds";
      case "measured": {
        const skipped = m?.skipped?.length ? `; step ${m.skipped.join(", ")} couldn't be measured` : "";
        return `Speeds measured at step ${m?.done.join(", ") ?? ""}${skipped}`;
      }
    }
  }
  switch (d.state) {
    case "starting":
      return `Setting its routes to ${to}`;
    case "running":
      return `Running to ${to}, ${mm(d.remaining)} to go`;
    case "braking":
      return `Braking for ${to}, ${mm(d.remaining)} to go`;
    case "stopping":
      return `Stopping, ${mm(d.remaining)} to go`;
    case "arrived": {
      const where = d.marker ? "the marker" : "where the track ends";
      const off = d.off ?? 0;
      if (Math.abs(off) < 1) return `Stopped in ${to} at ${where}`;
      return `Stopped in ${to}, ${mm(Math.abs(off))} ${off > 0 ? "short of" : "past"} ${where}`;
    }
  }
  if (d.error) return `Stopped: ${d.error}`;
  return d.state === "failed" ? "The drive failed" : "Stopped when asked";
}

/** The steps a speed measuring run takes up to the fastest: count of
 * them, evenly spaced. */
export function stepsUpTo(fastest: number, count = 6): number[] {
  const top = Math.min(Math.max(Math.round(fastest), 1), 126);
  const steps = new Set<number>();
  for (let i = 1; i <= count; i++) steps.add(Math.max(1, Math.round((top * i) / count)));
  return [...steps].sort((a, b) => a - b);
}
