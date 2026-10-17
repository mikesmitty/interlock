-- Automation traces: one row per run, the full trace as JSON. Only the
-- latest few per automation are kept.
CREATE TABLE automation_traces (
    id            TEXT PRIMARY KEY,
    automation_id TEXT NOT NULL,
    started_at    TEXT NOT NULL,
    finished_at   TEXT,
    result        TEXT NOT NULL,
    trace_json    TEXT NOT NULL
);
CREATE INDEX automation_traces_by_automation ON automation_traces (automation_id, started_at DESC);
