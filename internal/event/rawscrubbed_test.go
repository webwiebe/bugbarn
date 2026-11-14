package event

import "testing"

func TestRawScrubbedDetails(t *testing.T) {
	t.Parallel()
	if got := RawScrubbedDetails(nil); got != (RawDetails{}) {
		t.Fatalf("nil payload = %+v", got)
	}
	if got := RawScrubbedDetails(map[string]any{"name": "N"}); got.Name != "N" || got.Message != "" {
		t.Fatalf("no properties = %+v", got)
	}
	got := RawScrubbedDetails(map[string]any{"name": "N", "properties": map[string]any{"message": "M", "url": "U"}})
	if got.Name != "N" || got.Message != "M" || got.Source != "U" {
		t.Fatalf("url fallback = %+v", got)
	}
	got = RawScrubbedDetails(map[string]any{"properties": map[string]any{"source": "S", "url": "U"}})
	if got.Source != "S" {
		t.Fatalf("source wins over url: %+v", got)
	}
}
