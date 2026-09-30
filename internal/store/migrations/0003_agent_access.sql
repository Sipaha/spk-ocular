-- Agent access (P14): what the user granted local agents, per target, and
-- the journal of their calls.

-- A target with grants and the identity they were given for; observed: a
-- different identity a call saw (the grants are suspended until the user
-- confirms them again).
CREATE TABLE agent_targets (
    provider   TEXT    NOT NULL,
    target     TEXT    NOT NULL,
    title      TEXT    NOT NULL DEFAULT '',
    identity   TEXT    NOT NULL CHECK (identity <> ''),
    observed   TEXT    NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL, -- unix milliseconds
    PRIMARY KEY (provider, target)
);

-- One verb in one scope of a target. kinds: a JSON array of kind ids, NULL
-- for every kind. Objects outside namespaces (cluster) are only read.
CREATE TABLE agent_grants (
    id         INTEGER PRIMARY KEY,
    provider   TEXT    NOT NULL,
    target     TEXT    NOT NULL,
    scope_mode TEXT    NOT NULL CHECK (scope_mode IN ('one', 'all', 'cluster')),
    scope_name TEXT    NOT NULL DEFAULT '',
    verb       TEXT    NOT NULL,
    kinds      TEXT,
    no_confirm INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (provider, target) REFERENCES agent_targets (provider, target) ON DELETE CASCADE,
    CHECK (scope_mode <> 'cluster' OR verb = 'read'),
    CHECK ((scope_mode = 'one') = (scope_name <> ''))
);
CREATE INDEX agent_grants_by_target ON agent_grants (provider, target);

-- The journal: intents before writes, outcomes after, refusals, and reads
-- folded per minute (bucket) with a count. Never request bodies or values.
CREATE TABLE agent_audit (
    id          INTEGER PRIMARY KEY,
    at          INTEGER NOT NULL, -- unix milliseconds
    agent       TEXT    NOT NULL,
    method      TEXT    NOT NULL,
    provider    TEXT    NOT NULL DEFAULT '',
    target      TEXT    NOT NULL DEFAULT '',
    scope       TEXT    NOT NULL DEFAULT '',
    object      TEXT    NOT NULL DEFAULT '',
    verb        TEXT    NOT NULL DEFAULT '',
    destructive INTEGER NOT NULL DEFAULT 0,
    expect_hash TEXT    NOT NULL DEFAULT '',
    phase       TEXT    NOT NULL,
    outcome     TEXT    NOT NULL DEFAULT '',
    detail      TEXT    NOT NULL DEFAULT '',
    count       INTEGER NOT NULL DEFAULT 1,
    bucket      TEXT
);
CREATE UNIQUE INDEX agent_audit_by_bucket ON agent_audit (bucket) WHERE bucket IS NOT NULL;
