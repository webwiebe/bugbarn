package detect

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed defaults.json
var defaultsJSON []byte

// Defaults returns the built-in rules. They are validated by the tests, so a
// failure here is a build defect.
func Defaults() []Rule {
	var rules []Rule
	if err := json.Unmarshal(defaultsJSON, &rules); err != nil {
		panic(fmt.Sprintf("detect: defaults.json: %v", err))
	}
	return rules
}

// Merge overlays stored rules on the built-ins: a stored rule with a built-in
// ID replaces it (that is how a built-in gets tuned or disabled), any other
// stored rule is added. Built-ins keep their order; custom rules follow.
func Merge(builtins, stored []Rule) []Rule {
	byID := make(map[string]Rule, len(stored))
	for _, r := range stored {
		byID[r.ID] = r
	}
	out := make([]Rule, 0, len(builtins)+len(stored))
	seen := map[string]bool{}
	for _, b := range builtins {
		if r, ok := byID[b.ID]; ok {
			b = r
		}
		out = append(out, b)
		seen[b.ID] = true
	}
	for _, r := range stored {
		if !seen[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

// IsBuiltin reports whether id names a built-in rule.
func IsBuiltin(id string) bool {
	for _, r := range Defaults() {
		if r.ID == id {
			return true
		}
	}
	return false
}
