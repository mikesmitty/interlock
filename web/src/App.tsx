import { useEffect, useState } from "react";

interface Health {
  status: string;
  version: string;
  commit?: string;
  node_id: string;
  uptime_seconds: number;
}

type State =
  | { kind: "loading" }
  | { kind: "ok"; health: Health }
  | { kind: "error"; message: string };

export default function App() {
  const [state, setState] = useState<State>({ kind: "loading" });

  useEffect(() => {
    let cancelled = false;
    fetch("/api/v1/health")
      .then(async (res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return (await res.json()) as Health;
      })
      .then((health) => {
        if (!cancelled) setState({ kind: "ok", health });
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ kind: "error", message: err instanceof Error ? err.message : String(err) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <main className="shell">
      <h1>Interlock</h1>
      <p className="tagline">LCC layout controller</p>
      {state.kind === "loading" && <p>Connecting to the server…</p>}
      {state.kind === "error" && <p className="error">Could not reach the server: {state.message}</p>}
      {state.kind === "ok" && (
        <dl className="facts">
          <dt>Status</dt>
          <dd>{state.health.status}</dd>
          <dt>Version</dt>
          <dd>
            {state.health.version}
            {state.health.commit ? ` (${state.health.commit})` : ""}
          </dd>
          <dt>Node ID</dt>
          <dd>
            <code>{state.health.node_id}</code>
          </dd>
          <dt>Uptime</dt>
          <dd>{Math.round(state.health.uptime_seconds)} s</dd>
        </dl>
      )}
    </main>
  );
}
