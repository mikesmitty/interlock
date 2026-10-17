import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router";
import { api, type IdentifyResult, type RosterIdentity } from "../../api";
import type { DecoderIdentity, DecoderIndex, DecoderSource } from "../../decoder/types";
import { useBus } from "../../store";

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** A definition picked: its file, family and model. */
export interface PickedDefinition {
  file: string;
  family: string;
  model: string;
}

/** The identity as the roster keeps it. */
export function rosterIdentity(id: DecoderIdentity): RosterIdentity {
  return { manufacturer: id.manufacturer, version: id.version, product_id: id.has_product_id ? id.product_id : undefined };
}

/** The settings page for a definition, with the identity read, if any, so it can go into the roster. */
export function settingsLink(c: { file: string; model: string }, id?: DecoderIdentity): string {
  const q = new URLSearchParams({ file: c.file, model: c.model });
  if (id) {
    q.set("mfg", String(id.manufacturer));
    q.set("ver", String(id.version));
    if (id.has_product_id && id.product_id !== undefined) q.set("pid", String(id.product_id));
  }
  return `/programming/decoder?${q.toString()}`;
}

/** The settings page for a roster locomotive's decoder. */
export function rosterSettingsLink(train: string, decoder: string): string {
  return `/programming/decoder?${new URLSearchParams({ loco: train, decoder }).toString()}`;
}

function sourceText(s: DecoderSource): string {
  switch (s.source) {
    case "folder":
      return `Decoder definitions are read from ${s.path}.`;
    case "archive":
      if (s.archive?.state === "downloading") return `Downloading JMRI ${s.ref}'s decoder definitions: ${Math.round((s.archive.bytes ?? 0) / 1e6)} MB so far.`;
      if (s.archive?.state === "failed") return `The download of JMRI ${s.ref}'s decoder definitions failed (${s.archive.error}); files are fetched one at a time instead.`;
      return `Decoder definitions come from JMRI ${s.ref}, downloaded once.`;
  }
  return `Decoder definitions come from JMRI ${s.ref}, fetched from GitHub as they are needed.`;
}

interface Entry {
  manufacturer: string;
  family: string;
  model: string;
  file: string;
  search: string;
}

/**
 * Finding a decoder's definition: identify it on the track, or search.
 * Without onPick, a definition opens its settings and Identify names the
 * roster locomotive on the track; with it, picking hands the definition
 * back, as adding a decoder to a locomotive does.
 */
