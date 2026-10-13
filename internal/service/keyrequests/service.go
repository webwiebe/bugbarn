// Package keyrequests lets an agent ask for an ingest key for an active project
// and an admin approve or deny the request in the dashboard.
//
// The request row never holds a key. When the agent polls an approved request,
// the key is minted and stored (hash only) in the same transaction that marks
// the request claimed, so the plaintext exists in exactly one response.
package keyrequests

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

const (
	// RequestTTL is how long a request stays open, approved or not.
	RequestTTL = 24 * time.Hour
	// MaxPendingPerProject bounds how many open requests one project can hold.
	MaxPendingPerProject = 10
	maxLabelLen          = 100
	purgeAfter           = 7 * 24 * time.Hour
)

// Repository is the storage the service needs; *storage.KeyRequestStore satisfies it.
type Repository interface {
	CreateKeyRequest(ctx context.Context, kr domain.KeyRequest, pollSHA256 string) error
	CountPendingKeyRequests(ctx context.Context, projectID int64, now time.Time) (int, error)
	GetKeyRequest(ctx context.Context, id string) (domain.KeyRequest, string, error)
	ListPendingKeyRequests(ctx context.Context, now time.Time) ([]domain.KeyRequest, error)
	ResolveKeyRequest(ctx context.Context, id, status string, now time.Time) error
	ClaimKeyRequest(ctx context.Context, id, keyName, keySHA256 string, now time.Time) error
	PurgeKeyRequests(ctx context.Context, cutoff time.Time) error
}

type Service struct {
	repo   Repository
	logger *slog.Logger
	now    func() time.Time
}

func New(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger.With("service", "keyrequests"), now: time.Now}
}

// SetClock replaces the time source. Tests use it to step past the expiry.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Created is what the requester gets back: the id to poll and the one-time poll secret.
type Created struct {
	ID         string
	PollSecret string
	ExpiresAt  time.Time
}

// Create opens a pending request for an active project.
func (s *Service) Create(ctx context.Context, project domain.Project, label string) (Created, error) {
	label, ok := cleanLabel(label)
	if !ok {
		return Created{}, apperr.InvalidInput("label is required (1 to 100 printable characters, for example the repo and environment)", nil)
	}
	now := s.now().UTC()
	pending, err := s.repo.CountPendingKeyRequests(ctx, project.ID, now)
	if err != nil {
		return Created{}, err
	}
	if pending >= MaxPendingPerProject {
		return Created{}, apperr.Conflict("too many pending key requests for this project", nil)
	}
	id, err := randomHex(16)
	if err != nil {
		return Created{}, apperr.Internal("generate key request id", err)
	}
	poll, err := randomHex(32)
	if err != nil {
		return Created{}, apperr.Internal("generate poll secret", err)
	}
	kr := domain.KeyRequest{
		ID: id, ProjectID: project.ID, ProjectSlug: project.Slug, Label: label,
		Status: domain.KeyRequestPending, CreatedAt: now, ExpiresAt: now.Add(RequestTTL),
	}
	if err := s.repo.CreateKeyRequest(ctx, kr, sha256Hex(poll)); err != nil {
		s.logger.ErrorContext(ctx, "create key request", "project", project.Slug, "label", label, "error", err)
		return Created{}, err
	}
	s.logger.InfoContext(ctx, "key request created", "project", project.Slug, "label", label, "request_id", id)
	// Housekeeping rides on creation; a failure costs nothing but old rows.
	if err := s.repo.PurgeKeyRequests(ctx, now.Add(-purgeAfter)); err != nil {
		s.logger.WarnContext(ctx, "purge key requests", "error", err)
	}
	return Created{ID: id, PollSecret: poll, ExpiresAt: kr.ExpiresAt}, nil
}

// PollResult is the state of a request as the requester sees it. APIKey is set
// only on the one poll that claims the key.
type PollResult struct {
	Status string
	APIKey string
}

// Poll reports a request's state to the holder of its poll secret. A wrong
// secret, an unknown id or a slug that does not match answers NotFound, so the
// endpoint reveals nothing about other requests.
func (s *Service) Poll(ctx context.Context, slug, id, pollSecret string) (PollResult, error) {
	kr, pollSHA, err := s.repo.GetKeyRequest(ctx, id)
	if err != nil {
		return PollResult{}, err
	}
	if kr.ProjectSlug != slug || subtle.ConstantTimeCompare([]byte(sha256Hex(pollSecret)), []byte(pollSHA)) != 1 {
		return PollResult{}, apperr.NotFound("key request not found", nil)
	}
	now := s.now().UTC()
	if kr.Status != domain.KeyRequestClaimed && !now.Before(kr.ExpiresAt) {
		return PollResult{Status: domain.KeyRequestExpired}, nil
	}
	if kr.Status != domain.KeyRequestApproved {
		return PollResult{Status: kr.Status}, nil
	}

	key, err := randomHex(20)
	if err != nil {
		return PollResult{}, apperr.Internal("generate api key", err)
	}
	err = s.repo.ClaimKeyRequest(ctx, id, "agent: "+kr.Label, sha256Hex(key), now)
	switch {
	case err == nil:
		s.logger.InfoContext(ctx, "key request claimed", "project", kr.ProjectSlug, "label", kr.Label, "request_id", id)
		return PollResult{Status: domain.KeyRequestApproved, APIKey: key}, nil
	case errors.Is(err, apperr.ErrConflict):
		// Another poll claimed it first.
		return PollResult{Status: domain.KeyRequestClaimed}, nil
	default:
		s.logger.ErrorContext(ctx, "claim key request", "project", kr.ProjectSlug, "label", kr.Label, "request_id", id, "error", err)
		return PollResult{}, err
	}
}

// ListPending returns the open requests of every project for the dashboard.
func (s *Service) ListPending(ctx context.Context) ([]domain.KeyRequest, error) {
	return s.repo.ListPendingKeyRequests(ctx, s.now().UTC())
}

// Approve lets the requester claim a key. actor is the admin, for the log.
func (s *Service) Approve(ctx context.Context, id, actor string) error {
	return s.resolve(ctx, id, actor, domain.KeyRequestApproved, "key request approved")
}

// Deny closes the request without ever minting a key.
func (s *Service) Deny(ctx context.Context, id, actor string) error {
	return s.resolve(ctx, id, actor, domain.KeyRequestDenied, "key request denied")
}

func (s *Service) resolve(ctx context.Context, id, actor, status, msg string) error {
	kr, _, err := s.repo.GetKeyRequest(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.ResolveKeyRequest(ctx, id, status, s.now().UTC()); err != nil {
		if !errors.Is(err, apperr.ErrConflict) {
			s.logger.ErrorContext(ctx, "resolve key request", "request_id", id, "status", status, "error", err)
		}
		return err
	}
	s.logger.InfoContext(ctx, msg, "project", kr.ProjectSlug, "label", kr.Label, "request_id", id, "actor", actor)
	return nil
}

func cleanLabel(label string) (string, bool) {
	label = strings.TrimSpace(label)
	if label == "" || len(label) > maxLabelLen {
		return "", false
	}
	for _, r := range label {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return label, true
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
