-- Nodes seen on the bus, so a restart shows them at once and offline nodes
-- keep their names.
CREATE TABLE nodes (
    id         TEXT PRIMARY KEY,   -- dotted hex node ID
    first_seen TEXT NOT NULL,
    last_seen  TEXT NOT NULL,
    snip       TEXT,               -- JSON, NULL until identified
    protocols  INTEGER NOT NULL DEFAULT 0
);

-- Configuration Description Information read from a node, keyed by the
-- software version it was read under so an upgrade triggers a fresh read.
CREATE TABLE node_cdi (
    node_id          TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    software_version TEXT NOT NULL,
    xml              BLOB NOT NULL,
    fetched_at       TEXT NOT NULL
);
