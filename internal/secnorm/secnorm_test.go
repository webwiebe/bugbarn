package secnorm

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func parseOne(t *testing.T, line string) Record {
	t.Helper()
	recs, skipped := Parse([]byte(line), now)
	if skipped != 0 || len(recs) != 1 {
		t.Fatalf("Parse(%s) = %d records, %d skipped", line, len(recs), skipped)
	}
	return recs[0]
}

func TestTraefikAccessLog(t *testing.T) {
	line := `{"source":"traefik","host":"k3s1","timestamp":"2026-10-04T11:59:00Z","message":"{\"ClientHost\":\"172.70.1.1\",\"DownstreamStatus\":401,\"RequestMethod\":\"POST\",\"RequestHost\":\"bugbarn.wiebe.xyz\",\"RequestPath\":\"/api/v1/login\",\"RouterName\":\"bugbarn@kubernetes\",\"StartUTC\":\"2026-10-04T11:58:59.5Z\",\"request_Cf-Connecting-Ip\":\"203.0.113.9\"}"}`
	r := parseOne(t, line)
	if r.Source != "traefik" || r.Kind != "http" || r.Status != 401 || r.Method != "POST" || r.Path != "/api/v1/login" {
		t.Fatalf("got %+v", r)
	}
	if r.SrcIP != "203.0.113.9" {
		t.Fatalf("SrcIP = %q, want the Cf-Connecting-Ip client", r.SrcIP)
	}
	if !r.TS.Equal(time.Date(2026, 10, 4, 11, 58, 59, 5e8, time.UTC)) || r.Host != "k3s1" {
		t.Fatalf("ts/host = %v %q", r.TS, r.Host)
	}
	if got, _ := r.Field("status"); got != "401" {
		t.Fatalf("status field = %q", got)
	}
}

func TestTraefikDetectedWithoutHintAndForwardedFor(t *testing.T) {
	line := `{"message":"{\"ClientHost\":\"10.42.0.1\",\"DownstreamStatus\":200,\"RequestMethod\":\"GET\",\"RequestPath\":\"/\",\"request_X-Forwarded-For\":\"198.51.100.4, 10.0.0.1\"}"}`
	r := parseOne(t, line)
	if r.Source != "traefik" || r.SrcIP != "198.51.100.4" {
		t.Fatalf("got %+v", r)
	}
}

func TestTraefikDropsOwnTelemetryRequests(t *testing.T) {
	line := `{"source":"traefik","message":"{\"DownstreamStatus\":202,\"RequestMethod\":\"POST\",\"RequestPath\":\"/api/v1/telemetry/security\"}"}`
	if recs, _ := Parse([]byte(line), now); len(recs) != 0 {
		t.Fatalf("telemetry request was kept: %+v", recs)
	}
}

func TestK8sAudit(t *testing.T) {
	line := `{"source":"k8saudit","host":"layer7","message":"{\"kind\":\"Event\",\"auditID\":\"a1\",\"stage\":\"ResponseComplete\",\"requestURI\":\"/api/v1/namespaces/bugbarn-production/secrets/bugbarn-secrets\",\"verb\":\"get\",\"user\":{\"username\":\"system:serviceaccount:default:snoop\"},\"sourceIPs\":[\"10.42.0.7\"],\"objectRef\":{\"resource\":\"secrets\",\"namespace\":\"bugbarn-production\",\"name\":\"bugbarn-secrets\"},\"responseStatus\":{\"code\":200},\"stageTimestamp\":\"2026-10-04T11:00:00.000001Z\"}"}`
	r := parseOne(t, line)
	if r.Source != "k8saudit" || r.Kind != "secrets" || r.Action != "get" || r.User != "system:serviceaccount:default:snoop" || r.SrcIP != "10.42.0.7" || r.Status != 200 {
		t.Fatalf("got %+v", r)
	}
	if !strings.Contains(r.Message, "bugbarn-production/bugbarn-secrets") {
		t.Fatalf("message = %q", r.Message)
	}
}

func TestK8sAuditSkipsEarlyStages(t *testing.T) {
	line := `{"source":"k8saudit","message":"{\"auditID\":\"a1\",\"stage\":\"RequestReceived\",\"verb\":\"get\"}"}`
	if recs, _ := Parse([]byte(line), now); len(recs) != 0 {
		t.Fatalf("RequestReceived stage was kept: %+v", recs)
	}
}

func TestSSHMessages(t *testing.T) {
	cases := []struct {
		msg, kind, user, ip, action string
	}{
		{"Failed password for root from 192.0.2.10 port 51234 ssh2", "auth_failure", "root", "192.0.2.10", "password"},
		{"Failed password for invalid user admin from 192.0.2.11 port 1 ssh2", "invalid_user", "admin", "192.0.2.11", "password"},
		{"Invalid user oracle from 2001:db8::1 port 4242", "invalid_user", "oracle", "2001:db8::1", ""},
		{"Accepted publickey for wiebe from 192.0.2.12 port 2 ssh2: ED25519 SHA256:x", "auth_success", "wiebe", "192.0.2.12", "publickey"},
		{"error: maximum authentication attempts exceeded for root from 192.0.2.13 port 3 ssh2 [preauth]", "auth_failure", "root", "192.0.2.13", ""},
		{"Connection closed by authenticating user git 192.0.2.14 port 4 [preauth]", "preauth_disconnect", "git", "192.0.2.14", ""},
		{"error: PAM: authentication error for illegal user bob from 192.0.2.15", "auth_failure", "bob", "192.0.2.15", ""},
		{"Server listening on 0.0.0.0 port 22.", "other", "", "", ""},
	}
	for _, c := range cases {
		line := `{"source":"journald","host":"k3s1","SYSLOG_IDENTIFIER":"sshd","message":"` + c.msg + `"}`
		r := parseOne(t, line)
		if r.Source != "sshd" || r.Kind != c.kind || r.User != c.user || r.SrcIP != c.ip || r.Action != c.action {
			t.Errorf("%q: got kind=%q user=%q ip=%q action=%q", c.msg, r.Kind, r.User, r.SrcIP, r.Action)
		}
	}
}

