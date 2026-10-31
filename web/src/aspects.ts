// How a signal head's aspects read, and the lamp that stands for each.
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
