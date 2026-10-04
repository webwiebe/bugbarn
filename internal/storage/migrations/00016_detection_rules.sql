-- +goose Up
-- Detection rule overrides and custom rules. The built-in rules ship in the
-- binary (internal/detect/defaults.json); a row here with a built-in id
-- replaces that rule, any other id adds a rule. rule_json is the detect.Rule
-- as the API accepted it, so a new rule field needs no migration.
CREATE TABLE IF NOT EXISTS detection_rules (
  id TEXT PRIMARY KEY,
  rule_json TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS detection_rules;
