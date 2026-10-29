import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { api, ApiError, type DecoderCheck, type DecoderMatch, type DecoderProfile, type DecoderSession, type RosterIdentity } from "../api";
import { Status } from "../components/Lamp";
import { Page } from "../components/Page";
import { rosterSettingsLink } from "../components/decoder/DecoderPicker";
import { PaneNodes, type PaneContext } from "../components/decoder/PaneView";
import { SourceKey } from "../components/decoder/SourceLamp";
import { esuCVs, esuRowCVs, esuRowEdited, esuRowKnown } from "../decoder/esumap";
import type { DecoderDefinition, DecoderNode, DecoderPane, DecoderVariable, ESUMap } from "../decoder/types";
import { relativeTime, useNow } from "../format";
import {
  addressItems,
  addressOf,
  allConfirmed,
  applyUpdates,
  confirmed,
  cvsOf,
  discard,
  endSession,
  encode,
  initialTable,
  isEdited,
  isKnown,
  knownValues,
  loadProfile,
  merge,
  pagesToRead,
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
  kind: "read" | "write" | "check";
  done: number;
  total: number;
  failed: number;
}

/** A roster decoder's session: when it began, when it last saw work, and its check. */
interface SessionState {
  started: string;
  last: number;
  check?: DecoderSession["check"];
}

function sessionState(s: DecoderSession | undefined): SessionState | undefined {
  return s ? { started: s.started, last: Date.parse(s.last), check: s.check } : undefined;
}

