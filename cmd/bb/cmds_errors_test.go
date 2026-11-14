package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// scripted serves a fixed status and body for every request and records the
// requests it saw. It stands in for failures the real API cannot produce on cue.
type scripted struct {
	*httptest.Server
	mu   sync.Mutex
	seen []*http.Request
}

func newScripted(t *testing.T, status int, body string) *scripted {
	t.Helper()
	s := &scripted{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.Clone(r.Context()))
		s.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func apiKeyConfig(url string) Config {
	return Config{URL: url, Auth: AuthConfig{Type: "apikey", APIKey: "k"}}
}

// deadURL is a listener address that was closed, so connecting is refused.
func deadURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return "http://" + addr
}

func TestHTTPErrorsMapToErrorsAndExitCode1(t *testing.T) {
	inEmptyDir(t)
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{401, "unauthorized\n", "HTTP 401: unauthorized"},
		{403, "forbidden: read-only key", "HTTP 403: forbidden: read-only key"},
		{404, "not found", "HTTP 404: not found"},
		{500, "boom", "HTTP 500: boom"},
		{503, "retry later", "HTTP 503: retry later"},
	}
	for _, tc := range cases {
		srv := newScripted(t, tc.status, tc.body)
		useConfig(t, apiKeyConfig(srv.URL))

		for name, run := range map[string]func() error{
			"issues":  func() error { _, err := runCmd(t, cmdIssues); return err },
			"issue":   func() error { _, err := runCmd(t, cmdIssue, "X-1"); return err },
			"events":  func() error { _, err := runCmd(t, cmdEvents, "X-1"); return err },
			"resolve": func() error { _, err := runCmd(t, cmdResolve, "X-1"); return err },
			"mute":    func() error { _, err := runCmd(t, cmdMute, "X-1"); return err },
			"logs":    func() error { _, err := runCmd(t, cmdLogs, "--no-color"); return err },
		} {
			if err := run(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s with %d: err = %v, want %q", name, tc.status, err, tc.want)
			}
		}

		var code int
		out := quiet(t, func() { code = runCommand([]string{"bb", "issue", "X-1"}) })
		if code != 1 || !strings.Contains(out, "error: ") || !strings.Contains(out, tc.want) {
			t.Errorf("exit with %d: code=%d output=%q", tc.status, code, out)
		}
	}
}

func TestLongErrorBodyIsTruncated(t *testing.T) {
	inEmptyDir(t)
	srv := newScripted(t, 500, strings.Repeat("x", 500))
	useConfig(t, apiKeyConfig(srv.URL))
	_, err := runCmd(t, cmdIssue, "X-1")
	if err == nil || len(err.Error()) > len("HTTP 500: ")+200 {
		t.Errorf("error is %d bytes: %v", len(err.Error()), err)
	}
}

