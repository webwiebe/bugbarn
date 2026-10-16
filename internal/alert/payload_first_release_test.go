package alert

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func TestGenericPayloadCarriesFirstRelease(t *testing.T) {
	t.Parallel()
	d := &Deliverer{}
	rule := Rule{Name: "new", Condition: "new_issue"}
	base := domain.Issue{ID: "BB-1", Title: "boom", FirstSeen: time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)}

	for _, tc := range []struct {
		release string
		want    bool
	}{{"1.4.0", true}, {"", false}} {
		issue := base
		issue.FirstRelease = tc.release
		raw, err := d.genericPayload(rule, issue, "http://example.com")
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Issue map[string]any `json:"issue"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		got, present := out.Issue["first_release"]
		if present != tc.want || (tc.want && got != tc.release) {
			t.Errorf("release %q: first_release = %v (present %v)", tc.release, got, present)
		}
	}
}
