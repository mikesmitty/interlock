import { useEffect } from "react";
import { NavLink, Navigate, Outlet, Route, Routes } from "react-router";
import { StatusStrip } from "./components/StatusStrip";
import { AutomationEditor } from "./pages/AutomationEditor";
import { Automations } from "./pages/Automations";
import { AutomationTraces } from "./pages/AutomationTraces";
import { Clock } from "./pages/Clock";
import { DecoderSettings } from "./pages/DecoderSettings";
import { Entities } from "./pages/Entities";
import { Monitor } from "./pages/Monitor";
import { NodeDetail } from "./pages/NodeDetail";
import { Nodes } from "./pages/Nodes";
import { Programming } from "./pages/Programming";
import { Settings } from "./pages/Settings";
import { Throttle } from "./pages/Throttle";
import { bus, useBus } from "./store";

function Shell() {
  const { health } = useBus();
  useEffect(() => {
    bus.start();
  }, []);
  return (
    <div className="shell">
      <nav className="rail" aria-label="Main">
        <NavLink className="rail__brand" to="/nodes">
          Interlock
        </NavLink>
        <NavLink className="rail__link" to="/nodes">
          Nodes
        </NavLink>
        <NavLink className="rail__link" to="/entities">
          Entities
        </NavLink>
        <NavLink className="rail__link" to="/throttle">
          Throttle
        </NavLink>
        <NavLink className="rail__link" to="/automations">
          Automations
        </NavLink>
        <NavLink className="rail__link" to="/programming">
          Programming
        </NavLink>
        <NavLink className="rail__link" to="/clock">
          Clock
        </NavLink>
        <NavLink className="rail__link" to="/monitor">
          Monitor
        </NavLink>
        <NavLink className="rail__link" to="/settings">
          Settings
        </NavLink>
        <div className="rail__foot">{health ? `v${health.version}` : ""}</div>
      </nav>
      <div className="main">
        <StatusStrip />
        <Outlet />
      </div>
    </div>
  );
}

function NotFound() {
  return (
    <section className="page">
      <h1>Nothing here</h1>
      <p>There is no page at this address. Try the nodes list.</p>
    </section>
  );
}

export default function App() {
  return (
    <Routes>
      <Route element={<Shell />}>
        <Route index element={<Navigate to="/nodes" replace />} />
        <Route path="nodes" element={<Nodes />} />
        <Route path="nodes/:id" element={<NodeDetail />} />
        <Route path="entities" element={<Entities />} />
        <Route path="automations" element={<Automations />} />
        <Route path="automations/new" element={<AutomationEditor />} />
        <Route path="automations/:id" element={<AutomationEditor />} />
        <Route path="automations/:id/traces" element={<AutomationTraces />} />
        <Route path="throttle" element={<Throttle />} />
        <Route path="throttle/:id" element={<Throttle />} />
        <Route path="programming" element={<Programming />} />
        <Route path="programming/decoder" element={<DecoderSettings />} />
        <Route path="clock" element={<Clock />} />
        <Route path="monitor" element={<Monitor />} />
        <Route path="settings" element={<Settings />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
