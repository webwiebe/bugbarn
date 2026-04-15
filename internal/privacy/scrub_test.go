package privacy

import "testing"

func TestScrubRedactsSensitiveKeysAndStringPatterns(t *testing.T) {
	input := map[string]any{
		"authorization": "Bearer abcdefghijklmnopqrstuvwxyz",
		"user": map[string]any{
			"email": "user@example.com",
			"name":  "User at user@example.com from 192.168.1.10",
		},
		"message": "cart 550e8400-e29b-41d4-a716-446655440000 failed with token: abcdefghijkl",
	}

	got := Scrub(input).(map[string]any)
	if got["authorization"] != "[redacted]" {
		t.Fatalf("authorization was not redacted: %#v", got["authorization"])
	}

	user := got["user"].(map[string]any)
	if user["email"] != "[redacted]" {
		t.Fatalf("email key was not redacted: %#v", user["email"])
	}
	if user["name"] != "User at [redacted-email] from [redacted-ip]" {
		t.Fatalf("unexpected scrubbed name: %#v", user["name"])
	}
	if got["message"] != "cart [redacted-id] failed with [redacted-secret]" {
		t.Fatalf("unexpected scrubbed message: %#v", got["message"])
	}
}
