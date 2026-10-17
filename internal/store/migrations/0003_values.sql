-- Configuration values read from the segments of a node that hold event
-- IDs, so the entities built from them exist while the node is offline.
CREATE TABLE node_values (
    node_id          TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    software_version TEXT NOT NULL,
    values_json      TEXT NOT NULL,   -- {"space:address": "value", ...}
    read_at          TEXT NOT NULL
);