func TestNetworkFailureMapsToRequestFailed(t *testing.T) {
	inEmptyDir(t)
	useConfig(t, apiKeyConfig(deadURL(t)))
	for name, run := range map[string]func() error{
		"issues": func() error { _, err := runCmd(t, cmdIssues); return err },
		"logs":   func() error { _, err := runCmd(t, cmdLogs); return err },
		"stream": func() error { _, err := runCmd(t, cmdLogs, "-f"); return err },
	} {
		err := run()
		if err == nil || (!strings.Contains(err.Error(), "request failed") && !strings.Contains(err.Error(), "stream connect")) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	var code int
	quiet(t, func() { code = runCommand([]string{"bb", "projects"}) })
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestMalformedSuccessBodiesAreReported(t *testing.T) {
	inEmptyDir(t)
	srv := newScripted(t, 200, "not json at all")
	useConfig(t, apiKeyConfig(srv.URL))

	// Plain commands print what the server sent, even when it is not JSON.
	out, err := runCmd(t, cmdIssue, "X-1")
	if err != nil || !strings.Contains(out, "not json at all") {
		t.Errorf("issue: %v %q", err, out)
	}
	if _, err := runCmd(t, cmdVibe, "X-1", "--print"); err == nil || !strings.Contains(err.Error(), "parse issue response") {
		t.Errorf("vibe: %v", err)
	}
	if _, err := runCmd(t, cmdIssues, "--all"); err == nil || !strings.Contains(err.Error(), "decode issues page") {
		t.Errorf("issues --all: %v", err)
	}
	if _, err := runCmd(t, cmdIssues, "--project", "p"); err == nil || !strings.Contains(err.Error(), "parse projects response") {
		t.Errorf("project validation: %v", err)
	}
	if _, err := runCmd(t, cmdIssues, "--group", "g"); err == nil || !strings.Contains(err.Error(), "parse groups response") {
		t.Errorf("group validation: %v", err)
	}
}

func TestMuteRequiresTheStatusItAskedFor(t *testing.T) {
	inEmptyDir(t)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"wrong status", `{"issue":{"status":"unresolved"}}`, `is "unresolved" after the request, expected "muted"`},
		{"no issue", `{}`, "unconfirmed"},
		{"unreadable", `nope`, "unreadable response"},
	}
	for _, tc := range cases {
		srv := newScripted(t, 200, tc.body)
		useConfig(t, apiKeyConfig(srv.URL))
		if _, err := runCmd(t, cmdMute, "X-1"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	queued := newScripted(t, 202, `{"queued":true}`)
	useConfig(t, apiKeyConfig(queued.URL))
	if _, err := runCmd(t, cmdMute, "X-1"); err != nil {
		t.Errorf("a queued mute must pass: %v", err)
	}
	if _, err := runCmd(t, cmdUnmute, "X-1"); err != nil {
		t.Errorf("a queued unmute must pass: %v", err)
	}
}

func TestRequestHeadersCarryAuthAndScope(t *testing.T) {
	inEmptyDir(t)
	srv := newScripted(t, 200, `{}`)

	cases := []struct {
		name    string
		cfg     Config
		scope   func(*Client)
		method  string
		check   func(t *testing.T, r *http.Request)
		hasBody bool
	}{
		{
			name: "api key and project", cfg: apiKeyConfig(srv.URL), method: "GET",
			scope: func(c *Client) { c.project = "p" },
			check: func(t *testing.T, r *http.Request) {
				if r.Header.Get("X-BugBarn-API-Key") != "k" || r.Header.Get("X-BugBarn-Project") != "p" {
					t.Errorf("headers = %v", r.Header)
				}
			},
		},
		{
			name: "group wins over project", cfg: apiKeyConfig(srv.URL), method: "GET",
			scope: func(c *Client) { c.project, c.group = "p", "g" },
			check: func(t *testing.T, r *http.Request) {
				if r.Header.Get("X-BugBarn-Group") != "g" || r.Header.Get("X-BugBarn-Project") != "" {
					t.Errorf("headers = %v", r.Header)
				}
			},
		},
		{
			name: "session GET has no csrf", method: "GET",
			cfg: Config{URL: srv.URL, Auth: AuthConfig{Type: "session", SessionToken: "s", CSRFToken: "c"}},
			check: func(t *testing.T, r *http.Request) {
				if ck, err := r.Cookie("bugbarn_session"); err != nil || ck.Value != "s" {
					t.Errorf("cookie = %v %v", ck, err)
				}
				if r.Header.Get("X-BugBarn-CSRF") != "" {
					t.Error("a GET must not send the CSRF token")
				}
			},
		},
		{
			name: "session POST sends csrf and json", method: "POST", hasBody: true,
			cfg: Config{URL: srv.URL, Auth: AuthConfig{Type: "session", SessionToken: "s", CSRFToken: "c"}},
			check: func(t *testing.T, r *http.Request) {
				if r.Header.Get("X-BugBarn-CSRF") != "c" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("headers = %v", r.Header)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useConfig(t, tc.cfg)
			c, err := newClient()
			if err != nil {
				t.Fatal(err)
			}
			if tc.scope != nil {
				tc.scope(c)
			}
			var body any
			if tc.hasBody {
				body = map[string]string{"a": "b"}
			}
			if _, err := c.do(tc.method, "/x", body); err != nil {
				t.Fatal(err)
			}
			srv.mu.Lock()
			last := srv.seen[len(srv.seen)-1]
			srv.mu.Unlock()
			tc.check(t, last)
		})
	}
}

func TestClientDoRejectsUnencodableBody(t *testing.T) {
	inEmptyDir(t)
	srv := newScripted(t, 200, `{}`)
	useConfig(t, apiKeyConfig(srv.URL))
	c, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.post("/x", make(chan int)); err == nil || !strings.Contains(err.Error(), "marshal request") {
		t.Errorf("err = %v", err)
	}
	if _, err := c.patch("/x", map[string]string{"a": "b"}); err != nil {
		t.Errorf("patch: %v", err)
	}
	if got := c.withProject("p"); got.project != "p" || got.group != "" || c.project != "" {
		t.Errorf("withProject must copy and clear the group: %+v", got)
	}
}
