import { useEffect } from "react";
import { NavLink, Navigate, Outlet, Route, Routes } from "react-router";
import { StatusStrip } from "./components/StatusStrip";
import { Entities } from "./pages/Entities";
import { Monitor } from "./pages/Monitor";
import { NodeDetail } from "./pages/NodeDetail";
import { Nodes } from "./pages/Nodes";
import { Settings } from "./pages/Settings";
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
        <Route path="monitor" element={<Monitor />} />
        <Route path="settings" element={<Settings />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
