import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { ApiError, api, type Automation, type AutomationStatus } from "../api";
import { MODES, newAction, newTrigger, pathLabel, tidy } from "../automation";
import { ActionList } from "../components/automation/ActionRows";
import { ConditionList } from "../components/automation/ConditionRows";
import { TriggerList } from "../components/automation/TriggerRows";
import { EventNameList } from "../components/EventNameList";
import { Page } from "../components/Page";
import { useBus } from "../store";

function blank(): Automation {
  return { id: "", name: "", enabled: true, mode: "single", triggers: [newTrigger("state")], conditions: [], actions: [newAction("command")] };
}

interface Problem {
  message: string;
  path?: string;
}

export function AutomationEditor() {
  const { id } = useParams();
  const navigate = useNavigate();
  const { automations, eventNames, loaded } = useBus();
  const existing: AutomationStatus | undefined = id ? automations[id] : undefined;
  const [doc, setDoc] = useState<Automation | undefined>(id ? undefined : blank());
  const [problem, setProblem] = useState<Problem>();
  const [saving, setSaving] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [yaml, setYaml] = useState<string>();
  const [missing, setMissing] = useState(false);

  // Edit a copy of the stored document; later live updates do not clobber typing.
  useEffect(() => {
    if (!id || doc) return;
    if (existing) {
      setDoc(structuredClone({ ...existing, running: undefined, queued: undefined, last_triggered: undefined, last_result: undefined, last_trace: undefined, problems: undefined }) as Automation);
    } else if (loaded) {
      api
        .automation(id)
        .then((a) => setDoc(a))
        .catch(() => setMissing(true));
    }
  }, [id, existing, loaded, doc]);

  if (missing) {
    return (
      <Page title="Not found" back={<Back />}>
        <p className="muted">There is no automation called {id}.</p>
      </Page>
    );
  }
  if (!doc) {
    return (
      <Page title="Automation" back={<Back />}>
        <p className="muted">Loading…</p>
      </Page>
    );
  }

  const update = (patch: Partial<Automation>) => setDoc({ ...doc, ...patch });

  async function save(ev: FormEvent) {
    ev.preventDefault();
    if (!doc) return;
    setProblem(undefined);
    if (!doc.name.trim()) {
      setProblem({ message: "Give it a name.", path: "name" });
      return;
    }
    setSaving(true);
    try {
      const body = tidy(doc);
      const saved = id ? await api.updateAutomation(id, body) : await api.createAutomation(body);
      navigate(id ? `/automations/${encodeURIComponent(saved.id)}/traces` : "/automations");
    } catch (err) {
      if (err instanceof ApiError && typeof err.body?.path === "string") {
        setProblem({ message: err.message, path: err.body.path });
      } else {
        setProblem({ message: err instanceof Error ? err.message : String(err) });
      }
    } finally {
      setSaving(false);
    }
  }

  async function remove() {
    if (!id) return;
    setSaving(true);
    try {
      await api.deleteAutomation(id);
      navigate("/automations");
    } catch (err) {
      setProblem({ message: err instanceof Error ? err.message : String(err) });
      setSaving(false);
    }
  }

  async function showYaml() {
    if (yaml !== undefined) {
      setYaml(undefined);
      return;
    }
    if (!id) return;
    try {
      setYaml(await api.automationYAML(id));
    } catch (err) {
      setProblem({ message: err instanceof Error ? err.message : String(err) });
    }
  }

  const mode = MODES.find((m) => m.value === (doc.mode ?? "single")) ?? MODES[0];

  return (
    <Page title={id ? doc.name || "Automation" : "New automation"} back={<Back />}>
      <EventNameList names={eventNames} />
      <form className="auto-editor" onSubmit={(e) => void save(e)}>
        <div className="panel auto-plate">
          <div className="auto-row auto-row--plain">
            <label className="auto-field auto-field--wide">
              <span>Name</span>
              <input className="input input--text" value={doc.name} onChange={(e) => update({ name: e.target.value })} placeholder="Close Turnout 1 behind a train" autoFocus={!id} />
            </label>
            <label className="auto-field auto-field--wide">
              <span>Description</span>
              <input className="input input--text" value={doc.description ?? ""} onChange={(e) => update({ description: e.target.value })} placeholder="optional" />
            </label>
          </div>
          <div className="auto-row auto-row--plain">
            <label className="auto-field">
              <span>While it is running</span>
              <select className="input input--select" value={doc.mode ?? "single"} onChange={(e) => update({ mode: e.target.value as Automation["mode"] })}>
                {MODES.map((m) => (
                  <option key={m.value} value={m.value}>
                    {m.label}
                  </option>
                ))}
              </select>
            </label>
            <span className="auto-hint">{mode.hint}</span>
            {(doc.mode === "queued" || doc.mode === "parallel") && (
              <label className="auto-field">
                <span>At most</span>
                <input className="input input--num" type="number" min={1} max={100} value={doc.max ?? 10} onChange={(e) => update({ max: Number(e.target.value) })} />
              </label>
            )}
            <label className="check auto-check">
              <input type="checkbox" checked={doc.enabled} onChange={(e) => update({ enabled: e.target.checked })} />
              turned on
            </label>
          </div>
        </div>

        <section className="auto-part">
          <h2>When</h2>
          <p className="muted">Any one of these starts a run.</p>
          <div className="panel">
            <TriggerList triggers={doc.triggers} onChange={(triggers) => update({ triggers })} labels />
          </div>
        </section>

        <section className="auto-part">
          <h2>Only if</h2>
          <p className="muted">All of these must hold when it starts, or the run stops here.</p>
          <div className="panel">
            <ConditionList conditions={doc.conditions ?? []} onChange={(conditions) => update({ conditions })} />
          </div>
        </section>

        <section className="auto-part">
          <h2>Then</h2>
          <p className="muted">Done in order. A wait or a delay holds the run; a stop ends it.</p>
          <div className="panel">
            <ActionList actions={doc.actions} onChange={(actions) => update({ actions })} />
          </div>
        </section>

        {problem && (
          <p className="note note--error auto-problem-note">
            {problem.path ? <strong>{pathLabel(problem.path)}: </strong> : null}
            {problem.message}
          </p>
        )}

        <div className="auto-actions">
          <button type="submit" className="btn btn--primary" disabled={saving}>
            {id ? "Save" : "Create"}
          </button>
          <Link className="btn btn--quiet" to={id ? `/automations/${encodeURIComponent(id)}/traces` : "/automations"}>
            Cancel
          </Link>
          {id && (
            <button type="button" className="btn btn--quiet" onClick={() => void showYaml()}>
              {yaml === undefined ? "Show as YAML" : "Hide YAML"}
            </button>
          )}
          {id &&
            (confirmDelete ? (
              <span className="confirm">
                <span>Delete {doc.name}?</span>
                <button type="button" className="btn btn--danger btn--sm" disabled={saving} onClick={() => void remove()}>
                  Delete
                </button>
                <button type="button" className="btn btn--quiet btn--sm" onClick={() => setConfirmDelete(false)}>
                  Keep
                </button>
              </span>
            ) : (
              <button type="button" className="btn btn--danger-quiet auto-actions__delete" onClick={() => setConfirmDelete(true)}>
                Delete…
              </button>
            ))}
        </div>
        {yaml !== undefined && (
          <div className="panel auto-yaml">
            <p className="muted">As written to automations.yaml in the data directory. Unsaved changes are not in it yet.</p>
            <pre>{yaml}</pre>
          </div>
        )}
      </form>
    </Page>
  );
}

function Back() {
  return (
    <Link className="page__back" to="/automations">
      ← Automations
    </Link>
  );
}
