// The configuration form a node's CDI describes: segments as sections,
// groups as fieldsets, replicated groups as tabs, and a save bar that
// writes only what changed.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, ApiError, type CdiNode, type CdiSegment, type Layout, type Values } from "../api";
import { Lamp } from "./Lamp";

type SegmentState = { status: "waiting" | "reading" | "ready" | "error"; error?: string };

interface ConfigFormProps {
  nodeId: string;
  nodeName: string;
  online: boolean;
}

export function ConfigForm({ nodeId, nodeName, online }: ConfigFormProps) {
  const [layout, setLayout] = useState<Layout>();
  const [layoutError, setLayoutError] = useState<string>();
  const [segments, setSegments] = useState<SegmentState[]>([]);
  const [values, setValues] = useState<Values>({});
  const [edits, setEdits] = useState<Values>({});
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<{ kind: "ok" | "error"; text: string }>();
  const [readPass, setReadPass] = useState(0);
  const readToken = useRef(0);

  // Load the layout once per node.
  useEffect(() => {
    let cancelled = false;
    setLayout(undefined);
    setLayoutError(undefined);
    setValues({});
    setEdits({});
    api
      .cdi(nodeId)
      .then((r) => {
        if (cancelled) return;
        setLayout(r.layout);
        setSegments(r.layout.segments.map(() => ({ status: "waiting" })));
      })
      .catch((err: unknown) => {
        if (!cancelled) setLayoutError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [nodeId]);

  // Read the segments one after another; a node answers 64 bytes at a time.
  useEffect(() => {
    if (!layout || !online) return;
    const token = ++readToken.current;
    let cancelled = false;
    (async () => {
      for (let i = 0; i < layout.segments.length; i++) {
        const seg = layout.segments[i];
        if (cancelled || token !== readToken.current) return;
        if (seg.size === 0) {
          setSegments((s) => s.map((st, j) => (j === i ? { status: "ready" } : st)));
          continue;
        }
        setSegments((s) => s.map((st, j) => (j === i ? { status: "reading" } : st)));
        try {
          const res = await api.configRead(nodeId, { space: seg.space, address: seg.origin, size: seg.size });
          if (cancelled || token !== readToken.current) return;
          setValues((v) => ({ ...v, ...res.values }));
          setSegments((s) => s.map((st, j) => (j === i ? { status: "ready" } : st)));
        } catch (err) {
          if (cancelled || token !== readToken.current) return;
          const text = err instanceof Error ? err.message : String(err);
          setSegments((s) => s.map((st, j) => (j === i ? { status: "error", error: text } : st)));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [layout, nodeId, online, readPass]);

  const dirtyKeys = useMemo(() => Object.keys(edits).filter((k) => edits[k] !== (values[k] ?? "")), [edits, values]);

  const setEdit = useCallback((key: string, value: string) => {
    setEdits((e) => ({ ...e, [key]: value }));
    setFieldErrors((fe) => {
      if (!(key in fe)) return fe;
      const next = { ...fe };
      delete next[key];
      return next;
    });
  }, []);

  function discard() {
    setEdits({});
    setFieldErrors({});
    setMessage(undefined);
  }

  async function save(extra?: Values) {
    const batch: Values = {};
    for (const k of dirtyKeys) batch[k] = edits[k];
    Object.assign(batch, extra);
    if (Object.keys(batch).length === 0) return;
    setSaving(true);
    setMessage(undefined);
    try {
      const res = await api.configWrite(nodeId, batch);
      setValues((v) => ({ ...v, ...batch }));
      setEdits((e) => {
        const next = { ...e };
        for (const k of res.written) delete next[k];
        return next;
      });
      const n = res.written.length;
      setMessage({ kind: "ok", text: n === 1 ? "Saved 1 value to the node." : `Saved ${n} values to the node.` });
    } catch (err) {
      if (err instanceof ApiError && err.body) {
        const invalid = err.body.invalid as Record<string, string> | undefined;
        if (invalid) {
          setFieldErrors(invalid);
          setMessage({ kind: "error", text: "Nothing was written: some values are not valid. They are marked below." });
          setSaving(false);
          return;
        }
        const written = err.body.written as string[] | undefined;
        const failed = err.body.failed as string | undefined;
        if (written && written.length > 0) {
          const done: Values = {};
          for (const k of written) done[k] = batch[k];
          setValues((v) => ({ ...v, ...done }));
          setEdits((e) => {
            const next = { ...e };
            for (const k of written) delete next[k];
            return next;
          });
        }
        if (failed) setFieldErrors({ [failed]: err.message });
      }
      setMessage({ kind: "error", text: err instanceof Error ? err.message : String(err) });
    } finally {
      setSaving(false);
    }
  }

  if (layoutError) {
    return <p className="note note--error">The configuration layout could not be loaded: {layoutError}</p>;
  }
  if (!layout) {
    return <p className="muted">Loading the configuration layout…</p>;
  }

  const anyReading = segments.some((s) => s.status === "reading" || s.status === "waiting");

  const showBar = dirtyKeys.length > 0 || message !== undefined;
  return (
    <div className={showBar ? "cfg cfg--has-bar" : "cfg"}>
      {!online && <p className="note">The node is offline. Values cannot be read or written until it is back.</p>}
      {layout.segments.map((seg, i) => (
        <Segment
          key={`${seg.space}:${seg.origin}`}
          segment={seg}
          state={segments[i] ?? { status: "waiting" }}
          values={values}
          edits={edits}
          errors={fieldErrors}
          disabled={!online || saving}
          onChange={setEdit}
          onAction={(key, value) => void save({ [key]: value })}
        />
      ))}
      <div className="cfg__foot">
        <button className="btn btn--quiet" onClick={() => setReadPass((n) => n + 1)} disabled={!online || anyReading || saving}>
          Read values again
        </button>
        <DangerZone nodeId={nodeId} nodeName={nodeName} disabled={!online || saving} onMessage={setMessage} />
      </div>
      {showBar && (
        <div className="savebar" role="region" aria-label="Unsaved changes">
          <div className="savebar__inner">
            {message && <span className={message.kind === "error" ? "savebar__msg savebar__msg--error" : "savebar__msg"}>{message.text}</span>}
            {dirtyKeys.length > 0 && (
              <>
                <span className="savebar__count">{dirtyKeys.length === 1 ? "1 change" : `${dirtyKeys.length} changes`} not yet on the node</span>
                <button className="btn btn--quiet" onClick={discard} disabled={saving}>
                  Discard
                </button>
                <button className="btn btn--primary" onClick={() => void save()} disabled={saving || !online}>
                  {saving ? "Saving…" : "Save to node"}
                </button>
              </>
            )}
            {dirtyKeys.length === 0 && message && (
              <button className="btn btn--quiet" onClick={() => setMessage(undefined)}>
                Dismiss
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

interface ItemProps {
  values: Values;
  edits: Values;
  errors: Record<string, string>;
  disabled: boolean;
  onChange: (key: string, value: string) => void;
  onAction: (key: string, value: string) => void;
}

function Segment({ segment, state, ...rest }: { segment: CdiSegment; state: SegmentState } & ItemProps) {
  const visible = segment.items.filter(isShown);
  if (visible.length === 0) return null;
  return (
    <section className="cfg-segment">
      <header className="cfg-segment__head">
        <h2>{segment.name || `Space ${segment.space} at ${segment.origin}`}</h2>
        <SegmentLamp state={state} />
      </header>
      {segment.description && <p className="cfg-desc">{segment.description}</p>}
      {state.status === "error" && <p className="note note--error">Could not read this part of the node: {state.error}</p>}
      <div className="panel cfg-panel">
        <Items items={segment.items} {...rest} />
      </div>
    </section>
  );
}

function SegmentLamp({ state }: { state: SegmentState }) {
  switch (state.status) {
    case "ready":
      return <Lamp color="green" label="Values read" />;
    case "reading":
      return (
        <span className="cfg-segment__status">
          <Lamp color="amber" label="Reading" pulse /> reading
        </span>
      );
    case "error":
      return <Lamp color="red" label="Read failed" />;
    default:
      return <Lamp color="off" label="Waiting" />;
  }
}

/** Unnamed strings are padding the firmware keeps the layout aligned with. */
function isShown(node: CdiNode): boolean {
  if (node.kind === "group") return true;
  return node.name !== "" || node.kind !== "string";
}

function Items({ items, ...rest }: { items: CdiNode[] } & ItemProps) {
  return (
    <>
      {items
        .filter(isShown)
        .map((item) =>
          item.kind === "group" ? (
            <Group key={`${item.space}:${item.address}:${item.name}`} node={item} {...rest} />
          ) : (
            <Field key={item.key ?? `${item.space}:${item.address}`} node={item} {...rest} />
          ),
        )}
    </>
  );
}

function Group({ node, ...rest }: { node: CdiNode } & ItemProps) {
  const replicas = node.replicas ?? [];
  const [tab, setTab] = useState(0);
  const hideable = node.hints?.hideable;
  const [open, setOpen] = useState(!node.hints?.hidden);
  const readOnly = node.hints?.read_only ?? false;
  const inner = (
    <>
      {node.description && <p className="cfg-desc">{node.description}</p>}
      {replicas.length > 1 ? (
        <>
          <div className="cfg-tabs" role="tablist" aria-label={node.name}>
            {replicas.map((rep, i) => (
              <button key={rep.name} role="tab" type="button" aria-selected={i === tab} className="cfg-tab" onClick={() => setTab(i)}>
                {rep.name}
              </button>
            ))}
          </div>
          <div role="tabpanel" className="cfg-tabpanel">
            <Items items={replicas[Math.min(tab, replicas.length - 1)].items} {...rest} disabled={rest.disabled || readOnly} />
          </div>
        </>
      ) : (
        <Items items={replicas[0]?.items ?? []} {...rest} disabled={rest.disabled || readOnly} />
      )}
    </>
  );
  if (hideable) {
    return (
      <fieldset className="cfg-group">
        <legend>
          <button type="button" className="cfg-group__toggle" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
            <span className="cfg-group__chev" aria-hidden="true">
              {open ? "▾" : "▸"}
            </span>
            {node.name || "Settings"}
          </button>
        </legend>
        {open && inner}
      </fieldset>
    );
  }
  // Tabs named "Servo 1", "Servo 2" already say what the group is.
  const tabsCarryName = replicas.length > 1 && node.name !== "" && replicas.every((r) => r.name.startsWith(node.name));
  return (
    <fieldset className="cfg-group">
      {node.name && !tabsCarryName && <legend>{node.name}</legend>}
      {inner}
    </fieldset>
  );
}

const EVENT_RE = /^(?:[0-9a-fA-F]{2}[.:]?){7}[0-9a-fA-F]{2}$/;

function Field({ node, values, edits, errors, disabled, onChange, onAction }: { node: CdiNode } & ItemProps) {
  const key = node.key ?? `${node.space}:${node.address}`;
  const current = values[key];
  const value = key in edits ? edits[key] : (current ?? "");
  const dirty = key in edits && edits[key] !== (current ?? "");
  const error = errors[key];
  const unread = current === undefined && !(key in edits);
  const id = `f-${key.replace(":", "-")}`;
  const classes = ["field", dirty ? "is-dirty" : "", error ? "is-invalid" : "", unread ? "is-unread" : ""].filter(Boolean).join(" ");

  let control: React.ReactNode;
  switch (node.kind) {
    case "action":
      control = <ActionButton node={node} disabled={disabled} onFire={(v) => onAction(key, v)} />;
      break;
    case "int":
      if (node.map && node.map.length > 0) {
        if (node.hints?.checkbox && node.map.length === 2) {
          const on = node.map[1].property;
          control = (
            <label className="check">
              <input type="checkbox" id={id} checked={value === on} disabled={disabled} onChange={(e) => onChange(key, e.target.checked ? on : node.map![0].property)} />
              {value === on ? node.map[1].value : node.map[0].value}
            </label>
          );
        } else if (node.hints?.radio_button) {
          control = (
            <div className="radios" role="radiogroup" aria-labelledby={`${id}-label`}>
              {node.map.map((rel) => (
                <label key={rel.property} className="check">
                  <input type="radio" name={id} value={rel.property} checked={value === rel.property} disabled={disabled} onChange={() => onChange(key, rel.property)} />
                  {rel.value}
                </label>
              ))}
            </div>
          );
        } else {
          const known = node.map.some((r) => r.property === value);
          control = (
            <select id={id} className="input input--select" value={value} disabled={disabled} onChange={(e) => onChange(key, e.target.value)}>
              {unread && <option value="">—</option>}
              {!unread && !known && <option value={value}>{value ? `Value ${value} (not a listed choice)` : "—"}</option>}
              {node.map.map((rel) => (
                <option key={rel.property} value={rel.property}>
                  {rel.value}
                </option>
              ))}
            </select>
          );
        }
      } else if (node.hints?.slider) {
        control = (
          <span className="slider">
            <input type="range" min={node.min ?? 0} max={node.max ?? 255} value={value || node.min || 0} disabled={disabled} onChange={(e) => onChange(key, e.target.value)} />
            <input type="number" className="input input--num" min={node.min} max={node.max} value={value} disabled={disabled} onChange={(e) => onChange(key, e.target.value)} />
          </span>
        );
      } else {
        control = (
          <input
            id={id}
            type="number"
            className="input input--num"
            inputMode="numeric"
            min={node.min}
            max={node.max}
            step={1}
            value={value}
            placeholder={unread ? "" : undefined}
            disabled={disabled}
            onChange={(e) => onChange(key, e.target.value)}
          />
        );
      }
      break;
    case "float":
      control = (
        <input
          id={id}
          type="number"
          className="input input--num"
          step="any"
          min={node.min}
          max={node.max}
          value={value}
          disabled={disabled}
          onChange={(e) => onChange(key, e.target.value)}
        />
      );
      break;
    case "eventid": {
      const bad = value !== "" && !EVENT_RE.test(value);
      control = (
        <input
          id={id}
          type="text"
          className="input input--event"
          spellCheck={false}
          autoComplete="off"
          value={value}
          placeholder="00.00.00.00.00.00.00.00"
          aria-invalid={bad || undefined}
          disabled={disabled}
          onChange={(e) => onChange(key, e.target.value)}
        />
      );
      break;
    }
    case "blob":
      control = <code className="cfg-blob">{value || "—"}</code>;
      break;
    default:
      control = (
        <input
          id={id}
          type="text"
          className="input input--text"
          maxLength={Math.max(node.size - 1, 0)}
          value={value}
          disabled={disabled}
          onChange={(e) => onChange(key, e.target.value)}
        />
      );
  }

  return (
    <div className={classes}>
      <label className="field__label" htmlFor={id} id={`${id}-label`}>
        {node.name || `At ${node.address}`}
      </label>
      <div className="field__control">
        {control}
        {node.kind !== "action" && node.kind !== "blob" && (node.min !== undefined || node.max !== undefined) && !node.map && (
          <span className="field__range">
            {node.min ?? "…"} to {node.max ?? "…"}
          </span>
        )}
        {dirty && <span className="field__dirty">changed</span>}
      </div>
      {error && <p className="field__error">{error}</p>}
      {node.description && <p className="field__desc">{node.description}</p>}
    </div>
  );
}

function ActionButton({ node, disabled, onFire }: { node: CdiNode; disabled: boolean; onFire: (value: string) => void }) {
  const [confirming, setConfirming] = useState(false);
  const label = node.button_text || node.name || "Run";
  if (node.dialog_text && confirming) {
    return (
      <span className="confirm">
        <span>{node.dialog_text}</span>
        <button
          type="button"
          className="btn btn--primary"
          onClick={() => {
            setConfirming(false);
            onFire(node.value ?? "1");
          }}
          disabled={disabled}
        >
          {label}
        </button>
        <button type="button" className="btn btn--quiet" onClick={() => setConfirming(false)}>
          Cancel
        </button>
      </span>
    );
  }
  return (
    <button type="button" className="btn" disabled={disabled} onClick={() => (node.dialog_text ? setConfirming(true) : onFire(node.value ?? "1"))}>
      {label}
    </button>
  );
}

function DangerZone({
  nodeId,
  nodeName,
  disabled,
  onMessage,
}: {
  nodeId: string;
  nodeName: string;
  disabled: boolean;
  onMessage: (m: { kind: "ok" | "error"; text: string }) => void;
}) {
  const [arm, setArm] = useState<"reboot" | "reset">();
  const [busy, setBusy] = useState(false);

  async function run(kind: "reboot" | "reset") {
    setBusy(true);
    try {
      if (kind === "reboot") {
        await api.reboot(nodeId);
        onMessage({ kind: "ok", text: "Asked the node to restart. It will reappear in a moment." });
      } else {
        await api.factoryReset(nodeId);
        onMessage({ kind: "ok", text: "Asked the node to return to factory settings. Read the values again once it is back." });
      }
    } catch (err) {
      onMessage({ kind: "error", text: err instanceof Error ? err.message : String(err) });
    } finally {
      setBusy(false);
      setArm(undefined);
    }
  }

  if (arm) {
    const reset = arm === "reset";
    return (
      <div className="confirm confirm--block">
        <span>
          {reset ? `Reset ${nodeName} to factory settings? Every value above goes back to its default.` : `Restart ${nodeName}? It will be off the bus for a few seconds.`}
        </span>
        <button type="button" className={reset ? "btn btn--danger" : "btn btn--primary"} disabled={busy || disabled} onClick={() => void run(arm)}>
          {reset ? "Yes, reset it" : "Yes, restart it"}
        </button>
        <button type="button" className="btn btn--quiet" onClick={() => setArm(undefined)} disabled={busy}>
          Cancel
        </button>
      </div>
    );
  }
  return (
    <div className="cfg__danger">
      <button type="button" className="btn btn--quiet" disabled={disabled} onClick={() => setArm("reboot")}>
        Restart node
      </button>
      <button type="button" className="btn btn--quiet btn--danger-quiet" disabled={disabled} onClick={() => setArm("reset")}>
        Factory reset…
      </button>
    </div>
  );
}
