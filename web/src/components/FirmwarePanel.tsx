import { useEffect, useRef, useState } from "react";
import { api, type FirmwareJob, type FirmwarePhase, type NodeInfo } from "../api";
import { relativeTime, useNow } from "../format";
import { Lamp, Status } from "./Lamp";

/** Where an upgrade is, in a few words. */
const PHASE_WORDS: Record<FirmwarePhase, string> = {
  checking: "Checking",
  freezing: "Preparing",
  writing: "Sending",
  unfreezing: "Checking the image",
  restarting: "Restarting",
  verifying: "Verifying",
  done: "Installed",
  failed: "Failed",
};

function kb(n: number): string {
  return n >= 1024 ? `${Math.round(n / 1024)} KB` : `${n} bytes`;
}

/**
 * The firmware section of a node's page: the version it runs, a picker
 * for a new image, and the upgrade's progress while it runs.
 */
export function FirmwarePanel({ node, job }: { node: NodeInfo; job?: FirmwareJob }) {
  const now = useNow();
  const [file, setFile] = useState<File>();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const input = useRef<HTMLInputElement>(null);
  const active = job !== undefined && job.phase !== "done" && job.phase !== "failed";
  const upgradeActive = node.protocols?.includes("Firmware Upgrade Active");

  // A finished upgrade clears the picker so the next file starts fresh.
  useEffect(() => {
    if (!active) {
      setConfirming(false);
    }
  }, [active]);

  async function install() {
    if (!file) return;
    setBusy(true);
    setError(undefined);
    try {
      await api.installFirmware(node.id, file);
      setFile(undefined);
      setConfirming(false);
      if (input.current) input.current.value = "";
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function cancel() {
    setBusy(true);
    try {
      await api.cancelFirmware(node.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const version = node.snip?.SoftwareVersion;
  return (
    <>
      <div className="cfg-segment__head" style={{ marginTop: "1.75rem" }}>
        <h2>Firmware</h2>
        <span className="cfg-segment__status">{version ? `running version ${version}` : "version not known"}</span>
      </div>
      <div className="panel fw">
        {active && job ? (
          <Progress job={job} onCancel={job.phase === "writing" || job.phase === "checking" || job.phase === "freezing" ? () => void cancel() : undefined} busy={busy} />
        ) : (
          <>
            {job && <Outcome job={job} now={now} />}
            {upgradeActive && !job && (
              <p className="note">The node says it is in the middle of an upgrade. Installing an image again will take over from where that left off.</p>
            )}
            {!node.online ? (
              <p className="muted">The node is offline. Firmware can be installed when it is back on the bus.</p>
            ) : (
              <div className="fw__pick">
                <label className="btn">
                  Choose a firmware file
                  <input
                    ref={input}
                    type="file"
                    accept=".bin,.uf2,application/octet-stream"
                    className="sr-only"
                    onChange={(e) => {
                      setFile(e.target.files?.[0]);
                      setConfirming(false);
                      setError(undefined);
                    }}
                  />
                </label>
                {file ? (
                  <span className="fw__file">
                    {file.name} <span className="muted">({kb(file.size)})</span>
                  </span>
                ) : (
                  <span className="muted">A .bin or .uf2 built for this board.</span>
                )}
                {file && !confirming && (
                  <button type="button" className="btn btn--primary" onClick={() => setConfirming(true)} disabled={busy}>
                    Install
                  </button>
                )}
              </div>
            )}
            {file && confirming && node.online && (
              <div className="confirm confirm--block" style={{ marginTop: "0.75rem" }}>
                <span>
                  Install {file.name} on {node.snip?.UserName || node.id}? The node restarts when the image is in, and nothing on it works until it
                  is back.
                </span>
                <button type="button" className="btn btn--primary" onClick={() => void install()} disabled={busy}>
                  {busy ? "Starting…" : "Install now"}
                </button>
                <button type="button" className="btn btn--quiet" onClick={() => setConfirming(false)} disabled={busy}>
                  Keep the current firmware
                </button>
              </div>
            )}
            {error && <p className="note note--error">{error}</p>}
            <p className="fw__hint muted">
              The image goes to the node 64 bytes at a time over the bus, which takes a minute or two for a few hundred kilobytes. A board that
              tries new firmware before keeping it is watched until it has settled.
            </p>
          </>
        )}
      </div>
    </>
  );
}

function Progress({ job, onCancel, busy }: { job: FirmwareJob; onCancel?: () => void; busy: boolean }) {
  const pct = job.size > 0 ? Math.round((job.written / job.size) * 100) : 0;
  const determinate = job.phase === "writing";
  return (
    <div className="fw__progress">
      <div className="fw__phase">
        <Lamp color="amber" label={PHASE_WORDS[job.phase]} pulse />
        <span>
          {PHASE_WORDS[job.phase]} {job.file}
          {determinate ? `: ${kb(job.written)} of ${kb(job.size)}` : ""}
        </span>
      </div>
      <div className="progress" role="progressbar" aria-valuenow={determinate ? pct : undefined} aria-valuemin={0} aria-valuemax={100}>
        <div className={determinate ? "progress__bar" : "progress__bar progress__bar--indeterminate"} style={determinate ? { width: `${pct}%` } : undefined} />
      </div>
      <p className="fw__message">{job.message}</p>
      {onCancel && (
        <button type="button" className="btn btn--quiet btn--sm" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      )}
    </div>
  );
}

function Outcome({ job, now }: { job: FirmwareJob; now: number }) {
  if (job.phase === "done") {
    return (
      <p className="fw__outcome">
        <Status color="green">Installed</Status> {job.message} <span className="muted">({job.file}, {relativeTime(job.finished, now)})</span>
      </p>
    );
  }
  return (
    <div className="note note--error fw__outcome">
      <Status color="red">Failed</Status> {job.message} <span className="muted">({job.file}, {relativeTime(job.finished, now)})</span>
    </div>
  );
}
