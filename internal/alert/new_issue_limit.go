package alert

import (
	"sync"
	"time"
)

const (
	// newIssueMailMax is how many new-issue mails one project may send per
	// window. A fingerprint gap that opens a new issue per event would
	// otherwise mail the admin once per event.
	newIssueMailMax = 5
	// newIssueMailWindow is the length of the counting window.
	newIssueMailWindow = 10 * time.Minute
)

// newIssueLimiter caps new-issue mails per project per window. Issues past the
// cap are counted so one summary mail can report them when the window ends.
type newIssueLimiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu       sync.Mutex
	projects map[string]*issueWindow
}

type issueWindow struct {
	start      time.Time
	sent       int
	suppressed int
}

func newNewIssueLimiter() *newIssueLimiter {
	return &newIssueLimiter{max: newIssueMailMax, window: newIssueMailWindow, now: time.Now, projects: map[string]*issueWindow{}}
}

// admission is the limiter's verdict for one new issue.
type admission struct {
	// Send is true when the issue's own mail may go out.
	Send bool
	// Summarize is the number of suppressed issues of an earlier window that
	// have not been reported yet. It is non-zero when a window rolled over.
	Summarize int
	// ScheduleSummary is true for the first suppression of a window: the caller
	// arms a timer for Remaining that calls Expire with WindowStart.
	ScheduleSummary bool
	Remaining       time.Duration
	WindowStart     time.Time
}

// admit records a new issue for slug and returns what to do with it.
func (l *newIssueLimiter) admit(slug string) admission {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	var a admission
	w := l.projects[slug]
	if w != nil && now.Sub(w.start) >= l.window {
		a.Summarize = w.suppressed
		w = nil
	}
	if w == nil {
		w = &issueWindow{start: now}
		l.projects[slug] = w
	}
	a.WindowStart = w.start
	a.Remaining = w.start.Add(l.window).Sub(now)
	if w.sent < l.max {
		w.sent++
		a.Send = true
		return a
	}
	w.suppressed++
	a.ScheduleSummary = w.suppressed == 1
	return a
}

// expire ends the window that began at start, if it is still current, and
// returns how many issues it suppressed. It returns 0 when a later admit already
// rolled the window over and reported the count.
func (l *newIssueLimiter) expire(slug string, start time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	w := l.projects[slug]
	if w == nil || !w.start.Equal(start) {
		return 0
	}
	delete(l.projects, slug)
	return w.suppressed
}
