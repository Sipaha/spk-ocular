-- Objects whose details were opened, per target (the palette's "recent").
-- Identity includes the UID: an object replaced under the same name is a new
-- entry; the old one opens as "no longer exists".
CREATE TABLE recent_objects (
    provider  TEXT    NOT NULL,
    target    TEXT    NOT NULL,
    kind      TEXT    NOT NULL,
    scope     TEXT    NOT NULL,
    name      TEXT    NOT NULL,
    uid       TEXT    NOT NULL,
    title     TEXT    NOT NULL,
    opened_at INTEGER NOT NULL, -- unix milliseconds
    PRIMARY KEY (provider, target, kind, scope, name, uid)
);
CREATE INDEX recent_objects_by_target ON recent_objects (provider, target, opened_at);
CREATE INDEX recent_objects_by_time ON recent_objects (opened_at);
