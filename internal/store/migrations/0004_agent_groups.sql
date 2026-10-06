-- Legacy grant rows stay enabled and unchanged. New editors save named groups
-- atomically with their scope switches; NULL represents no extra configuration.
ALTER TABLE agent_targets ADD COLUMN groups_json TEXT CHECK (groups_json IS NULL OR json_valid(groups_json));
ALTER TABLE agent_targets ADD COLUMN disabled_scopes_json TEXT CHECK (disabled_scopes_json IS NULL OR json_valid(disabled_scopes_json));
