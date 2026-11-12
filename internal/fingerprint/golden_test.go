package fingerprint

import (
	"encoding/json"
	"flag"
	"os"
	"sort"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/snapshots.golden.json")

// goldenCorpus covers every branch of SnapshotFor and flattenScalarFieldsInto.
// The golden file pins the fingerprint of each case: a change that moves any of
// them regroups production issues (#259, internal/fingerprint/CLAUDE.md).
func goldenCorpus() map[string]event.Event {
	frames := []event.StackFrame{
		{Module: "app/Billing", Function: "charge", File: `C:\src\billing\123\charge.go`},
		{Function: "run", File: "/srv/app/worker.js"},
		{},
		{Module: "  ", Function: "", File: ""},
	}
	return map[string]event.Event{
		"full": {
			Message: "ignored when the exception has one",
			Exception: event.Exception{Type: "TypeError", Stacktrace: frames,
				Message: "Cannot read 3f2504e0-4f89-11d3-9a0c-0305e82c3301 at https://shop.example.com/cart/42?x=1"},
			Resource:   map[string]any{"service.name": "api", "region": "eu-west-1", "environment": "prod"},
			Attributes: map[string]any{"http.route": "/users/:id", "http.status_code": float64(500), "http.method": "GET"},
		},
		"message-fallback": {Message: "Timeout after 30 seconds on 10.0.0.1"},
		"empty":            {},
		"raw-name-message": {RawScrubbed: map[string]any{"name": "UnhandledRejection",
			"properties": map[string]any{"message": "boom 12345", "source": "https://x.example/app.js"}}},
		"raw-source-only": {RawScrubbed: map[string]any{"name": "Error",
			"properties": map[string]any{"url": "https://x.example/page"}}},
		"raw-name-only": {RawScrubbed: map[string]any{"name": "SecurityError"}},
		"raw-ignored-with-stack": {Exception: event.Exception{Stacktrace: frames[:1]},
			RawScrubbed: map[string]any{"name": "Ignored"}},
		"scalars": {Exception: event.Exception{Type: "E"},
			Resource: map[string]any{
				"service": map[string]any{"name": "svc", "namespace": "ns"},
				"list":    []any{map[string]any{"status_code": 404}, "x", true, int64(7)},
			},
			Attributes: map[string]any{
				"route":       "/a/b",
				"status_code": json.Number("418"),
				"environment": true,
				"blank":       "",
				" ":           "spacey key",
				"nil":         nil,
				"other":       struct{}{},
				"int":         3,
				"float":       1.5,
			}},
		"attribute-overrides-resource": {Exception: event.Exception{Type: "E"},
			Resource:   map[string]any{"environment": "staging"},
			Attributes: map[string]any{"environment": "prod"}},
		"path-valued-service": {Exception: event.Exception{Type: "E"},
			Resource: map[string]any{"service.name": "scrape/lyon", "service.namespace": "https://h.example/p/q"}},
	}
}

func TestSnapshotGolden(t *testing.T) {
	got := map[string]map[string]any{}
	for name, evt := range goldenCorpus() {
		snap := SnapshotFor(evt)
		got[name] = map[string]any{"fingerprint": Fingerprint(evt), "material": snap.Material, "explanation": snap.Explanation}
	}
	out, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	const path = "testdata/snapshots.golden.json"
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(out) {
		names := make([]string, 0, len(got))
		for n := range got {
			names = append(names, n)
		}
		sort.Strings(names)
		t.Fatalf("fingerprints moved for the corpus %v: this regroups production issues; compare with testdata/snapshots.golden.json", names)
	}
}
