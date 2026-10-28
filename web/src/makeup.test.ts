import { test } from "node:test";
import assert from "node:assert/strict";
import type { Car, Formation, Train } from "./api.ts";
import { formationOf, lengthText, makeupLength, makeupText, vehicleInfo } from "./makeup.ts";

const trains = { motor: { id: "motor", name: "Motor car", length: 114 } as Train };
const cars: Record<string, Car> = {
  c1: { id: "c1", name: "Trailer 1", length: 114 },
  c2: { id: "c2", name: "Trailer 2" },
};

test("a train's length adds up its locomotives and cars", () => {
  const vehicles = [
    { locomotive: "motor", reverse: false },
    { car: "c1", reverse: false },
    { car: "c2", reverse: true },
  ];
  assert.deepEqual(makeupLength(vehicles, trains, cars), { length: 228, unmeasured: 1 });
  assert.deepEqual(makeupLength([], trains, cars), { length: 0, unmeasured: 0 });
});

test("a vehicle links to its own page", () => {
  assert.equal(vehicleInfo({ locomotive: "motor", reverse: false }, trains, cars).href, "/roster/motor");
  assert.deepEqual(vehicleInfo({ car: "c1", reverse: false }, trains, cars), { kind: "car", id: "c1", name: "Trailer 1", length: 114, href: "/roster/cars/c1" });
  assert.equal(vehicleInfo({ car: "gone", reverse: false }, trains, cars).name, "gone", "not in the roster");
});

test("what a train is made of reads as a list", () => {
  const f = (vehicles: Formation["vehicles"]) => ({ vehicles });
  assert.equal(makeupText(f([]), trains), "Nothing yet");
  assert.equal(makeupText(f([{ car: "c1", reverse: false }]), trains), "1 car");
  assert.equal(makeupText(f([{ locomotive: "motor", reverse: false }]), trains), "Motor car");
  assert.equal(
    makeupText(
      f([
        { car: "c1", reverse: false },
        { locomotive: "motor", reverse: false },
        { car: "c2", reverse: false },
      ]),
      trains,
    ),
    "Motor car and 2 cars",
  );
});

test("a car is found in the train that holds it", () => {
  const formations: Record<string, Formation> = {
    a: { id: "a", name: "A", vehicles: [{ car: "c1", reverse: false }], length: 114, unmeasured: 0 },
    b: { id: "b", name: "B", vehicles: [{ locomotive: "motor", reverse: false }], length: 114, unmeasured: 0 },
  };
  assert.equal(formationOf({ car: "c1" }, formations)?.id, "a");
  assert.equal(formationOf({ locomotive: "motor" }, formations)?.id, "b");
  assert.equal(formationOf({ car: "c1" }, formations, "a"), undefined);
  assert.equal(formationOf({ car: "c2" }, formations), undefined);
});

test("lengths read in millimeters", () => {
  assert.equal(lengthText(456), "456 mm");
  assert.equal(lengthText(242.54), "242.5 mm");
});
