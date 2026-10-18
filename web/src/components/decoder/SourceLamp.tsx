import type { CVState } from "../../decoder/values";

// Where a value came from, as a lamp: green for values read from the
// decoder (filled) or written to it (a ring) in this session, amber for
// values it has not confirmed, imported (filled) or the definition's
// default (a ring). Values saved before the session have no lamp, and
// changes not written yet have the brass mark instead.
const SOURCES: Partial<Record<CVState, { lamp: string; word: string; title: string }>> = {
  read: { lamp: "lamp--green", word: "Read", title: "Read from the decoder in this session" },
  stored: { lamp: "dec-lamp--ring-green", word: "Written", title: "Written to the decoder in this session, not read back" },
  imported: { lamp: "lamp--amber", word: "Imported", title: "Imported, not read from the decoder" },
  fromfile: { lamp: "dec-lamp--ring-amber", word: "Default", title: "The definition's default, not read from the decoder" },
  unknown: { lamp: "", word: "Not read", title: "Not read from the decoder" },
};

const KEY: CVState[] = ["read", "stored", "imported", "fromfile", "unknown"];

/** A setting's source lamp, or an empty space the same size so values line up. */
export function SourceLamp({ state }: { state: CVState | undefined }) {
  const s = state && SOURCES[state];
  if (!s) return <span className="dec-lamp" aria-hidden="true" />;
  return <span className={`lamp dec-lamp ${s.lamp}`} role="img" aria-label={s.title} title={s.title} />;
}

/** What the lamps mean. */
export function SourceKey() {
  return (
    <p className="dec-key">
      {KEY.map((state) => (
        <span key={state} className="dec-key__item">
          <span className={`lamp dec-lamp ${SOURCES[state]!.lamp}`} aria-hidden="true" />
          {SOURCES[state]!.word}
        </span>
      ))}
      <span className="dec-key__item">
        <span className="dec-key__changed" aria-hidden="true" />
        Changed, not written
      </span>
      <span className="dec-key__item">No lamp: saved before this session</span>
    </p>
  );
}
