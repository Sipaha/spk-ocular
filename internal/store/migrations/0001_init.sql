-- App-wide UI preferences (flat key/value).
CREATE TABLE ui_prefs (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Per-target state (last scope and view, column layout, ...), keyed by
-- provider + target id. Values are JSON owned by the caller.
CREATE TABLE target_state (
    provider TEXT NOT NULL,
    target   TEXT NOT NULL,
    key      TEXT NOT NULL,
    value    TEXT NOT NULL,
    PRIMARY KEY (provider, target, key)
);
