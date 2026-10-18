import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { api, type RosterIdentity } from "../api";
import { Status } from "../components/Lamp";
import { Page } from "../components/Page";
import { rosterSettingsLink } from "../components/decoder/DecoderPicker";
import { PaneNodes, type PaneContext } from "../components/decoder/PaneView";
import { SourceKey } from "../components/decoder/SourceLamp";
import { esuCVs, esuRowEdited, esuRowKnown } from "../decoder/esumap";
import type { DecoderDefinition, DecoderNode, DecoderPane, DecoderVariable, ESUMap } from "../decoder/types";
import { relativeTime, useNow } from "../format";
import {
  addressItems,
  addressOf,
  applyUpdates,
  cvsOf,
  discard,
  endSession,
  encode,
  initialTable,
  isEdited,
  isKnown,
  knownValues,
  merge,
  pendingChanges,
  qualified,
  setAddress,
  settle,
  variableMap,
  withChanges,
  withImported,
  withSaved,
  withSession,
  isImported,
  type CVChange,
  type CVTable,
  type CVUpdate,
  type EncodeResult,
  type Value,
} from "../decoder/values";
import { useBus } from "../store";

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

interface Job {
  kind: "read" | "write";
  done: number;
  total: number;
  failed: number;
}

/** The bits a whole CV holds: 16 for the TCS protocol, 8 for the rest. */
// A session ends after this long without work, as the server's
// SessionIdle says.
const SESSION_IDLE = 2 * 60 * 60 * 1000;
const noSession = { read: [], written: [] };

const fullBits = (cv: string) => (cv.startsWith("T2CV") ? 0xffff : 0xff);

/** The ESU function maps a pane shows. */
function esuMaps(nodes: DecoderNode[]): ESUMap[] {
  return nodes.flatMap((n) => [...(n.esu_map ? [n.esu_map] : []), ...esuMaps(n.children ?? [])]);
}

/** The CVs a pane reads and writes, in its order. */
function paneCVs(pane: DecoderPane, def: DecoderDefinition, vars: Map<string, DecoderVariable>): string[] {
  if (pane.all_cvs) return def.cvs.map((c) => c.name);
  const maps = esuMaps(pane.nodes).flatMap(esuCVs);
  const items = [...pane.items];
  // Writing a short address can make a decoder change these, so they go
  // after it.
  const short = vars.get("Short Address");
  if (short && items.includes(short.item)) {
    for (const cv of short.address_changes ?? []) {
      const v = def.variables.find((x) => x.parts?.some((p) => p.cv === cv));
      if (v && !items.includes(v.item)) items.push(v.item);
    }
  }
  const cvs = cvsOf(items, vars);
  return [...cvs, ...maps.filter((cv) => !cvs.includes(cv))];
}

/** The identity the Identify button read, carried in the address. */
function identityParam(params: URLSearchParams): RosterIdentity | undefined {
  const mfg = Number(params.get("mfg"));
  if (!params.get("mfg") || !Number.isInteger(mfg)) return undefined;
  const pid = params.get("pid");
  return { manufacturer: mfg, version: Number(params.get("ver") ?? 0), product_id: pid ? Number(pid) : undefined };
}

