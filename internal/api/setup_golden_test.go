package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

var generatedLine = regexp.MustCompile(`(?m)^Generated .*$`)

// The setup page is the text an integrating agent follows, so its bytes are the
// contract. The golden files hold the page for a pending project on the
// canonical host and on a bb.<domain> alias; the Generated timestamp is the only
// part masked. UPDATE_GOLDEN=1 rewrites them.
func TestServeSetupPageGolden(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	userAuth, err := auth.NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionManager("test-secret", time.Hour)
	server := NewServerWithAuth(nil, store, userAuth, sessions, nil, nil)
	server.SetSetupConfig("secret", "https://bugbarn.example.com")

	cases := []struct{ name, host, golden string }{
		{"canonical host", "bugbarn.example.com", "setup_page_canonical.golden"},
		{"vanity alias", "bb.customer.nl", "setup_page_vanity.golden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/golden-app", nil)
			req.Host = tc.host
			server.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
			}
			got := generatedLine.ReplaceAllString(rr.Body.String(), "Generated <time>")
			path := filepath.Join("testdata", tc.golden)
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("setup page differs from %s:\n%s", path, firstDiff(string(want), got))
			}
		})
	}
}

func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) && i < len(gl); i++ {
		if wl[i] != gl[i] {
			return "line " + strconv.Itoa(i+1) + "\n  want: " + wl[i] + "\n  got:  " + gl[i]
		}
	}
	return "length differs: want " + strconv.Itoa(len(wl)) + " lines, got " + strconv.Itoa(len(gl))
}
