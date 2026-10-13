package bugbarn

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckInURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://bugbarn.example/api/v1/events": "https://bugbarn.example/api/v1/monitors/sweep/check-ins",
		"/api/v1/events":                        "/api/v1/monitors/sweep/check-ins",
		"http://127.0.0.1:9/api/v1/events/":     "http://127.0.0.1:9/api/v1/monitors/sweep/check-ins",
	} {
		if got := checkInURL(in, "sweep"); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestCheckInSendsStatusTimingsAndHeaders(t *testing.T) {
	type got struct {
		path, key, project string
		body               map[string]string
	}
	seen := make(chan got, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		g := got{path: r.URL.Path, key: r.Header.Get("X-BugBarn-Api-Key"), project: r.Header.Get("X-BugBarn-Project")}
		_ = json.Unmarshal(raw, &g.body)
		seen <- g
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	Init(Options{APIKey: "k", Endpoint: srv.URL, ProjectSlug: "geo"})
	defer Shutdown(2 * time.Second)

	if !CheckIn("pending-sweep", CheckInError, CheckInInterval(time.Hour), CheckInGrace(10*time.Minute), CheckInMessage("db locked")) {
		t.Fatal("CheckIn returned false")
	}
	if !Flush(2 * time.Second) {
		t.Fatal("flush did not wait for the check-in")
	}
	select {
	case g := <-seen:
		if g.path != "/api/v1/monitors/pending-sweep/check-ins" || g.key != "k" || g.project != "geo" {
			t.Fatalf("request: %+v", g)
		}
		if g.body["status"] != "error" || g.body["interval"] != "1h0m0s" || g.body["grace"] != "10m0s" || g.body["message"] != "db locked" {
			t.Fatalf("body: %v", g.body)
		}
	default:
		t.Fatal("no request reached the server after Flush")
	}
}

func TestCheckInRejectsBadInput(t *testing.T) {
	Init(Options{APIKey: "k", Endpoint: "http://127.0.0.1:1"})
	defer Shutdown(time.Second)
	if CheckIn("", CheckInOK) || CheckIn("sweep", "late") {
		t.Fatal("bad input accepted")
	}
}

func TestCheckInBeforeInitReturnsFalse(t *testing.T) {
	Shutdown(time.Second)
	if CheckIn("sweep", CheckInOK) {
		t.Fatal("CheckIn succeeded without Init")
	}
}
