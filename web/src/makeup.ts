// Trains made up of locomotives and cars, as the roster pages show them:
// their lengths, what they are made of and which train holds a car.
import type { Car, Formation, Train, Vehicle } from "./api";

/** A vehicle's locomotive or car, as the roster has it. */
export interface VehicleInfo {
  kind: "locomotive" | "car";
  id: string;
  name: string;
  length?: number;
  /** The page it is edited on. */
  href: string;
}

export function vehicleInfo(v: Vehicle, trains: Record<string, Train>, cars: Record<string, Car>): VehicleInfo {
  if (v.locomotive) {
    const t = trains[v.locomotive];
    return { kind: "locomotive", id: v.locomotive, name: t?.name ?? v.locomotive, length: t?.length, href: `/roster/${encodeURIComponent(v.locomotive)}` };
  }
  const c = cars[v.car ?? ""];
  return { kind: "car", id: v.car ?? "", name: c?.name ?? v.car ?? "", length: c?.length, href: `/roster/cars/${encodeURIComponent(v.car ?? "")}` };
}

/** A train's length from its vehicles, and how many have no length given. */
export function makeupLength(vehicles: Vehicle[], trains: Record<string, Train>, cars: Record<string, Car>): { length: number; unmeasured: number } {
  let length = 0;
  let unmeasured = 0;
  for (const v of vehicles) {
    const l = vehicleInfo(v, trains, cars).length ?? 0;
    if (l <= 0) unmeasured++;
    length += l;
  }
  return { length, unmeasured };
}

/** "Local 1 motor car and 3 cars": a train's locomotives by name, then its cars counted. */
export function makeupText(f: Pick<Formation, "vehicles">, trains: Record<string, Train>): string {
  const locos = f.vehicles.filter((v) => v.locomotive).map((v) => trains[v.locomotive!]?.name ?? v.locomotive!);
  const cars = f.vehicles.length - locos.length;
  const parts = [...locos];
  if (cars > 0) parts.push(`${cars} car${cars === 1 ? "" : "s"}`);
  if (parts.length === 0) return "Nothing yet";
  if (parts.length === 1) return parts[0];
  return `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
}

/** The made-up train a locomotive or car is in, other than skip. */
export function formationOf(v: Pick<Vehicle, "locomotive" | "car">, formations: Record<string, Formation>, skip?: string): Formation | undefined {
  return Object.values(formations).find((f) => f.id !== skip && f.vehicles.some((w) => (v.locomotive ? w.locomotive === v.locomotive : w.car === v.car)));
}

/** A length in millimeters as the pages write it: "456 mm". */
export function lengthText(mm: number): string {
  return `${Math.round(mm * 10) / 10} mm`;
}
