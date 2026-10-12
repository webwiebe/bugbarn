package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// frontendTracesBase is the endpoint the SPA hands to the SpanBarn SDK. The
	// SDK appends /api/v1/spans, which is the path served here.
	frontendTracesBase = "/api/v1/frontend-traces"
	frontendTracesPath = frontendTracesBase + "/api/v1/spans"
	// frontendTracesMaxBody bounds one batch of spans from a browser.
	frontendTracesMaxBody = 256 << 10
)

// frontendTracesProxy relays browser spans to SpanBarn under a key that only
// the server holds, so no API key ships in the page.
type frontendTracesProxy struct {
	spansURL string
	apiKey   string
	service  string
	client   *http.Client
}

// SetFrontendTraces enables the browser span relay. baseURL is the SpanBarn
// server, apiKey a project-scoped SpanBarn key and service the name the SPA
// reports under. An empty baseURL or key leaves the relay off.
func (s *Server) SetFrontendTraces(baseURL, apiKey, service string) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	if baseURL == "" || apiKey == "" {
		s.frontendTraces = nil
		return
	}
	s.frontendTraces = &frontendTracesProxy{
		spansURL: baseURL + "/api/v1/spans",
		apiKey:   apiKey,
		service:  service,
		client:   &http.Client{Timeout: 5 * time.Second},
	}
}

// serveFrontendTraces handles POST /api/v1/frontend-traces/api/v1/spans for an
// already-authenticated caller. It returns true when it wrote a response.
//
// The SDK posts with the session cookie and no CSRF header. That is safe here:
// the endpoint changes nothing in BugBarn, and the session cookie is
// SameSite=Strict, so another site cannot make the browser send it.
func (s *Server) serveFrontendTraces(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != frontendTracesPath {
		return false
	}
	if s.frontendTraces == nil {
		http.NotFound(w, r)
		return true
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, frontendTracesMaxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return true
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return true
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.frontendTraces.spansURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return true
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SpanBarn-Api-Key", s.frontendTraces.apiKey)

	resp, err := s.frontendTraces.client.Do(req)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return true
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return true
	}
	w.WriteHeader(http.StatusAccepted)
	return true
}
