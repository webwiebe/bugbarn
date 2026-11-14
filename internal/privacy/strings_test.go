package privacy

import (
	"reflect"
	"testing"
)

func TestScrubStringRedactsEveryPattern(t *testing.T) {
	in := "mail ops@example.com from 10.0.0.1 id 550e8400-e29b-41d4-a716-446655440000 secret: abcdefghijkl1234"
	want := "mail [redacted-email] from [redacted-ip] id [redacted-id] [redacted-secret]"
	if got := ScrubString(in); got != want {
		t.Fatalf("ScrubString = %q, want %q", got, want)
	}
}

func TestScrubWithWalksSlicesAndKeepsScalars(t *testing.T) {
	in := map[string]any{
		"items": []any{"ping 192.0.2.4", map[string]any{"api-key": "x"}, 7, true, nil},
	}
	got := ScrubWith(in, Options{}).(map[string]any)
	want := []any{"ping [redacted-ip]", map[string]any{"api-key": "[redacted]"}, 7, true, nil}
	if !reflect.DeepEqual(got["items"], want) {
		t.Fatalf("items = %#v, want %#v", got["items"], want)
	}
}

func TestScrubDoesNotMutateItsInput(t *testing.T) {
	in := map[string]any{"password": "hunter2", "note": "ip 192.0.2.4"}
	_ = Scrub(in)
	if in["password"] != "hunter2" || in["note"] != "ip 192.0.2.4" {
		t.Fatalf("input was modified: %v", in)
	}
}

func TestSensitiveKeyMatchingIgnoresCaseAndHyphens(t *testing.T) {
	for _, key := range []string{"X-Api-Key", "Set-Cookie", "SESSION_ID", "csrf-token", "Passwd"} {
		got := Scrub(map[string]any{key: "v"}).(map[string]any)
		if got[key] != "[redacted]" {
			t.Errorf("key %q was not redacted: %v", key, got[key])
		}
	}
	got := Scrub(map[string]any{"username": "v"}).(map[string]any)
	if got["username"] != "v" {
		t.Errorf("non-sensitive key was redacted: %v", got["username"])
	}
}
