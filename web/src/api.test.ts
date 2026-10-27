import { test } from "node:test";
import assert from "node:assert/strict";
import { trainNodeAddress } from "./api.ts";

test("trainNodeAddress reads DCC train node IDs", () => {
  assert.deepEqual(trainNodeAddress("06.01.00.00.C4.D2"), { address: 1234, long: true });
  assert.deepEqual(trainNodeAddress("06.01.00.00.00.03"), { address: 3, long: false });
  assert.deepEqual(trainNodeAddress("06.01.00.00.C0.03"), { address: 3, long: true });
  assert.deepEqual(trainNodeAddress("06.01.00.00.00.FF"), { address: 255, long: true });
});

test("trainNodeAddress leaves other nodes alone", () => {
  // A command station's own node elsewhere in 06.01 is no locomotive.
  assert.equal(trainNodeAddress("06.01.49.6D.83.4F"), undefined);
  assert.equal(trainNodeAddress("0A.FF.90.24.A6.34"), undefined);
  assert.equal(trainNodeAddress("06.01.00.00.C4"), undefined);
});
