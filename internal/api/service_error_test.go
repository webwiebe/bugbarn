package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

func TestWriteServiceErrorMapsKindsToStatus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		err        error
		status     int
		retryAfter bool
	}{
		{"not found", apperr.NotFound("x", nil), http.StatusNotFound, false},
		{"conflict", apperr.Conflict("x", nil), http.StatusConflict, false},
		{"invalid input", apperr.InvalidInput("bad", nil), http.StatusBadRequest, false},
		{"unavailable", apperr.Unavailable("later", nil), http.StatusServiceUnavailable, true},
		{"busy", fmt.Errorf("list: %w", apperr.Busy("database busy", errors.New("locked"))), http.StatusServiceUnavailable, true},
		{"internal", apperr.Internal("x", nil), http.StatusInternalServerError, false},
		{"not an apperr", errors.New("raw"), http.StatusInternalServerError, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			writeServiceError(rec, tc.err)
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			if got := rec.Header().Get("Retry-After") != ""; got != tc.retryAfter {
				t.Errorf("Retry-After present = %v, want %v", got, tc.retryAfter)
			}
		})
	}
}