func TestSudoMessages(t *testing.T) {
	cases := []struct{ msg, kind, user string }{
		{"   wiebe : TTY=pts/0 ; PWD=/home/wiebe ; USER=root ; COMMAND=/usr/bin/apt update", "sudo", "wiebe"},
		{"   mallory : 3 incorrect password attempts ; TTY=pts/1 ; PWD=/ ; USER=root ; COMMAND=/bin/sh", "sudo_failure", "mallory"},
		{"pam_unix(sudo:auth): authentication failure; logname= uid=1001 euid=0 tty=/dev/pts/1 ruser=eve rhost=  user=eve", "sudo_failure", "eve"},
		{"pam_unix(sudo:session): session opened for user root", "other", ""},
	}
	for _, c := range cases {
		line := `{"source":"journald","SYSLOG_IDENTIFIER":"sudo","message":"` + c.msg + `"}`
		r := parseOne(t, line)
		if r.Source != "sudo" || r.Kind != c.kind || r.User != c.user {
			t.Errorf("%q: got source=%q kind=%q user=%q", c.msg, r.Source, r.Kind, r.User)
		}
	}
}

func TestOtherJournaldLines(t *testing.T) {
	r := parseOne(t, `{"source":"journald","SYSLOG_IDENTIFIER":"kernel","message":"oom-kill"}`)
	if r.Source != "journald" || r.Kind != "other" || r.Action != "kernel" {
		t.Fatalf("got %+v", r)
	}
}

func TestMacOSLogStream(t *testing.T) {
	line := `{"source":"macos","host":"mini-1","message":"{\"eventMessage\":\"Invalid user test from 192.0.2.20 port 5000\",\"processImagePath\":\"/usr/sbin/sshd\",\"timestamp\":\"2026-10-04 13:00:00.123456+0200\"}"}`
	r := parseOne(t, line)
	if r.Source != "sshd" || r.Kind != "invalid_user" || r.SrcIP != "192.0.2.20" || r.Host != "mini-1" {
		t.Fatalf("got %+v", r)
	}
	other := parseOne(t, `{"message":"{\"eventMessage\":\"hello\",\"processImagePath\":\"/usr/libexec/foo\"}"}`)
	if other.Source != "macos" || other.Kind != "other" {
		t.Fatalf("got %+v", other)
	}
}

func TestCloudflareEvent(t *testing.T) {
	line := `{"source":"cloudflare","message":{"action":"block","clientIP":"203.0.113.50","clientRequestHTTPHost":"wiebe.xyz","clientRequestPath":"/wp-login.php","clientRequestHTTPMethodName":"POST","clientCountryName":"NL","datetime":"2026-10-04T10:00:00Z","source":"waf","ruleId":"abc","edgeResponseStatus":403}}`
	r := parseOne(t, line)
	if r.Source != "cloudflare" || r.Kind != "waf" || r.Action != "block" || r.SrcIP != "203.0.113.50" || r.Host != "wiebe.xyz" || r.Status != 403 {
		t.Fatalf("got %+v", r)
	}
}

func TestGenericAndMalformedLines(t *testing.T) {
	body := "{\"source\":\"custom\",\"message\":\"hi\"}\nnot json\n\n[broken\n"
	recs, skipped := Parse([]byte(body), now)
	if len(recs) != 1 || skipped != 2 {
		t.Fatalf("records=%d skipped=%d", len(recs), skipped)
	}
	if recs[0].Source != "custom" || recs[0].Kind != "other" || !recs[0].TS.Equal(now) {
		t.Fatalf("got %+v", recs[0])
	}
}

func TestJSONArrayBodyAndRawTruncation(t *testing.T) {
	big := strings.Repeat("a", MaxRawBytes+100)
	body := `[{"source":"custom","message":"` + big + `"}]`
	recs, _ := Parse([]byte(body), now)
	if len(recs) != 1 || len(recs[0].Raw) != MaxRawBytes {
		t.Fatalf("records=%d raw=%d", len(recs), len(recs[0].Raw))
	}
	if _, skipped := Parse([]byte(`[{"broken"`), now); skipped != 1 {
		t.Fatalf("broken array skipped=%d", skipped)
	}
}

func TestFieldNames(t *testing.T) {
	r := Record{Source: "s", Host: "h", Kind: "k", SrcIP: "1.2.3.4", User: "u", Action: "a", Method: "m", Path: "p", Message: "msg"}
	for name, want := range map[string]string{"source": "s", "host": "h", "kind": "k", "src_ip": "1.2.3.4", "user": "u", "action": "a", "method": "m", "path": "p", "message": "msg", "status": ""} {
		if got, ok := r.Field(name); !ok || got != want {
			t.Errorf("Field(%q) = %q, %v", name, got, ok)
		}
	}
	if _, ok := r.Field("nope"); ok {
		t.Error("unknown field reported ok")
	}
}

func TestClientIPRejectsHostnames(t *testing.T) {
	for in, want := range map[string]string{"1.2.3.4:80": "1.2.3.4", "[2001:db8::1]:443": "2001:db8::1", "example.com": "", "": ""} {
		if got := clientIP(in); got != want {
			t.Errorf("clientIP(%q) = %q, want %q", in, got, want)
		}
	}
}