export function DecoderSettings() {
  const [params] = useSearchParams();
  const { trains, loaded } = useBus();
  // A roster locomotive's decoder, or a definition on its own.
  const loco = params.get("loco") ?? "";
  const decoderID = params.get("decoder") ?? "";
  const entry = loco ? trains[loco] : undefined;
  const rosterDecoder = entry?.decoders?.find((d) => d.id === decoderID);
  const file = loco ? (rosterDecoder?.file ?? "") : (params.get("file") ?? "");
  const model = loco ? (rosterDecoder?.model ?? "") : (params.get("model") ?? "");
  const now = useNow();
  const [savedAt, setSavedAt] = useState<string>();
  const [def, setDef] = useState<DecoderDefinition>();
  const [loadError, setLoadError] = useState<string>();
  const [table, setTableState] = useState<CVTable>({});
  const tableRef = useRef<CVTable>({});
  const [tab, setTab] = useState(0);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [failures, setFailures] = useState<Record<string, string>>({});
  const [job, setJob] = useState<Job>();
  const stopRef = useRef(false);
  // Write page waits for a yes when it would write values not read.
  const [confirmWrite, setConfirmWrite] = useState(false);
  const [train, setTrain] = useState(params.get("train") ?? "");
  const [message, setMessage] = useState<string>();
  // How many changes from an earlier visit came back, for a note.
  const [restored, setRestored] = useState(0);
  // A roster decoder's session: when it began and when it last saw work.
  const [session, setSession] = useState<{ started: string; last: number }>();
  const runningRef = useRef(false);
  // The changes the roster keeps, as last sent, and the save waiting.
  const sentRef = useRef("{}");
  const saveTimer = useRef<number | undefined>(undefined);
  const navigate = useNavigate();

  const setTable = (t: CVTable) => {
    tableRef.current = t;
    setTableState(t);
  };

  useEffect(() => {
    if (!file || !model) return;
    // Nothing of the last decoder's may be kept for this one.
    setTable({});
    sentRef.current = "{}";
    setDef(undefined);
    setLoadError(undefined);
    setSavedAt(undefined);
    setSession(undefined);
    const saved = loco && decoderID ? api.savedCVs(loco, decoderID) : Promise.resolve(undefined);
    Promise.all([api.decoderDefinition(file, model), saved])
      .then(([d, values]) => {
        const t = values
          ? withChanges(withSession(withImported(withSaved(initialTable(d), values.cvs), values.imported ?? {}), values.session ?? noSession), values.changes ?? {})
          : initialTable(d);
        setSession(values?.session ? { started: values.session.started, last: Date.parse(values.session.last) } : undefined);
        sentRef.current = JSON.stringify(pendingChanges(t));
        setDef(d);
        setTable(t);
        setRestored(Object.keys(values?.changes ?? {}).length);
        setSavedAt(values && Object.keys(values.cvs).length + Object.keys(values.imported ?? {}).length > 0 ? values.updated : undefined);
        setTab(0);
      })
      .catch((err) => setLoadError(describe(err)));
  }, [file, model, loco, decoderID]);

  // A roster decoder keeps changes not written yet, so they are there
  // when its page opens again, here or elsewhere.
  const flushChanges = useCallback(
    async (keepalive = false) => {
      window.clearTimeout(saveTimer.current);
      if (!loco || !decoderID) return;
      const changes = pendingChanges(tableRef.current);
      const json = JSON.stringify(changes);
      if (json === sentRef.current) return;
      sentRef.current = json;
      try {
        await api.saveChanges(loco, decoderID, changes, keepalive);
      } catch (err) {
        sentRef.current = "";
        setMessage(`Changes not written yet could not be kept: ${describe(err)}`);
      }
    },
    [loco, decoderID],
  );
  useEffect(() => {
    if (!def || !loco) return;
    window.clearTimeout(saveTimer.current);
    saveTimer.current = window.setTimeout(() => void flushChanges(), 600);
  }, [table, def, loco, flushChanges]);
  useEffect(() => {
    const onHide = () => void flushChanges(true);
    window.addEventListener("pagehide", onHide);
    return () => {
      window.removeEventListener("pagehide", onHide);
      void flushChanges(true);
    };
  }, [flushChanges]);
  // The session can end elsewhere (another page, an Identify on the
  // programming track), so coming back to the page checks it; and it ends
  // here after a spell without work.
  const syncSession = useCallback(async () => {
    if (!loco || !decoderID || runningRef.current) return;
    try {
      const s = (await api.savedCVs(loco, decoderID)).session;
      if (runningRef.current) return;
      setTable(withSession(endSession(tableRef.current), s ?? noSession));
      setSession(s ? { started: s.started, last: Date.parse(s.last) } : undefined);
    } catch {
      // The marks stay as they are until the next check.
    }
  }, [loco, decoderID]);
  useEffect(() => {
    const onShow = () => {
      if (document.visibilityState === "visible") void syncSession();
    };
    window.addEventListener("focus", onShow);
    document.addEventListener("visibilitychange", onShow);
    return () => {
      window.removeEventListener("focus", onShow);
      document.removeEventListener("visibilitychange", onShow);
    };
  }, [syncSession]);
  useEffect(() => {
    if (!session) return;
    const left = session.last + SESSION_IDLE - Date.now();
    const timer = window.setTimeout(() => void syncSession(), Math.max(left, 0) + 1000);
    return () => window.clearTimeout(timer);
  }, [session, syncSession]);

  // A decoder opened on its own keeps nothing, so leaving with changes
  // asks first.
  useEffect(() => {
    if (loco) return;
    const onLeave = (e: BeforeUnloadEvent) => {
      if (Object.values(tableRef.current).some((c) => c.state === "edited")) e.preventDefault();
    };
    window.addEventListener("beforeunload", onLeave);
    return () => window.removeEventListener("beforeunload", onLeave);
  }, [loco]);

  const vars = useMemo(() => (def ? variableMap(def) : new Map<string, DecoderVariable>()), [def]);
  const cvInfo = useMemo(() => new Map((def?.cvs ?? []).map((c) => [c.name, c])), [def]);

  const back = loco ? (
    <Link className="page__back" to={`/roster/${encodeURIComponent(loco)}`}>
      {entry?.name ?? "Roster"}
    </Link>
  ) : (
    <Link className="page__back" to="/programming">
      Programming
    </Link>
  );
  if (loco && !loaded) {
    return (
      <Page title="Decoder settings" back={back} lead="Loading…">
        {null}
      </Page>
    );
  }
  if (loco && !rosterDecoder) {
    return (
      <Page title="Decoder settings" back={back}>
        <p className="note">{entry ? `${entry.name} has no such decoder.` : "That locomotive is not in the roster."}</p>
      </Page>
    );
  }
  if (loco && (!file || !model)) {
    return (
      <Page title={entry!.name} back={back}>
        <p className="note">This decoder has no definition yet. Choose one for it on the locomotive's roster page.</p>
      </Page>
    );
  }
  if (!file || !model) {
    return (
      <Page title="Decoder settings" back={back}>
        <p className="note">Choose a decoder on the Programming page first.</p>
      </Page>
    );
  }
  if (loadError) {
    return (
      <Page title={model} back={back}>
        <p className="note note--error">{loadError}</p>
      </Page>
    );
  }
  if (!def) {
    return (
      <Page title={model} back={back} lead="Reading the decoder definition…">
        <div className="progress" aria-label="Loading">
          <div className="progress__bar progress__bar--indeterminate" />
        </div>
      </Page>
    );
  }

  const onMain = train !== "";
  const pane = def.panes[Math.min(tab, def.panes.length - 1)];
  const busy = job !== undefined;
  const programmable = def.programming.no_decoder !== true && !(def.programming.modes ?? []).some((m) => m.startsWith("LOCONET"));

  function edit(item: string, r: EncodeResult) {
    if ("error" in r) {
      setErrors((e) => ({ ...e, [item]: r.error }));
      return;
    }
    setErrors((e) => {
      const { [item]: _drop, ...rest } = e;
      return rest;
    });
    changeTable(applyUpdates(tableRef.current, r.updates, "edited"));
  }

  // Reads, writes and changes are work on the decoder: a session begins
  // with the first read or write and goes on while there is work.
  function worked(start: boolean) {
    if (!loco) return;
    setSession((s) => (s ? { ...s, last: Date.now() } : start ? { started: new Date().toISOString(), last: Date.now() } : s));
  }
  function changeTable(t: CVTable) {
    setTable(t);
    worked(false);
  }

  async function finishSession() {
    if (!loco || !decoderID) return;
    try {
      await api.endSession(loco, decoderID);
      setTable(endSession(tableRef.current));
      setSession(undefined);
    } catch (err) {
      setMessage(`The session could not be ended: ${describe(err)}`);
    }
  }

  const ctx: PaneContext = {
    table,
    vars,
    cvs: def.cvs,
    busy,
    onMain,
    errors,
    setValue: (v: DecoderVariable, value: Value) => edit(v.item, encode(v, value, tableRef.current, vars)),
    setAddress: (address: number) => edit("Address", setAddress(addressItems(vars), address, tableRef.current, vars)),
    setCV: (cv: string, value: number) => {
      const u: CVUpdate = { cv, value, bits: fullBits(cv) };
      changeTable(applyUpdates(tableRef.current, [u], "edited"));
    },
    setBits: (updates: CVUpdate[]) => changeTable(applyUpdates(tableRef.current, updates, "edited")),
    readCVs: onMain || !programmable ? undefined : (list: string[]) => void run("read", list.filter(readable), readCV),
    functionLabels: entry ? (entry.installed === decoderID ? entry.functions : rosterDecoder?.functions) : undefined,
  };

  async function cvCall(cv: string, write?: number) {
    return api.decoderCV({
      file,
      model,
      cv,
      write: write !== undefined,
      value: write,
      train: train || undefined,
      locomotive: loco || undefined,
      decoder: loco ? decoderID : undefined,
    });
  }

  /** After writes, the roster follows a new address on the fitted decoder. */
  async function followAddress() {
    if (!entry || entry.installed !== decoderID) return;
    const a = addressOf(addressItems(vars), tableRef.current, vars);
    if (!a || !isKnown(vars.get(a.long ? "Long Address" : "Short Address")!, tableRef.current)) return;
    const long = a.long || a.address > 127;
    if (a.address === entry.address && long === entry.long) return;
    try {
      await api.updateTrain(entry.id, { address: a.address, long });
      setMessage((m) => `${m ? `${m} ` : ""}${entry.name} now drives at address ${a.address}.`);
    } catch (err) {
      setMessage(`The decoder's address changed, but the roster could not follow: ${describe(err)}`);
    }
  }

  async function run(kind: Job["kind"], cvs: string[], step: (cv: string) => Promise<string | undefined>) {
    stopRef.current = false;
    runningRef.current = true;
    setConfirmWrite(false);
    setMessage(undefined);
    let failed = 0;
    setJob({ kind, done: 0, total: cvs.length, failed });
    const newFailures: Record<string, string> = {};
    try {
      // The roster has the latest changes before the decoder is touched,
      // so a value read or written that holds one can drop it.
      await flushChanges();
      for (let i = 0; i < cvs.length; i++) {
        if (stopRef.current) break;
        let problem: string | undefined;
        try {
          problem = await step(cvs[i]);
        } catch (err) {
          problem = describe(err);
        }
        if (problem) {
          failed++;
          newFailures[cvs[i]] = problem;
        }
        setJob({ kind, done: i + 1, total: cvs.length, failed });
      }
    } finally {
      runningRef.current = false;
      setJob(undefined);
      setFailures(newFailures);
      const verb = kind === "read" ? "read" : "written";
      if (stopRef.current) setMessage("Stopped.");
      else if (failed > 0) setMessage(`${failed} of ${cvs.length} CVs could not be ${verb}.`);
      else if (cvs.length === 0) setMessage(kind === "read" ? "This page has nothing to read." : "Nothing on this page needs writing.");
      else setMessage(`${cvs.length} CV${cvs.length === 1 ? "" : "s"} ${verb}${loco ? " and saved" : ""}.`);
      if (loco && failed < cvs.length) setSavedAt(new Date().toISOString());
    }
    if (kind === "write") await followAddress();
  }

  async function readCV(cv: string): Promise<string | undefined> {
    const r = await cvCall(cv);
    if (!r.ok) return r.error ?? "Failed";
    setTable(settle(tableRef.current, cv, r.value, "read"));
    worked(true);
    return undefined;
  }

  async function writeCV(cv: string): Promise<string | undefined> {
    const cell = tableRef.current[cv];
    if (!cell) return undefined;
    let value = cell.value;
    const info = cvInfo.get(cv);
    // A CV edited before it was ever read keeps the decoder's other bits.
    if (!cell.known && cell.state === "edited" && cell.edited !== fullBits(cv) && !info?.write_only && !onMain) {
      const r = await cvCall(cv);
      if (!r.ok) return `Could not read it first: ${r.error ?? "failed"}`;
      value = merge(cell, r.value);
    }
    const w = await cvCall(cv, value);
    if (!w.ok) return w.error ?? "Failed";
    setTable(settle(tableRef.current, cv, value, "stored"));
    worked(true);
    return undefined;
  }

  const readable = (cv: string) => {
    const c = cvInfo.get(cv);
    return !c?.info_only && !c?.write_only;
  };
  const writable = (cv: string) => {
    const c = cvInfo.get(cv);
    return !c?.info_only && !c?.read_only;
  };
  const cvs = pane ? paneCVs(pane, def, vars) : [];
  const toRead = cvs.filter(readable);
  const changed = cvs.filter((cv) => writable(cv) && table[cv]?.state === "edited");
  const toWrite = cvs.filter((cv) => writable(cv) && table[cv] && table[cv].state !== "unknown");
  // Values Write page would put on the decoder without its having held
  // them: imported ones not read since, and the definition's defaults.
  const unreadImported = toWrite.filter((cv) => table[cv].state === "imported").length;
  const unreadDefaults = toWrite.filter((cv) => table[cv].state === "fromfile").length;
  const unreadList = [
    unreadImported > 0 ? `${unreadImported} imported value${unreadImported === 1 ? "" : "s"}` : "",
    unreadDefaults > 0 ? `${unreadDefaults} default${unreadDefaults === 1 ? "" : "s"}` : "",
  ]
    .filter(Boolean)
    .join(" and ");
  const allChanged = def.cvs.filter((c) => !c.info_only && !c.read_only && table[c.name]?.state === "edited").map((c) => c.name);
  const pageEdited = cvs.filter((cv) => table[cv]?.state === "edited");
  const anyEdited = Object.values(table).some((c) => c.state === "edited");
  // An ESU function map's rows count as settings.
  const mapRows = pane ? esuMaps(pane.nodes).flatMap((m) => Array.from({ length: m.rows }, (_, r) => ({ m, r }))) : [];
  const unread =
    (pane ? pane.items.map((i) => vars.get(i)).filter((v): v is DecoderVariable => !!v && v.type !== "constant" && !isKnown(v, table)).length : 0) +
    mapRows.filter(({ m, r }) => !esuRowKnown(m, table, r)).length;
  const changedSettings =
    (pane ? pane.items.filter((i) => { const v = vars.get(i); return v && isEdited(v, table); }).length : 0) +
    mapRows.filter(({ m, r }) => esuRowEdited(m, table, r)).length;
  const roster = Object.values(trains)
    .filter((t) => t.roster)
    .sort((a, b) => a.name.localeCompare(b.name));

  const decoderTitle = rosterDecoder?.name || def.model;
  const imported = Object.values(table).filter(isImported).length;
  const importedOnly = imported > 0 && !Object.values(table).some((c) => c.known);
  const saved = !savedAt
    ? "No values saved yet; read a page to start."
    : importedOnly
      ? `Imported values from ${relativeTime(savedAt, now)}, not read from the decoder yet; reading a page replaces them.`
      : `Saved values from ${relativeTime(savedAt, now)}${
          imported > 0 ? `, with ${imported.toLocaleString()} imported CV${imported === 1 ? "" : "s"} not read from the decoder yet` : ""
        }; reading a page replaces them.`;
  const lead = entry
    ? `${decoderTitle === def.model ? def.model : `${decoderTitle}: ${def.model}`}, ${def.family}. ${saved}`
    : `${def.family}, ${def.manufacturer}. Settings come from the JMRI project's decoder definitions.`;

  return (
    <Page title={entry ? entry.name : def.model} back={back} lead={lead}>
      {!programmable && <p className="note">This device is not programmed through DCC, so Interlock can show its settings but not read or write them.</p>}
      {entry && entry.installed !== decoderID && (
        <p className="note">This decoder is not the one fitted in {entry.name}. Programming on the main reaches whichever decoder answers at {entry.address}.</p>
      )}
      <div className="panel dec-target">
        <label className="dec-target__pick">
          <span>Program</span>
          <select className="input input--select" value={train} disabled={busy} onChange={(e) => setTrain(e.target.value)}>
            <option value="">on the programming track</option>
            {entry ? (
              <option value={entry.id}>on the main, at address {entry.address}</option>
            ) : (
              roster.map((t) => (
                <option key={t.id} value={t.id}>
                  on the main: {t.name || `#${t.address}`}
                </option>
              ))
            )}
          </select>
        </label>
        {onMain && <span className="dim">Writes only: reading on the main needs RailCom.</span>}
        {session && (
          <span className="dec-session">
            <Status color="green">{`Session started ${relativeTime(session.started, now)}`}</Status>
            <button className="btn btn--quiet" disabled={busy} onClick={() => void finishSession()}>
              End session
            </button>
          </span>
        )}
      </div>

      {restored > 0 && anyEdited && (
        <p className="note">
          {restored === 1 ? "A change" : `${restored} changes`} made earlier {restored === 1 ? "is" : "are"} not written to the decoder yet. Pages with
          changes are marked; write them or discard them there.
        </p>
      )}

      <div className="cfg-tabs" role="tablist" aria-label="Pages">
        {def.panes.map((p, i) => {
          const enabled = qualified(p.qualifiers, table, vars);
          const dirty = !p.all_cvs && anyEdited && paneCVs(p, def, vars).some((cv) => table[cv]?.state === "edited");
          return (
            <button
              key={i}
              role="tab"
              type="button"
              className={dirty ? "cfg-tab cfg-tab--dirty" : "cfg-tab"}
              aria-selected={i === tab}
              aria-description={dirty ? "Has changes not written yet" : undefined}
              disabled={!enabled || busy}
              title={p.tooltip}
              onClick={() => {
                setTab(i);
                setConfirmWrite(false);
                setMessage(undefined);
                setFailures({});
              }}
            >
              {p.name}
            </button>
          );
        })}
      </div>

      {pane && (
        <div role="tabpanel" className="panel cfg-panel dec-panel">
          <div className="dec-toolbar">
            <button className="btn" disabled={busy || onMain || !programmable || toRead.length === 0} onClick={() => void run("read", toRead, readCV)}>
              Read page
            </button>
            <button className="btn btn--primary" disabled={busy || !programmable || changed.length === 0} onClick={() => void run("write", changed, writeCV)}>
              Write changes{changed.length > 0 ? ` (${changed.length})` : ""}
            </button>
            <button
              className="btn btn--quiet"
              disabled={busy || !programmable || toWrite.length === 0}
              onClick={() => (unreadList ? setConfirmWrite(true) : void run("write", toWrite, writeCV))}
            >
              Write page
            </button>
            {pageEdited.length > 0 && (
              <button className="btn btn--quiet" disabled={busy} onClick={() => setTable(discard(tableRef.current, pageEdited))}>
                Discard changes
              </button>
            )}
            {allChanged.length > changed.length && !busy && (
              <button className="btn btn--quiet" disabled={!programmable} onClick={() => void run("write", allChanged, writeCV)}>
                Write changes on every page ({allChanged.length})
              </button>
            )}
            <span className="dec-toolbar__status">
              {job ? (
                <>
                  <span className="progress dec-progress" aria-label={`${job.done} of ${job.total}`}>
                    <span className="progress__bar" style={{ width: `${job.total ? (job.done / job.total) * 100 : 0}%` }} />
                  </span>
                  <span className="num">
                    {job.kind === "read" ? "Reading" : "Writing"} {job.done} of {job.total}
                  </span>
                  <button className="btn btn--quiet" onClick={() => (stopRef.current = true)}>
                    Stop
                  </button>
                </>
              ) : (
                <>
                  {message && <span>{message}</span>}
                  {!message && unread > 0 && <Status color="amber">{`${unread} not read yet`}</Status>}
                  {!message && unread === 0 && changedSettings === 0 && (pane.items.length > 0 || mapRows.length > 0) && <Status color="green">Up to date</Status>}
                </>
              )}
            </span>
          </div>
          {confirmWrite && unreadList && !busy && (
            <div className="confirm confirm--block dec-confirm">
              <span>
                {unreadList} on this page {unreadImported + unreadDefaults === 1 ? "was" : "were"} not read from the decoder. Writing the page
                replaces what the decoder holds with {unreadImported + unreadDefaults === 1 ? "it" : "them"}.
              </span>
              <button className="btn btn--danger" onClick={() => void run("write", toWrite, writeCV)}>
                Write page
              </button>
              <button className="btn btn--quiet" onClick={() => setConfirmWrite(false)}>
                Cancel
              </button>
            </div>
          )}
          {Object.keys(failures).length > 0 && !busy && (
            <details className="note note--error">
              <summary>Which CVs failed</summary>
              <ul>
                {Object.entries(failures).map(([cv, why]) => (
                  <li key={cv}>
                    CV {cv}: {why}
                  </li>
                ))}
              </ul>
            </details>
          )}
          <SourceKey />
          <PaneNodes nodes={pane.nodes} ctx={ctx} />
        </div>
      )}

      {!loco && (
        <AddToRoster
          def={def}
          identity={identityParam(params)}
          address={addressOf(addressItems(vars), table, vars)}
          values={() => knownValues(tableRef.current)}
          changes={() => pendingChanges(tableRef.current)}
          onAdded={(t, d) => navigate(rosterSettingsLink(t, d), { replace: true })}
        />
      )}

      {((def.problems?.length ?? 0) > 0 || (def.missing?.length ?? 0) > 0) && (
        <details className="note dec-problems">
          <summary>Parts of this definition Interlock could not use</summary>
          <ul>
            {def.problems?.map((p, i) => <li key={`p${i}`}>{p}</li>)}
            {def.missing?.map((m) => <li key={m}>Missing file {m}</li>)}
          </ul>
        </details>
      )}
    </Page>
  );
}

