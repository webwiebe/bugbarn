package api

import "testing"

// TestSanitizeReturnTo guards the widget-reconnect redirect against open-redirect
// and header-injection: only plain same-origin "/…" paths survive; everything
// else collapses to "/".
func TestSanitizeReturnTo(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "/"},
		{"root", "/", "/"},
		{"app hash route", "/app/#/account", "/app/#/account"},
		{"path with query", "/app/?x=1#/account", "/app/?x=1#/account"},
		{"absolute http", "http://evil.example.com/", "/"},
		{"absolute https", "https://evil.example.com/x", "/"},
		{"protocol-relative", "//evil.example.com", "/"},
		{"scheme buried", "/redir?u=http://evil.example.com", "/"},
		{"not a path", "account", "/"},
		{"crlf injection", "/app\r\nSet-Cookie: x=1", "/"},
		{"newline", "/app\nfoo", "/"},
		{"over-long", "/" + string(make([]byte, 600)), "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeReturnTo(tc.in); got != tc.want {
				t.Errorf("sanitizeReturnTo(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
