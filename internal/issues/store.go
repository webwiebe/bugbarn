package issues

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/fingerprint"
)

var (
	uuidPattern     = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
	ipv4Pattern     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	longNumber      = regexp.MustCompile(`\b\d{4,}\b`)
	hexAddress      = regexp.MustCompile(`(?i)\b0x[0-9a-f]{6,}\b`)
	whitespace      = regexp.MustCompile(`\s+`)
	pathNumber      = regexp.MustCompile(`/\d+`)
	trimPunctuation = regexp.MustCompile(`^[\s:;,_\-]+|[\s:;,_\-]+$`)
)

type Store struct {
	mu            sync.Mutex
	nextID        uint64
	byFingerprint map[string]*Issue
}

type Issue struct {
	ID                  string
	Fingerprint         string
	Title               string
	NormalizedTitle     string
	ExceptionType       string
	FirstSeen           time.Time
	LastSeen            time.Time
	EventCount          int
	RepresentativeEvent event.Event
	Events              []event.Event
}

func NewStore() *Store {
	return &Store{
		byFingerprint: make(map[string]*Issue),
	}
}

func (s *Store) Add(evt event.Event) *Issue {
	return s.AddWithFingerprint(evt, "")
}

func (s *Store) AddWithFingerprint(evt event.Event, providedFingerprint string) *Issue {
	key := strings.TrimSpace(providedFingerprint)
	if key == "" {
		key = fingerprint.Fingerprint(evt)
	}

	title, normalizedTitle, exceptionType := issueDetails(evt)

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.byFingerprint[key]; ok {
		existing.LastSeen = latest(existing.LastSeen, evt.ObservedAt, evt.ReceivedAt)
		existing.EventCount++
		existing.Events = append(existing.Events, evt)
		return existing
	}

	s.nextID++
	seenAt := issueSeenAt(evt)
	issue := &Issue{
		ID:                  issueID(s.nextID),
		Fingerprint:         key,
		Title:               title,
		NormalizedTitle:     normalizedTitle,
		ExceptionType:       exceptionType,
		FirstSeen:           seenAt,
		LastSeen:            seenAt,
		EventCount:          1,
		RepresentativeEvent: evt,
		Events:              []event.Event{evt},
	}
	s.byFingerprint[key] = issue
	return issue
}

func (s *Store) GetByFingerprint(fingerprint string) (*Issue, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	issue, ok := s.byFingerprint[strings.TrimSpace(fingerprint)]
	return issue, ok
}

func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.byFingerprint)
}

func issueSeenAt(evt event.Event) time.Time {
	if !evt.ObservedAt.IsZero() {
		return evt.ObservedAt
	}
	if !evt.ReceivedAt.IsZero() {
		return evt.ReceivedAt
	}
	return time.Now().UTC()
}

func latest(values ...time.Time) time.Time {
	var out time.Time
	for _, value := range values {
		if value.IsZero() {
			continue
		}
		if out.IsZero() || value.After(out) {
			out = value
		}
	}
	return out
}

func issueID(n uint64) string {
	return fmt.Sprintf("issue-%06d", n)
}

func issueDetails(evt event.Event) (title, normalizedTitle, exceptionType string) {
	exceptionType = strings.TrimSpace(evt.Exception.Type)
	message := strings.TrimSpace(evt.Exception.Message)
	if message == "" {
		message = strings.TrimSpace(evt.Message)
	}

	switch {
	case exceptionType != "" && message != "":
		title = exceptionType + ": " + message
	case exceptionType != "":
		title = exceptionType
	default:
		title = message
	}

	normalizedTitle = normalizeTitle(title)
	return title, normalizedTitle, exceptionType
}

func normalizeTitle(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = uuidPattern.ReplaceAllString(value, "<id>")
	value = ipv4Pattern.ReplaceAllString(value, "<ip>")
	value = hexAddress.ReplaceAllString(value, "<hex>")
	value = longNumber.ReplaceAllString(value, "<num>")
	value = pathNumber.ReplaceAllString(value, "/:num")
	value = whitespace.ReplaceAllString(value, " ")
	value = trimPunctuation.ReplaceAllString(value, "")
	return value
}
