-- Recovery metadata contains no credentials or configuration fingerprints.
CREATE TABLE owned_helpers (
 id TEXT PRIMARY KEY,
 provider TEXT NOT NULL,
 target TEXT NOT NULL,
 identity_json TEXT NOT NULL CHECK(json_valid(identity_json)),
 uid TEXT NOT NULL DEFAULT '',
 owner TEXT NOT NULL,
 lease_until INTEGER NOT NULL,
 next_try INTEGER NOT NULL,
 delay_ms INTEGER NOT NULL CHECK(delay_ms BETWEEN 60000 AND 86400000)
);
CREATE INDEX owned_helpers_due ON owned_helpers(provider,target,next_try,lease_until);
