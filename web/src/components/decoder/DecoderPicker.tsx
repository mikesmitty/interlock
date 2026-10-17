import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { api } from "../../api";
import type { DecoderCandidate, DecoderIdentity, DecoderIndex, DecoderSource } from "../../decoder/types";

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function settingsLink(c: { file: string; model: string }): string {
  return `/programming/decoder?${new URLSearchParams({ file: c.file, model: c.model }).toString()}`;
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

/** Finding a decoder's definition: identify it on the track, or search. */
export function DecoderPicker() {
  const [source, setSource] = useState<DecoderSource>();
  const [identifying, setIdentifying] = useState(false);
  const [found, setFound] = useState<{ identity: DecoderIdentity; manufacturer?: string; candidates: DecoderCandidate[]; note?: string }>();
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
  const matches = words.length === 0 ? [] : entries.filter((e) => words.every((w) => e.search.includes(w))).slice(0, 30);

  const id = found?.identity;
  return (
    <div className="panel dec-picker">
      <h2>Decoder settings</h2>
      <p className="dim">Change a decoder's settings by name. Identify the decoder on the programming track, or search for its model.</p>
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
            {id.has_product_id ? `, product ${id.product_id}` : ""}.
          </p>
          {found.note && <p className="note">{found.note}</p>}
          {found.candidates.length === 0 && !found.note && <p className="note">No definition matches this decoder. Search for its model instead.</p>}
          {found.candidates.length > 1 && <p className="dim">More than one definition fits. Choose the decoder that is fitted:</p>}
          <ul className="dec-picker__list">
            {found.candidates.map((c) => (
              <li key={`${c.file}/${c.model}`}>
                <Link to={settingsLink(c)}>{c.model}</Link> <span className="dim">{c.family}</span>
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
          {matches.length === 0 && <li className="dim">No decoder matches.</li>}
          {matches.map((e) => (
            <li key={`${e.file}/${e.model}`}>
              <Link to={settingsLink(e)}>{e.model}</Link> <span className="dim">{e.family === e.model ? e.manufacturer : `${e.family}, ${e.manufacturer}`}</span>
            </li>
          ))}
        </ul>
      )}
      {words.length > 0 && !index && !indexError && <p className="dim">Reading the decoder list…</p>}
      {source && <p className="faint">{sourceText(source)}</p>}
    </div>
  );
}
