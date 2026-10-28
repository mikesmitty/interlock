import { test } from "node:test";
import assert from "node:assert/strict";
import type { Route } from "./api.ts";
import { heldLegs, routeName, routeStateText, routesFrom, setRoutes } from "./routes.ts";

const port = (block: string, point: string) => ({ block, point, heading: 0 });
const route = (id: string, from: string, to: string, extra: Partial<Route> = {}): Route => ({ id, from: port(from, "p1"), to: port(to, "p2"), length: 100, legs: [], ...extra });
const blocks: Record<string, string> = { east: "East outer", main: "Main St outer", siding: "Main St siding" };
const blockName = (id: string) => blocks[id] ?? id;
const turnoutName = (j: string) => ({ "main-east": "Main St east", x: "Crossover" })[j] ?? j;

test("a route is named by the blocks it joins", () => {
  const r = route("east~main", "east", "main", { turnouts: [{ junction: "main-east", position: "closed" }] });
  assert.equal(routeName(r, [r], blockName, turnoutName), "East outer → Main St outer");
});

test("two ways between the same blocks are told apart by what they throw", () => {
  const straight = route("a~b", "a", "b", { turnouts: [{ junction: "x", position: "closed" }] });
  const across = route("a~b~2", "a", "b", { turnouts: [{ junction: "x", position: "thrown" }] });
  assert.equal(routeName(across, [straight, across], blockName, turnoutName), "a → b, Crossover thrown");
  assert.equal(routeName(straight, [straight, across], blockName, turnoutName), "a → b, straight through");
  const other = route("a~b~3", "a", "b");
  assert.equal(routeName(straight, [straight, other], blockName, turnoutName), "a → b (a~b)");
});

test("the routes from a block are listed by where they lead", () => {
  const all = [route("east~siding", "east", "siding"), route("main~east", "main", "east"), route("east~main", "east", "main")];
  assert.deepEqual(
    routesFrom("east", all, blockName).map((r) => r.id),
    ["east~main", "east~siding"],
  );
});

test("the routes set are those with a state, the longest set first", () => {
  const all = [
    route("b", "a", "b", { state: "set", since: "2026-10-28T10:00:01Z" }),
    route("c", "a", "c"),
    route("a", "a", "d", { state: "occupied", since: "2026-10-28T10:00:00Z" }),
  ];
  assert.deepEqual(
    setRoutes(all).map((r) => r.id),
    ["a", "b"],
  );
});

test("a route holds the track not yet released behind its train", () => {
  const legs = [{ piece: "j1", junction: true }, { piece: "t2" }, { piece: "j2", junction: true }];
  assert.deepEqual(heldLegs(route("r", "a", "b", { legs, state: "occupied", released: 1 })), legs.slice(1));
  assert.deepEqual(heldLegs(route("r", "a", "b", { legs })), []);
});

test("what a route set is doing reads plainly", () => {
  const trainName = (id: string) => (id === "local-1" ? "Local 1" : id);
  assert.equal(routeStateText(route("r", "a", "b", { state: "setting", waiting: ["main-east"] }), trainName, turnoutName), "Waiting for Main St east");
  assert.equal(routeStateText(route("r", "a", "b", { state: "set" }), trainName, turnoutName), "Set");
  assert.equal(routeStateText(route("r", "a", "b", { state: "occupied", train: "local-1" }), trainName, turnoutName), "Local 1 on it");
  assert.equal(
    routeStateText(route("r", "a", "b", { state: "occupied", train: "local-1", waiting: ["main-east"] }), trainName, turnoutName),
    "Local 1 on it, but Main St east moved",
  );
  assert.equal(routeStateText(route("r", "a", "b"), trainName, turnoutName), "Not set");
});
