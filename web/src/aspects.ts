// How a signal head's aspects read, and the lamp that stands for each;
// and how a signal on the plan reads as a whole.
import type { SignalState } from "./api";
import type { LampColor } from "./components/Lamp";

const ASPECTS: Record<string, { word: string; color: LampColor; pulse?: boolean }> = {
  dark: { word: "Dark", color: "off" },
  red: { word: "Red", color: "red" },
  yellow: { word: "Yellow", color: "amber" },
  green: { word: "Green", color: "green" },
  lunar: { word: "Lunar", color: "lunar" },
  s: { word: "S", color: "lunar" },
  d: { word: "D", color: "lunar" },
  number: { word: "Number", color: "lunar" },
  flashing_red: { word: "Flashing red", color: "red", pulse: true },
  flashing_yellow: { word: "Flashing yellow", color: "amber", pulse: true },
  flashing_green: { word: "Flashing green", color: "green", pulse: true },
};

/** The word for an aspect. */
export function aspectWord(aspect: string): string {
  return ASPECTS[aspect]?.word ?? "Unknown";
}

/** The lamp and word for a head showing an aspect. */
export function aspectState(aspect: string): { color: LampColor; pulse?: boolean; word: string } {
  return ASPECTS[aspect] ?? { color: "off", word: "Unknown" };
}

/** The kind of signal, in words. */
export function signalKind(s?: SignalState): string {
  if (s?.kind === "home") return "Home signal";
  if (s?.kind === "automatic") return "Automatic signal";
  return "Signal";
}

/** What its heads show: "Green over yellow", or "Red" with one head. */
export function signalAspects(s: SignalState): string {
  if (!s.lower) return aspectWord(s.upper);
  return `${aspectWord(s.upper)} over ${aspectWord(s.lower).toLowerCase()}`;
}

/** What it tells a train. */
export function signalMeaning(s: SignalState): string {
  if (s.indication === "stop") return s.reason ? `Stop. ${s.reason}` : "Stop.";
  const way = s.kind === "home" ? (s.diverging ? ", on the diverging route" : ", on the straight route") : "";
  return s.indication === "clear" ? `Proceed${way}. The next signal is clear.` : `Proceed${way}, ready to stop at the next signal.`;
}
