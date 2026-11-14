package digest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/mail"
)

func sampleReport() Report {
	return Report{
		PeriodStart: "2026-11-08T09:00:00Z",
		PeriodEnd:   "2026-11-15T09:00:00Z",
		PublicURL:   "https://bugbarn.example",
		Projects: []ProjectSection{
			{
				Project: "api",
				Stats:   StatsBlock{TotalEvents: 120, NewIssues: 3, ResolvedIssues: 2, Regressions: 1},
				TopIssues: []IssueBlock{
					{ID: "A-1", Title: "nil pointer", EventCount: 90, Status: "open", URL: "https://bugbarn.example/app/#/issues/A-1"},
					{ID: "A-2", Title: "timeout", EventCount: 30, Status: "resolved"},
				},
			},
			{Project: "web", Stats: StatsBlock{TotalEvents: 5, NewIssues: 1}, TopIssues: []IssueBlock{}},
		},
	}
}

func TestRenderMailSubjectAndTotals(t *testing.T) {
	subject, plain, html, err := renderMail(sampleReport())
	if err != nil {
		t.Fatalf("renderMail: %v", err)
	}
	if subject != "BugBarn weekly digest: Nov 8 - Nov 15, 2026" {
		t.Errorf("subject = %q", subject)
	}
	for _, want := range []string{
		"BugBarn weekly digest (Nov 8 – Nov 15 2026 UTC)",
		"All projects: 125 events   4 new issues   2 resolved   1 regressions",
		"── api ──",
		"· nil pointer  (90 events, open) — https://bugbarn.example/app/#/issues/A-1",
		"· timeout  (30 events, resolved)\n",
		"── web ──",
		"View all issues: https://bugbarn.example",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain body missing %q\n%s", want, plain)
		}
	}
	for _, want := range []string{
		`<a href="https://bugbarn.example/app/#/issues/A-1" style="color:#0066cc">nil pointer</a>`,
		"<div style=\"font-size:24px;font-weight:bold\">125</div>",
		"View all issues →",
		"<h3 style=\"color:#1a1a1a;margin-top:28px;margin-bottom:4px;border-bottom:2px solid #eee;padding-bottom:6px\">web</h3>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html body missing %q", want)
		}
	}
	// An issue without a URL renders as plain text, not a link.
	if strings.Contains(html, ">timeout</a>") {
		t.Errorf("issue without a URL was rendered as a link")
	}
}

func TestRenderMailOmitsPublicLinkWhenUnset(t *testing.T) {
	r := sampleReport()
	r.PublicURL = ""
	_, plain, html, err := renderMail(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "View all issues") || strings.Contains(html, "View all issues") {
		t.Errorf("link rendered without a public URL")
	}
}

func TestEmailNotifier(t *testing.T) {
	n := &EmailNotifier{}
	if n.Name() != "email" {
		t.Fatalf("name = %q", n.Name())
	}
	// An inactive config sends nothing and reports success.
	if err := n.Send(context.Background(), sampleReport()); err != nil {
		t.Fatalf("inactive Send = %v, want nil", err)
	}
	// An active config pointing at a closed port fails with a mail.Error that
	// carries a class and no relay text.
	n.Cfg = mail.Config{Enabled: true, Host: "127.0.0.1", Port: 1, From: "a@example.com", To: "b@example.com"}
	err := n.Send(context.Background(), sampleReport())
	var mailErr *mail.Error
	if !errors.As(err, &mailErr) || mailErr.Class == "" {
		t.Fatalf("Send err = %v, want a *mail.Error with a class", err)
	}
}

