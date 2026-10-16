-- Settings Interlock keeps for itself, such as the node ID it generated.
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