/** Keeps a decoder opened on its own in the roster, with the values read so far. */
function AddToRoster({
  def,
  identity,
  address,
  values,
  changes,
  onAdded,
}: {
  def: DecoderDefinition;
  identity?: RosterIdentity;
  address?: { address: number; long: boolean };
  values: () => Record<string, number>;
  changes: () => Record<string, CVChange>;
  onAdded: (train: string, decoder: string) => void;
}) {
  const { trains } = useBus();
  const roster = Object.values(trains)
    .filter((t) => t.roster)
    .sort((a, b) => a.name.localeCompare(b.name));
  const [into, setInto] = useState("");
  const [name, setName] = useState("");
  const [addr, setAddr] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const known = address && address.address > 0 ? address.address : undefined;
  const n = Number(addr || known || 0);

  async function add(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      let id = into;
      if (!id) {
        const t = await api.createTrain({ name, address: n, long: n > 127 || (address?.long ?? false) });
        id = t.id;
      }
      const r = await api.addDecoder(id, { file: def.file, family: def.family, model: def.model, identity, install: !into || !trains[into]?.installed });
      const cvs = values();
      if (Object.keys(cvs).length > 0) await api.saveCVs(id, r.decoder.id, cvs, true);
      const kept = changes();
      if (Object.keys(kept).length > 0) await api.saveChanges(id, r.decoder.id, kept);
      onAdded(id, r.decoder.id);
    } catch (err) {
      setError(describe(err));
      setBusy(false);
    }
  }

  return (
    <form className="panel dec-keep" onSubmit={(e) => void add(e)}>
      <h2>Keep in the roster</h2>
      <p className="dim">
        Save this decoder with a locomotive, with the values read so far and any changes not written yet, so its settings open without reading
        them again.
      </p>
      <div className="dec-keep__row">
        <label className="auto-field">
          <span>Locomotive</span>
          <select className="input input--select" value={into} onChange={(e) => setInto(e.target.value)}>
            <option value="">A new locomotive</option>
            {roster.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name}
              </option>
            ))}
          </select>
        </label>
        {!into && (
          <>
            <label className="auto-field">
              <span>Name</span>
              <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder={def.model} />
            </label>
            <label className="auto-field">
              <span>DCC address</span>
              <input
                className="input"
                type="number"
                min={1}
                max={10239}
                value={addr}
                placeholder={known ? String(known) : ""}
                onChange={(e) => setAddr(e.target.value)}
                required={!known}
              />
            </label>
          </>
        )}
        <button className="btn btn--primary" type="submit" disabled={busy || (!into && !(n > 0))}>
          {into ? "Add to the locomotive" : "Add to the roster"}
        </button>
      </div>
      {error && <p className="field__error">{error}</p>}
    </form>
  );
}