func TestWebhookNotifierPostsReport(t *testing.T) {
	var got struct {
		Type     string           `json:"type"`
		Start    string           `json:"period_start"`
		Projects []ProjectSection `json:"projects"`
	}
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	n := &WebhookNotifier{URL: srv.URL}
	if n.Name() != "webhook" {
		t.Fatalf("name = %q", n.Name())
	}
	if err := n.Send(context.Background(), sampleReport()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if contentType != "application/json" || got.Type != "weekly_digest" || got.Start != "2026-11-08T09:00:00Z" || len(got.Projects) != 2 {
		t.Fatalf("payload = %+v, content type %q", got, contentType)
	}
}

func TestWebhookNotifierFailures(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()

	if err := (&WebhookNotifier{URL: bad.URL}).Send(context.Background(), sampleReport()); err == nil || !strings.Contains(err.Error(), "webhook returned 502") {
		t.Errorf("non-2xx err = %v", err)
	}
	if err := (&WebhookNotifier{URL: "http://127.0.0.1:1/hook"}).Send(context.Background(), sampleReport()); err == nil {
		t.Error("unreachable host should fail")
	}
	if err := (&WebhookNotifier{URL: "http://bad host/\x7f"}).Send(context.Background(), sampleReport()); err == nil {
		t.Error("invalid URL should fail")
	}
}

func TestWebhookHostStripsPathAndQuery(t *testing.T) {
	if got := webhookHost("https://hooks.example.com/secret/token?key=1"); got != "hooks.example.com" {
		t.Errorf("host = %q", got)
	}
	if got := webhookHost("://bad"); got != "" {
		t.Errorf("unparseable URL host = %q, want empty", got)
	}
}

type listFailStore struct{ *fakeStore }

func (listFailStore) ListProjects(context.Context) ([]domain.Project, error) {
	return nil, errors.New("db down")
}

func TestSendListProjectsFailure(t *testing.T) {
	notifier := &fakeNotifier{}
	errs := Send(context.Background(), nil, Config{}, listFailStore{&fakeStore{}}, []Notifier{notifier})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "list projects: db down") {
		t.Fatalf("errs = %v", errs)
	}
	if len(notifier.delivered()) != 0 {
		t.Fatal("nothing should be delivered when projects cannot be listed")
	}
}

type quietStore struct{ *fakeStore }

func (quietStore) WeeklyDigest(context.Context, int64, time.Time) (domain.DigestData, error) {
	return domain.DigestData{}, nil
}

func TestSendSkipsDeliveryWithoutActivity(t *testing.T) {
	notifier := &fakeNotifier{}
	store := quietStore{&fakeStore{projects: makeProjects(3)}}
	if errs := Send(context.Background(), nil, Config{}, store, []Notifier{notifier}); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(notifier.delivered()) != 0 {
		t.Fatal("a digest with no activity must not be delivered")
	}
}

func TestBuildSectionLinksIssuesOnlyWithPublicURL(t *testing.T) {
	data := domain.DigestData{
		TotalEvents: 4, NewIssues: 1, ResolvedIssues: 2, Regressions: 3,
		TopIssues: []domain.DigestIssue{{ID: "X-1", Title: "boom", EventCount: 4, Status: "open"}},
	}
	with := buildSection(Config{PublicURL: "https://b.example"}, "api", data)
	if with.Project != "api" || with.Stats != (StatsBlock{TotalEvents: 4, NewIssues: 1, ResolvedIssues: 2, Regressions: 3}) {
		t.Fatalf("section = %+v", with)
	}
	if len(with.TopIssues) != 1 || with.TopIssues[0].URL != "https://b.example/app/#/issues/X-1" {
		t.Fatalf("issues = %+v", with.TopIssues)
	}
	without := buildSection(Config{}, "api", data)
	if without.TopIssues[0].URL != "" {
		t.Fatalf("url = %q, want empty", without.TopIssues[0].URL)
	}
	empty := buildSection(Config{}, "api", domain.DigestData{})
	if empty.TopIssues == nil {
		t.Fatal("TopIssues must marshal as [] not null")
	}
}

func TestConfigEnabled(t *testing.T) {
	if (Config{}).Enabled() {
		t.Error("empty config is not enabled")
	}
	if !(Config{WebhookURL: "https://x"}).Enabled() {
		t.Error("webhook config is enabled")
	}
	if !(Config{Mail: mail.Config{Enabled: true, Host: "h", To: "t"}}).Enabled() {
		t.Error("mail config is enabled")
	}
}
