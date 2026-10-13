package alert

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

type captureNotifier struct {
	rule  Rule
	issue domain.Issue
}

func (c *captureNotifier) EmailConfigured() bool { return true }

func (c *captureNotifier) Fire(_ context.Context, r Rule, i domain.Issue, _ string) error {
	c.rule, c.issue = r, i
	return nil
}

func TestSilenceNotifier_MailsTheAdminAboutTheProject(t *testing.T) {
	t.Parallel()

	cap := &captureNotifier{}
	n := &SilenceNotifier{deliverer: cap, adminTo: "admin@example.com", publicURL: "http://x"}

	if err := n.NotifySilent(context.Background(), "demo", 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}

	if cap.rule.EmailTo != "admin@example.com" || cap.rule.Condition != "silence" || !cap.rule.isAdmin() {
		t.Errorf("unexpected rule %+v", cap.rule)
	}
	if cap.issue.ProjectSlug != "demo" || !strings.Contains(cap.issue.Title, "7 days") {
		t.Errorf("unexpected issue %+v", cap.issue)
	}
	if got := conditionLabel("silence"); got != "Project silent" {
		t.Errorf("label = %q", got)
	}
}
