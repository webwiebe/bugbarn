package api

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestClientGone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"canceled", context.Canceled, true},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped canceled", fmt.Errorf("insert web session: %w", context.Canceled), true},
		{"real failure", errors.New("disk on fire"), false},
		{"nil", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := clientGone(tc.err); got != tc.want {
				t.Errorf("clientGone(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