/** A check that found something else on the programming track, and the work waiting on it. */
interface Mismatch {
  check: DecoderCheck;
  kind: "read" | "write";
  cvs: string[];
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
  const tabsRef = useRef<HTMLDivElement>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [failures, setFailures] = useState<Record<string, string>>({});
  const [job, setJob] = useState<Job>();
  const stopRef = useRef(false);
  // On the main, CVs 17 and 18 written in one run go together as one long
  // address; the one reached second is then skipped.
  const pairRef = useRef({ both: false, done: false });
  // Write page waits for a yes when it would write values not read.
  const [confirmWrite, setConfirmWrite] = useState(false);
  const [train, setTrain] = useState(params.get("train") ?? "");
  const [message, setMessage] = useState<string>();
  // How many changes from an earlier visit came back, for a note.
  const [restored, setRestored] = useState(0);
  // A roster decoder's session, kept in a ref too so work started right
  // after it changes sees it.
  const [session, setSessionState] = useState<SessionState>();
  const sessionRef = useRef<SessionState>(undefined);
  const setSession = useCallback((s: SessionState | undefined) => {
    sessionRef.current = s;
    setSessionState(s);
  }, []);
  const [mismatch, setMismatch] = useState<Mismatch>();
  const [profiles, setProfiles] = useState<DecoderProfile[]>([]);
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
    setMismatch(undefined);
    const saved = loco && decoderID ? api.savedCVs(loco, decoderID) : Promise.resolve(undefined);
    Promise.all([api.decoderDefinition(file, model), saved])
      .then(([d, values]) => {
        const t = values
          ? withChanges(withSession(withImported(withSaved(initialTable(d), values.cvs), values.imported ?? {}), values.session ?? noSession), values.changes ?? {})
          : initialTable(d);
        setSession(sessionState(values?.session));
        setProfiles(values?.profiles ?? []);
        sentRef.current = JSON.stringify(pendingChanges(t));
        setDef(d);
        setTable(t);
        setRestored(Object.keys(values?.changes ?? {}).length);
        setSavedAt(values && Object.keys(values.cvs).length + Object.keys(values.imported ?? {}).length > 0 ? values.updated : undefined);
        setTab(0);
      })
      .catch((err) => setLoadError(describe(err)));
  }, [file, model, loco, decoderID, setSession]);

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
      setSession(sessionState(s));
    } catch {
      // The marks stay as they are until the next check.
    }
  }, [loco, decoderID, setSession]);
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
  // with a check on the programming track or the first write on the main,
  // and goes on while there is work.
  function worked(start: boolean) {
    if (!loco) return;
    const s = sessionRef.current;
    setSession(s ? { ...s, last: Date.now() } : start ? { started: new Date().toISOString(), last: Date.now() } : s);
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
    readCVs: !programmable ? undefined : (list: string[]) => void run("read", list.filter(readable), readCV),
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

  // Before the first read or write of a session on the programming track,
  // Interlock checks that the decoder there is this one, since a fleet
  // often shares a decoder model.
  async function checkFirst(kind: Mismatch["kind"], cvs: string[]): Promise<boolean> {
    setConfirmWrite(false);
    setMessage(undefined);
    setMismatch(undefined);
    runningRef.current = true;
    setJob({ kind: "check", done: 0, total: 0, failed: 0 });
    try {
      await flushChanges();
      const c = await api.checkDecoder(loco, decoderID);
      if (!c.match) {
        setMismatch({ check: c, kind, cvs });
        return false;
      }
      // What the check read is read in the session.
      let t = tableRef.current;
      for (const [cv, value] of Object.entries(c.read ?? {})) if (t[cv]) t = settle(t, cv, value, "read");
      setTable(t);
      setSession(sessionState(c.session));
      return true;
    } catch (err) {
      setMessage(`The decoder on the programming track could not be checked: ${describe(err)}`);
      return false;
    } finally {
      runningRef.current = false;
      setJob(undefined);
    }
  }

  async function continueAnyway() {
    const m = mismatch;
    if (!m) return;
    setMismatch(undefined);
    try {
      setSession(sessionState(await api.startSession(loco, decoderID)));
    } catch (err) {
      setMessage(`The session could not begin: ${describe(err)}`);
      return;
    }
    await run(m.kind, m.cvs, m.kind === "read" ? readCV : writeCV);
  }

  async function run(kind: Mismatch["kind"], cvs: string[], step: (cv: string) => Promise<string | undefined>) {
    if (loco && !onMain && cvs.length > 0 && !sessionRef.current?.check && !(await checkFirst(kind, cvs))) return;
    stopRef.current = false;
    runningRef.current = true;
    setConfirmWrite(false);
    setMessage(undefined);
    // The session can end elsewhere while this runs; what is left waits
    // for a new check.
    let unchecked: string[] | undefined;
    let failed = 0;
    setJob({ kind, done: 0, total: cvs.length, failed });
    pairRef.current = { both: kind === "write" && onMain && cvs.includes("17") && cvs.includes("18"), done: false };
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
          if (err instanceof ApiError && err.status === 409) {
            unchecked = cvs.slice(i);
            break;
          }
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
    if (unchecked) {
      setTable(endSession(tableRef.current));
      setSession(undefined);
      return run(kind, unchecked, step);
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
    if ((cv === "17" || cv === "18") && pairRef.current.both) {
      if (pairRef.current.done) return undefined;
      pairRef.current.done = true;
      return writeLongAddress();
    }
    let value = cell.value;
    const info = cvInfo.get(cv);
    // A change to part of a CV keeps the decoder's other bits: unless this
    // session read or wrote the CV, it is read first. A command station
    // that cannot read on the main writes it as the page has it.
    if (!confirmed(cell) && cell.state === "edited" && cell.edited !== fullBits(cv) && !info?.write_only) {
      const r = await cvCall(cv);
      if (r.ok) value = merge(cell, r.value);
      else if (!(onMain && r.cannot_read)) return `Could not read it first: ${r.error ?? "failed"}`;
    }
    const w = await cvCall(cv, value);
    if (!w.ok) return w.error ?? "Failed";
    setTable(settle(tableRef.current, cv, value, "stored"));
    worked(true);
    return undefined;
  }

  // CVs 17 and 18 on the main, together: one Long Address instruction,
  // which sets CV 29 bit 5 too and moves the decoder to the new address at
  // once, so the roster follows it before anything else is written.
  async function writeLongAddress(): Promise<string | undefined> {
    const v17 = tableRef.current["17"].value;
    const v18 = tableRef.current["18"].value;
    const r = await api.decoderCV({
      file,
      model,
      cv: "17",
      write: true,
      value: v17,
      next: v18,
      train: train || undefined,
      locomotive: loco || undefined,
      decoder: loco ? decoderID : undefined,
    });
    if (!r.ok) return r.error ?? "Failed";
    let t = settle(settle(tableRef.current, "17", v17, "stored"), "18", v18, "stored");
    const c29 = t["29"];
    if (c29) {
      // Other changes to CV 29 are written after, at the new address.
      if (c29.state !== "edited") t = settle(t, "29", c29.value | 0x20, "stored");
      else if ((c29.edited & ~0x20) === 0 && c29.value & 0x20) t = settle(t, "29", c29.value, "stored");
    }
    setTable(t);
    worked(true);
    await followAddress();
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
  // The pages that can be opened now to read these CVs.
  const pagesFor = (list: string[]) =>
    pagesToRead(
      list.filter(readable),
      def.panes.map((p) => ({ cvs: qualified(p.qualifiers, table, vars) ? paneCVs(p, def, vars) : [], table: p.all_cvs })),
    ).map((i) => ({ index: i, name: def.panes[i].name }));
  function openTab(i: number) {
    setTab(i);
    setConfirmWrite(false);
    setMismatch(undefined);
    setMessage(undefined);
    setFailures({});
  }
  const cvs = pane ? paneCVs(pane, def, vars) : [];
  const toRead = cvs.filter(readable);
  const changed = cvs.filter((cv) => writable(cv) && table[cv]?.state === "edited");
  const toWrite = cvs.filter((cv) => writable(cv) && table[cv] && table[cv].state !== "unknown");
  // Values Write page would put on the decoder that this session has not
  // seen it hold: saved before the session, imported and not read since,
  // and the definition's defaults.
  const unreadSaved = toWrite.filter((cv) => table[cv].state === "saved").length;
  const unreadImported = toWrite.filter((cv) => table[cv].state === "imported").length;
  const unreadDefaults = toWrite.filter((cv) => table[cv].state === "fromfile").length;
  const unreadCount = unreadSaved + unreadImported + unreadDefaults;
  const unreadParts = [
    unreadSaved > 0 ? `${unreadSaved} saved value${unreadSaved === 1 ? "" : "s"}` : "",
    unreadImported > 0 ? `${unreadImported} imported value${unreadImported === 1 ? "" : "s"}` : "",
    unreadDefaults > 0 ? `${unreadDefaults} default${unreadDefaults === 1 ? "" : "s"}` : "",
  ].filter(Boolean);
  const unreadList = unreadParts.length > 1 ? `${unreadParts.slice(0, -1).join(", ")} and ${unreadParts.at(-1)}` : (unreadParts[0] ?? "");
  const allChanged = def.cvs.filter((c) => !c.info_only && !c.read_only && table[c.name]?.state === "edited").map((c) => c.name);
  const pageEdited = cvs.filter((cv) => table[cv]?.state === "edited");
  const anyEdited = Object.values(table).some((c) => c.state === "edited");
  // An ESU function map's rows count as settings.
  const mapRows = pane ? esuMaps(pane.nodes).flatMap((m) => Array.from({ length: m.rows }, (_, r) => ({ m, r }))) : [];
  const settings = pane ? pane.items.map((i) => vars.get(i)).filter((v): v is DecoderVariable => !!v && v.type !== "constant") : [];
  const unread = settings.filter((v) => !isKnown(v, table)).length + mapRows.filter(({ m, r }) => !esuRowKnown(m, table, r)).length;
  // Settings saved before this session, which the decoder may have
  // changed since: the tab is up to date only once they are read again.
  const checked = (list: string[]) => allConfirmed(list, table, readable);
  const unchecked =
    settings.filter((v) => isKnown(v, table) && !checked((v.parts ?? []).map((p) => p.cv))).length +
    mapRows.filter(({ m, r }) => esuRowKnown(m, table, r) && !checked(esuRowCVs(m, r))).length;
  const changedSettings =
    (pane
      ? pane.items.filter((i) => {
          const v = vars.get(i);
          return v && isEdited(v, table);
        }).length
      : 0) + mapRows.filter(({ m, r }) => esuRowEdited(m, table, r)).length;
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
        <p className="note">
          This decoder is not the one fitted in {entry.name}. Programming on the main reaches whichever decoder answers at {entry.address}.
        </p>
      )}
      <div className="panel dec-target">
        <label className="dec-target__pick">
          <span>Program</span>
          <select
            className="input input--select"
            value={train}
            disabled={busy}
            onChange={(e) => {
              setTrain(e.target.value);
              setMismatch(undefined);
            }}
          >
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
        {session && (
          <span className="dec-session">
            {session.check === "accepted" ? (
              <Status color="amber">{`Session started ${relativeTime(session.started, now)}, though the decoder did not match`}</Status>
            ) : (
              <Status color="green">{`Session started ${relativeTime(session.started, now)}`}</Status>
            )}
            <button className="btn btn--quiet" disabled={busy} onClick={() => void finishSession()}>
              End session
            </button>
          </span>
        )}
      </div>

      {restored > 0 && anyEdited && (
        <p className="note">
          {restored === 1 ? "A change" : `${restored} changes`} made earlier {restored === 1 ? "is" : "are"} not written to the decoder yet. Pages with changes are marked; write
          them or discard them there.
        </p>
      )}

      <div className="cfg-tabs" role="tablist" aria-label="Pages" ref={tabsRef}>
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
              onClick={() => openTab(i)}
            >
              {p.name}
            </button>
          );
        })}
      </div>

      {pane && (
        <div role="tabpanel" className="panel cfg-panel dec-panel">
          <div className="dec-toolbar">
            <button className="btn" disabled={busy || !programmable || toRead.length === 0} onClick={() => void run("read", toRead, readCV)}>
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
              {job?.kind === "check" ? (
                <>
                  <span className="progress dec-progress" aria-label="Checking">
                    <span className="progress__bar progress__bar--indeterminate" />
                  </span>
                  <span>Checking which decoder is on the programming track…</span>
                </>
              ) : job ? (
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
                  {!message && unread === 0 && unchecked > 0 && <Status color="off">{`${unchecked} saved before this session`}</Status>}
                  {!message && unread === 0 && unchecked === 0 && changedSettings === 0 && (pane.items.length > 0 || mapRows.length > 0) && (
                    <Status color="green">Up to date</Status>
                  )}
                </>
              )}
            </span>
          </div>
          {mismatch && !busy && entry && (
            <CheckMismatch
              mismatch={mismatch}
              loco={entry.name}
              decoder={`${entry.name}'s ${decoderTitle}`}
              model={def.model}
              onContinue={() => void continueAnyway()}
              onCancel={() => setMismatch(undefined)}
            />
          )}
          {confirmWrite && unreadList && !busy && (
            <div className="confirm confirm--block dec-confirm">
              <span>
                {unreadList} on this page {unreadCount === 1 ? "was" : "were"} not read from the decoder in this session. Writing the page replaces what the decoder holds with{" "}
                {unreadCount === 1 ? "it" : "them"}.
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

      {loco && (
        <DecoderProfiles
          loco={loco}
          decoder={decoderID}
          profiles={profiles}
          busy={busy}
          edited={anyEdited}
          onChange={setProfiles}
          onLoad={(st, apply, all) => {
            const r = loadProfile(tableRef.current, st.cvs, writable, fullBits, all);
            if (apply) changeTable(r.table);
            return r;
          }}
          pagesFor={pagesFor}
          onPage={(i) => {
            openTab(i);
            tabsRef.current?.scrollIntoView({ block: "start", behavior: "smooth" });
          }}
        />
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
            {def.problems?.map((p, i) => (
              <li key={`p${i}`}>{p}</li>
            ))}
            {def.missing?.map((m) => (
              <li key={m}>Missing file {m}</li>
            ))}
          </ul>
        </details>
      )}
    </Page>
  );
}

/** What a check found on the programming track instead of this decoder, and the choice to go on. */
function CheckMismatch({
  mismatch,
  loco,
  decoder,
  model,
  onContinue,
  onCancel,
}: {
  mismatch: Mismatch;
  loco: string;
  /** The roster decoder, as "GP40's LokSound 5". */
  decoder: string;
  model: string;
  onContinue: () => void;
  onCancel: () => void;
}) {
  const { trains } = useBus();
  const { check: c, kind } = mismatch;
  const address = (n: number, long?: boolean) => (long && n < 128 ? `long address ${n}` : `address ${n}`);
  const lines = c.differences.map((d) => {
    switch (d.what) {
      case "manufacturer":
        return `It is made by ${d.track_name ?? `manufacturer ${d.track}`}, not ${d.roster_name ?? `manufacturer ${d.roster}`}.`;
      case "product":
        return `It is ${c.model ? `a ${c.model}` : "another model"}, not a ${model}.`;
      case "address":
        return `It answers at ${address(d.track, d.track_long)}, not ${address(d.roster, d.roster_long)}.`;
      case "cv":
        return d.imported ? `CV ${d.cv} is ${d.track}; the imported value is ${d.roster}.` : `CV ${d.cv} is ${d.track}; the roster has ${d.roster}.`;
    }
  });
  const name = (m: DecoderMatch) => {
    const t = trains[m.train];
    const d = t?.decoders?.find((x) => x.id === m.decoder);
    if (!t) return m.train;
    return d ? `${t.name}'s ${d.name || d.model || "decoder"}` : t.name;
  };
  const link = (m: DecoderMatch) => (m.decoder ? rosterSettingsLink(m.train, m.decoder) : `/roster/${encodeURIComponent(m.train)}`);
  const list = (ms: DecoderMatch[]) =>
    ms.map((m, i) => (
      <span key={`${m.train}/${m.decoder ?? ""}`}>
        {i > 0 && (i === ms.length - 1 ? " or " : ", ")}
        <Link to={link(m)}>{name(m)}</Link>
      </span>
    ));
  // A new decoder answers at address 3 until it is set.
  const fresh = c.differences.length === 1 && c.differences[0].what === "address" && c.differences[0].track === 3 && !c.differences[0].track_long;
  return (
    <div className="confirm confirm--block dec-confirm dec-check" role="alert">
      {lines.length > 0 ? (
        <>
          <span>The decoder on the programming track does not match {decoder}.</span>
          <ul>
            {lines.map((l) => (
              <li key={l}>{l}</li>
            ))}
          </ul>
        </>
      ) : (
        <span>
          The decoder on the programming track could be {decoder}, but nothing saved tells it from {list(c.alike)}, with the same model at the same address.
        </span>
      )}
      {c.others.length > 0 && <span>It could be {list(c.others)}.</span>}
      {fresh && <span className="dim">New decoders answer at address 3. If this one is new, go on and set its address.</span>}
      <span>{kind === "read" ? `Reading anyway saves what it reads for ${loco}.` : `Writing anyway changes the decoder on the track and saves the values for ${loco}.`}</span>
      <span className="dec-check__actions">
        <button className="btn btn--danger" onClick={onContinue}>
          {kind === "read" ? "Read anyway" : "Write anyway"}
        </button>
        <button className="btn btn--quiet" onClick={onCancel}>
          Cancel
        </button>
      </span>
    </div>
  );
}

/**
 * Copies of a roster decoder's values kept under a name, such as how it
 * came or how it ran best. Loading one marks what differs as changes, so
 * nothing reaches the decoder until they are written.
 */
function DecoderProfiles({
  loco,
  decoder,
  profiles,
  busy,
  edited,
  onChange,
  onLoad,
  pagesFor,
  onPage,
}: {
  loco: string;
  decoder: string;
  profiles: DecoderProfile[];
  busy: boolean;
  /** Whether there are changes not written yet, which a profile leaves out. */
  edited: boolean;
  onChange: (profiles: DecoderProfile[]) => void;
  /** What loading a profile would change; with apply, it does. all marks too what matches but was not read in this session. */
  onLoad: (profile: DecoderProfile, apply: boolean, all: boolean) => { changed: number; replaced: number; unread: string[] };
  /** The pages to read to see these CVs. */
  pagesFor: (cvs: string[]) => { index: number; name: string }[];
  /** Opens a page. */
  onPage: (index: number) => void;
}) {
  const now = useNow();
  const [name, setName] = useState("");
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<string>();
  const [note, setNote] = useState<string>();
  // The profile loaded last, while values that match it may still hold
  // something else on the decoder.
  const [loaded, setLoaded] = useState<string>();
  // What waits for a yes: replacing a profile by the same name, deleting
  // one, or loading one over changes not written yet.
  const [ask, setAskState] = useState<{ kind: "replace" | "delete" | "load"; profile: string; replaced?: number; all?: boolean }>();
  const setAsk = (a: typeof ask) => {
    setAskState(a);
    if (a) {
      setNote(undefined);
      setError(undefined);
      setLoaded(undefined);
    }
  };

  async function refresh() {
    onChange((await api.savedCVs(loco, decoder)).profiles ?? []);
  }

  async function keep(replace: boolean) {
    const n = name.trim();
    if (!replace && profiles.some((st) => st.name === n)) {
      setAsk({ kind: "replace", profile: n });
      return;
    }
    setWorking(true);
    setError(undefined);
    setNote(undefined);
    setAsk(undefined);
    try {
      const st = await api.keepProfile(loco, decoder, n, replace);
      setName("");
      setNote(`Kept ${Object.keys(st.cvs).length.toLocaleString()} values as ${st.name}.`);
      await refresh();
    } catch (err) {
      setError(describe(err));
    } finally {
      setWorking(false);
    }
  }

  async function remove(n: string) {
    setWorking(true);
    setError(undefined);
    setNote(undefined);
    setAsk(undefined);
    try {
      await api.deleteProfile(loco, decoder, n);
      await refresh();
    } catch (err) {
      setError(describe(err));
    } finally {
      setWorking(false);
    }
  }

  function load(st: DecoderProfile, force: boolean, all = false) {
    setError(undefined);
    setAsk(undefined);
    const r = onLoad(st, false, all);
    if (r.replaced > 0 && !force) {
      setAsk({ kind: "load", profile: st.name, replaced: r.replaced, all });
      return;
    }
    onLoad(st, true, all);
    setLoaded(st.name);
    const changes = `${r.changed.toLocaleString()} ${all ? "more value" : "change"}${r.changed === 1 ? "" : "s"}`;
    setNote(
      all
        ? `Marked ${changes} of ${st.name} to write.`
        : r.changed > 0
          ? `Loaded ${st.name}: ${changes} to write, marked on their pages.`
          : r.replaced > 0
            ? `Loaded ${st.name}: the values now match it, with nothing to write.`
            : `The values here match ${st.name}; nothing to change.`,
    );
  }

  const disabled = busy || working;
  // What a full load of the profile loaded last would still mark, counted
  // afresh so reads since then take values off.
  const last = loaded && !disabled ? profiles.find((st) => st.name === loaded) : undefined;
  const unreadCVs = last ? onLoad(last, false, true).unread : [];
  const unread = unreadCVs.length;
  const pages = unread > 0 ? pagesFor(unreadCVs) : [];
  return (
    <section className="panel dec-profiles">
      <h2>Decoder profiles</h2>
      <p className="dim">
        Keep a copy of this decoder's values to load again later, such as how it came or how it ran best. Loading one marks what differs as changes; nothing reaches the decoder
        until you write them.
      </p>
      {profiles.length > 0 && (
        <ul className="dec-profiles__list">
          {profiles.map((st) => (
            <li key={st.name}>
              <span className="dec-profiles__name">{st.name}</span>
              <span className="dim">
                {Object.keys(st.cvs).length.toLocaleString()} values, kept {relativeTime(st.kept, now)}
              </span>
              <span className="dec-profiles__actions">
                <button className="btn btn--quiet" disabled={disabled} onClick={() => load(st, false)}>
                  Load
                </button>
                <button className="btn btn--quiet" disabled={disabled} onClick={() => setAsk({ kind: "delete", profile: st.name })}>
                  Delete
                </button>
              </span>
              {ask?.profile === st.name && ask.kind === "delete" && (
                <span className="confirm">
                  <span>Delete {st.name}? The decoder's values stay as they are.</span>
                  <button className="btn btn--danger" disabled={disabled} onClick={() => void remove(st.name)}>
                    Delete
                  </button>
                  <button className="btn btn--quiet" onClick={() => setAsk(undefined)}>
                    Cancel
                  </button>
                </span>
              )}
              {ask?.profile === st.name && ask.kind === "load" && (
                <span className="confirm">
                  <span>
                    {ask.all ? `Marking every value of ${st.name}` : `Loading ${st.name}`} replaces {ask.replaced === 1 ? "a change" : `${ask.replaced} changes`} not written yet.
                  </span>
                  <button className="btn btn--danger" disabled={disabled} onClick={() => load(st, true, ask.all)}>
                    {ask.all ? "Mark all" : "Load"}
                  </button>
                  <button className="btn btn--quiet" onClick={() => setAsk(undefined)}>
                    Cancel
                  </button>
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
      <form
        className="dec-profiles__keep"
        onSubmit={(e) => {
          e.preventDefault();
          void keep(false);
        }}
      >
        <label className="auto-field">
          <span>Name</span>
          <input className="input" value={name} maxLength={64} onChange={(e) => setName(e.target.value)} placeholder="As delivered" />
        </label>
        <button className="btn" type="submit" disabled={disabled || !name.trim()}>
          Keep these values
        </button>
      </form>
      {ask?.kind === "replace" && (
        <span className="confirm">
          <span>A profile named {ask.profile} is kept already. Replace it with these values?</span>
          <button className="btn btn--danger" disabled={disabled} onClick={() => void keep(true)}>
            Replace
          </button>
          <button className="btn btn--quiet" onClick={() => setAsk(undefined)}>
            Cancel
          </button>
        </span>
      )}
      {edited && <p className="dim">Changes not written yet are left out of a profile.</p>}
      {note && <p>{note}</p>}
      {last && unread > 0 && (
        <span className="confirm">
          <span>
            {unread.toLocaleString()} value{unread === 1 ? " matches" : "s match"} {last.name} here but {unread === 1 ? "was" : "were"} not read from the decoder in this session,
            so it may hold something else.{" "}
            {pages.length > 0 ? (
              <>
                Read{" "}
                {pages.map((p, i) => (
                  <span key={p.index}>
                    {i > 0 && (i === pages.length - 1 ? " and " : ", ")}
                    <button type="button" className="linklike" onClick={() => onPage(p.index)}>
                      {p.name}
                    </button>
                  </span>
                ))}
                , then load {last.name} again to check, or mark {unread === 1 ? "it" : "them"} to write too.
              </>
            ) : (
              <>
                {unread === 1 ? "It cannot" : "They cannot"} be read back; mark {unread === 1 ? "it" : "them"} to write too.
              </>
            )}
          </span>
          <button className="btn" onClick={() => load(last, false, true)}>
            Mark {unread === 1 ? "it" : "them"} too
          </button>
        </span>
      )}
      {error && <p className="field__error">{error}</p>}
    </section>
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
      <p className="dim">Save this decoder with a locomotive, with the values read so far and any changes not written yet, so its settings open without reading them again.</p>
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
