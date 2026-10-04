package privacy

import "testing"

func TestScrubWithKeepIPs(t *testing.T) {
	in := map[string]any{
		"msg":      "Failed password from 203.0.113.9 for ops@example.com",
		"password": "hunter2",
	}
	def := Scrub(in).(map[string]any)
	if def["msg"] != "Failed password from [redacted-ip] for [redacted-email]" {
		t.Fatalf("default scrub = %q", def["msg"])
	}
	keep := ScrubWith(in, Options{KeepIPs: true}).(map[string]any)
	if keep["msg"] != "Failed password from 203.0.113.9 for [redacted-email]" {
		t.Fatalf("KeepIPs scrub = %q", keep["msg"])
	}
	if keep["password"] != "[redacted]" {
		t.Fatalf("KeepIPs dropped key redaction: %q", keep["password"])
	}
}