export function DecoderPicker({
  onPick,
  title = "Decoder settings",
  intro = "Change a decoder's settings by name. Identify the decoder on the programming track, or search for its model.",
}: {
  onPick?: (d: PickedDefinition, identity?: DecoderIdentity) => void;
  title?: string;
  intro?: string;
}) {
  const navigate = useNavigate();
  const { trains } = useBus();
  const [source, setSource] = useState<DecoderSource>();
  const [identifying, setIdentifying] = useState(false);
  const [found, setFound] = useState<IdentifyResult>();
  const [adding, setAdding] = useState(false);
  const [error, setError] = useState<string>();
  const [index, setIndex] = useState<DecoderIndex>();
  const [indexError, setIndexError] = useState<string>();
  const [query, setQuery] = useState("");

  useEffect(() => {
    api.decoderSource().then(setSource).catch(() => setSource(undefined));
  }, []);

  function loadIndex() {
    if (index || indexError) return;
    api
      .decoderIndex()
      .then(setIndex)
      .catch((err) => setIndexError(describe(err)));
  }

  async function identify() {
    setIdentifying(true);
    setError(undefined);
    setFound(undefined);
    try {
      setFound(await api.identifyDecoder());
    } catch (err) {
      setError(describe(err));
    } finally {
      setIdentifying(false);
    }
  }

  const entries = useMemo<Entry[]>(() => {
    if (!index) return [];
    const out: Entry[] = [];
    for (const f of index.families) {
      if (f.modes) continue;
      for (const m of f.models) {
        if (m.show === "no") continue;
        out.push({ manufacturer: f.manufacturer, family: f.name, model: m.name, file: f.file, search: `${f.manufacturer} ${f.name} ${m.name}`.toLowerCase() });
      }
    }
    return out;
  }, [index]);

  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  const results = words.length === 0 ? [] : entries.filter((e) => words.every((w) => e.search.includes(w))).slice(0, 30);

  const id = found?.identity;
  // Roster locomotives at the decoder's address that have no decoder yet
  // can take the definition picked.
  const bare = onPick ? [] : (found?.roster ?? []).filter((m) => !m.decoder && trains[m.train]);
  const matches = onPick ? [] : (found?.roster ?? []).filter((m) => m.decoder && trains[m.train]);

  async function addTo(train: string, d: PickedDefinition) {
    setAdding(true);
    setError(undefined);
    try {
      const r = await api.addDecoder(train, { ...d, identity: id ? rosterIdentity(id) : undefined, install: true });
      navigate(rosterSettingsLink(train, r.decoder.id));
    } catch (err) {
      setError(describe(err));
    } finally {
      setAdding(false);
    }
  }

  async function install(train: string, decoder: string) {
    setError(undefined);
    try {
      await api.installDecoder(train, decoder);
    } catch (err) {
      setError(describe(err));
    }
  }

  /** A definition in a list: a link to its settings, or a button that picks it. */
  function choice(d: PickedDefinition, extra?: string) {
    return (
      <>
        {onPick ? (
          <button type="button" className="linklike" onClick={() => onPick(d, id)}>
            {d.model}
          </button>
        ) : (
          <Link to={settingsLink(d, id)}>{d.model}</Link>
        )}{" "}
        <span className="dim">{extra ?? d.family}</span>
        {bare.map((m) => (
          <button key={m.train} type="button" className="linklike dec-picker__add" disabled={adding} onClick={() => void addTo(m.train, d)}>
            add to {trains[m.train].name}
          </button>
        ))}
      </>
    );
  }

  return (
    <div className="panel dec-picker">
      <h2>{title}</h2>
      <p className="dim">{intro}</p>
      <div className="dec-picker__identify">
        <button className="btn" onClick={() => void identify()} disabled={identifying}>
          {identifying ? "Identifying…" : "Identify the decoder"}
        </button>
        {error && <span className="field__error">{error}</span>}
      </div>
      {id && (
        <div className="dec-picker__found">
          <p>
            {found.manufacturer ?? `Manufacturer ${id.manufacturer}`}, version {id.version}
            {id.has_product_id ? `, product ${id.product_id}` : ""}
            {found.address ? `, at address ${found.address.address}${found.address.long && found.address.address <= 127 ? " (long)" : ""}` : ""}.
          </p>
          {matches.length > 0 && (
            <ul className="dec-picker__roster">
              {matches.map((m) => {
                const t = trains[m.train];
                const d = t.decoders?.find((x) => x.id === m.decoder);
                if (!d) return null;
                return (
                  <li key={`${m.train}/${m.decoder}`}>
                    <Link to={rosterSettingsLink(m.train, d.id)}>
                      {t.name}: {d.name || d.model || d.id}
                    </Link>
                    {!m.identity && <span className="faint"> (same address; its identity was never saved)</span>}
                    {!m.installed && (
                      <>
                        {" "}
                        <span className="dim">is not marked as fitted.</span>{" "}
                        <button type="button" className="linklike" onClick={() => void install(m.train, d.id)}>
                          Mark it fitted
                        </button>
                      </>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
          {matches.length > 0 && found.candidates.length > 0 && <p className="dim">Or open a definition without the roster:</p>}
          {found.note && <p className="note">{found.note}</p>}
          {found.candidates.length === 0 && !found.note && <p className="note">No definition matches this decoder. Search for its model instead.</p>}
          {found.candidates.length > 1 && matches.length === 0 && <p className="dim">More than one definition fits. Choose the decoder that is fitted:</p>}
          <ul className="dec-picker__list">
            {found.candidates.map((c) => (
              <li key={`${c.file}/${c.model}`}>
                {choice(c)}
                {!c.exact && found.identity.has_product_id && <span className="faint"> (matched by maker and version only)</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
      <label className="dec-picker__search">
        <span>Search</span>
        <input
          className="input input--text"
          type="search"
          placeholder="LokSound 5 micro"
          value={query}
          onFocus={loadIndex}
          onChange={(e) => setQuery(e.target.value)}
        />
      </label>
      {indexError && <p className="field__error">{indexError}</p>}
      {words.length > 0 && index && (
        <ul className="dec-picker__list">
          {results.length === 0 && <li className="dim">No decoder matches.</li>}
          {results.map((e) => (
            <li key={`${e.file}/${e.model}`}>{choice(e, e.family === e.model ? e.manufacturer : `${e.family}, ${e.manufacturer}`)}</li>
          ))}
        </ul>
      )}
      {words.length > 0 && !index && !indexError && <p className="dim">Reading the decoder list…</p>}
      {source && <p className="faint">{sourceText(source)}</p>}
    </div>
  );
}
