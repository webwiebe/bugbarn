package api

import (
	"io"
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/ingestresp"
)

// readBody reads the request body within the size limit. When it cannot, it
// returns the drop reason to answer with and ok false.
func (s *SpoolForwarder) readBody(r *http.Request) (body []byte, drop ingestresp.Drop, ok bool) {
	if r.Body == nil {
		return nil, ingestresp.Drop{}, true
	}
	var err error
	if s.maxBodyByte > 0 {
		body, err = io.ReadAll(io.LimitReader(r.Body, s.maxBodyByte+1))
		if err == nil && int64(len(body)) > s.maxBodyByte {
			return nil, ingestresp.DropTooLarge, false
		}
	} else {
		body, err = io.ReadAll(r.Body)
	}
	if err != nil {
		return nil, ingestresp.DropMalformed, false
	}
	return body, ingestresp.Drop{}, true
}

// forwardedHeaderValues copies the headers the writer needs from the request.
func forwardedHeaderValues(r *http.Request) map[string]string {
	headers := make(map[string]string, len(forwardedHeaders))
	for _, h := range forwardedHeaders {
		if v := r.Header.Get(h); v != "" {
			headers[h] = v
		}
	}
	return headers
}

// stampProjectSlug resolves a project-scoped API key to its project and stamps
// the slug onto the headers: only the header survives into queue.Item, and the
// consumer drops any item with an empty slug. The header, when present, wins, as
// the caller's explicit override. It answers the request and returns false when
// the lookup fails, so the client retries.
func (s *SpoolForwarder) stampProjectSlug(w http.ResponseWriter, r *http.Request, headers map[string]string) bool {
	if headers[projectHeader] != "" || s.resolveSlug == nil {
		return true
	}
	slug, err := s.resolveSlug(r.Context(), headers[apiKeyHeader])
	if err != nil {
		s.logger.Warn("spool forward: project lookup failed, asking the client to retry", "error", err)
		ingestresp.WriteDropped(w, ingestresp.DropUnavailable)
		return false
	}
	if slug != "" {
		headers[projectHeader] = slug
	}
	return true
}
