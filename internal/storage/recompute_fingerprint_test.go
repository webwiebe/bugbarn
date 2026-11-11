package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/fingerprint"
)

func computedRow(t *testing.T, material string, evt event.Event) fingerprintIssueRow {
	t.Helper()
	sum := sha256.Sum256([]byte(material))
	body, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprintIssueRow{id: 7, projectID: 3, fingerprint: hex.EncodeToString(sum[:]), material: material, eventJSON: body}
}

// recomputeFingerprint updates only computed fingerprints whose current hash
// differs, and skips supplied ones, undecodable events and current rows.
func TestRecomputeFingerprint(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	evt := event.Event{
		ReceivedAt: at, ObservedAt: at, Severity: "ERROR", Message: "boom",
		Exception: event.Exception{Type: "panic", Message: "boom"},
	}

	t.Run("stale computed fingerprint is updated", func(t *testing.T) {
		u, ok := recomputeFingerprint(computedRow(t, "old material", evt))
		if !ok {
			t.Fatal("no update for a stale computed fingerprint")
		}
		if u.id != 7 || u.projectID != 3 || u.newFingerprint != fingerprint.Fingerprint(evt) || u.newMaterial == "" {
			t.Fatalf("update = %+v", u)
		}
	})

	t.Run("current fingerprint is left alone", func(t *testing.T) {
		snap := fingerprint.SnapshotFor(evt)
		if _, ok := recomputeFingerprint(computedRow(t, snap.Material, evt)); ok {
			t.Fatal("update for a fingerprint that is already current")
		}
	})

	t.Run("supplied fingerprint is left alone", func(t *testing.T) {
		row := computedRow(t, "old material", evt)
		row.fingerprint = "alertmanager:abc"
		if _, ok := recomputeFingerprint(row); ok {
			t.Fatal("update for a supplied fingerprint")
		}
	})

	t.Run("undecodable event is skipped", func(t *testing.T) {
		row := computedRow(t, "old material", evt)
		row.eventJSON = []byte("{not json")
		if _, ok := recomputeFingerprint(row); ok {
			t.Fatal("update for an undecodable event")
		}
	})
}
