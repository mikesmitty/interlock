import { Link } from "react-router";
import { aliasHex } from "../api";
import { fastHM, fastNow, rateLabel } from "../clock";
import { useNow } from "../format";
import { useBus } from "../store";
import { Lamp } from "./Lamp";

/** The strip across the top: one lamp per link, the fast clock, and the controller's own state. */
export function StatusStrip() {
  const { connection, links, health, nodes, clock } = useBus();
  const now = useNow(1000);
  const local = Object.values(nodes).find((n) => n.local);

  const serverColor = connection === "live" ? "green" : connection === "reconnecting" ? "red" : "amber";
  const serverLabel = connection === "live" ? "Connected to server" : connection === "reconnecting" ? "Server connection lost, retrying" : "Connecting to server";

  let nodeColor: "green" | "amber" | "off" = "off";
  let nodeLabel = "Controller node not started";
  if (local?.online) {
    nodeColor = "green";
    nodeLabel = "Controller node on the bus";
  } else if (health && health.node_state === "checking") {
    nodeColor = "amber";
    nodeLabel = "Controller node joining the bus";
  }

  return (
    <div className="strip" role="status" aria-live="polite">
      <div className="strip__group">
        <span className="strip__item" title={serverLabel}>
          <Lamp color={serverColor} label={serverLabel} />
          <span>{connection === "live" ? "Server" : connection === "reconnecting" ? "Reconnecting" : "Connecting"}</span>
        </span>
      </div>
      <div className="strip__group">
        {links.length === 0 && (
          <Link className="strip__item" to="/settings">
            <Lamp color="off" label="No links configured" />
            <span>No links</span>
          </Link>
        )}
        {links.map((link) => (
          <Link
            key={link.name}
            className="strip__item"
            to="/settings"
            title={link.connected ? `${link.name}: connected to ${link.target}` : `${link.name}: not connected${link.last_error ? ` (${link.last_error})` : ""}`}
          >
            <Lamp color={link.connected ? "green" : "red"} label={link.connected ? "connected" : "not connected"} />
            <span>{link.name}</span>
          </Link>
        ))}
      </div>
      <span className="strip__spacer" />
      {clock && (
        <div className="strip__group">
          <Link className="strip__item strip__clock" to="/clock" title={clock.running ? `Fast clock running at ${rateLabel(clock.rate)}` : "Fast clock stopped"}>
            <Lamp color={clock.running ? "green" : "off"} label={clock.running ? "fast clock running" : "fast clock stopped"} />
            <span>{fastHM(fastNow(clock, now))}</span>
            <span className="strip__id">{clock.running ? rateLabel(clock.rate) : "stopped"}</span>
          </Link>
        </div>
      )}
      <div className="strip__group">
        <span className="strip__item" title={nodeLabel}>
          <Lamp color={nodeColor} label={nodeLabel} />
          <span>{local?.snip?.UserName || "Interlock"}</span>
          {local && (
            <span className="strip__id">
              {local.id}
              {local.alias ? ` as ${aliasHex(local.alias)}` : ""}
            </span>
          )}
        </span>
      </div>
    </div>
  );
}
