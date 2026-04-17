package alert

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Create the minimal schema required by the repository.
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS projects (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	slug TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`)
	if err != nil {
		t.Fatalf("create projects table: %v", err)
	}

	_, err = db.Exec(`INSERT INTO projects (slug, name) VALUES ('default', 'Default')`)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS alerts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	severity TEXT NOT NULL DEFAULT '',
	rule_json TEXT NOT NULL DEFAULT '{}',
	webhook_url TEXT NOT NULL DEFAULT '',
	condition TEXT NOT NULL DEFAULT 'new_issue',
	threshold INTEGER NOT NULL DEFAULT 0,
	cooldown_minutes INTEGER NOT NULL DEFAULT 15,
	last_fired_at TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`)
	if err != nil {
		t.Fatalf("create alerts table: %v", err)
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS alert_firings (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	alert_id INTEGER NOT NULL,
	issue_id INTEGER NOT NULL,
	fired_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`)
	if err != nil {
		t.Fatalf("create alert_firings table: %v", err)
	}

	return db
}

func TestSQLiteRepository_RecordAndLastFiring(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	repo := NewSQLiteRepository(db)
	ctx := context.Background()

	// No firings yet → zero time.
	last, err := repo.LastFiring(ctx, "alert-000001", "issue-000001")
	if err != nil {
		t.Fatalf("LastFiring (empty): %v", err)
	}
	if !last.IsZero() {
		t.Errorf("expected zero time before any firing, got %v", last)
	}

	before := time.Now().UTC().Truncate(time.Second)
	if err := repo.RecordFiring(ctx, "alert-000001", "issue-000001"); err != nil {
		t.Fatalf("RecordFiring: %v", err)
	}
	after := time.Now().UTC().Add(time.Second)

	last, err = repo.LastFiring(ctx, "alert-000001", "issue-000001")
	if err != nil {
		t.Fatalf("LastFiring: %v", err)
	}
	if last.IsZero() {
		t.Fatal("expected non-zero time after firing")
	}
	if last.Before(before) || last.After(after) {
		t.Errorf("last firing time %v out of expected range [%v, %v]", last, before, after)
	}
}

func TestSQLiteRepository_ListForProject(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	repo := NewSQLiteRepository(db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, `
INSERT INTO alerts (project_id, name, enabled, webhook_url, condition, threshold, cooldown_minutes)
VALUES (1, 'Alert One', 1, 'https://example.com/hook', 'new_issue', 0, 15)`)
	if err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	rules, err := repo.ListForProject(ctx, 1)
	if err != nil {
		t.Fatalf("ListForProject: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	r := rules[0]
	if r.Name != "Alert One" {
		t.Errorf("expected name 'Alert One', got %q", r.Name)
	}
	if !r.Enabled {
		t.Error("expected rule to be enabled")
	}
	if r.WebhookURL != "https://example.com/hook" {
		t.Errorf("expected webhook_url, got %q", r.WebhookURL)
	}
	if r.Condition != "new_issue" {
		t.Errorf("expected condition 'new_issue', got %q", r.Condition)
	}
	if r.CooldownMinutes != 15 {
		t.Errorf("expected cooldown 15, got %d", r.CooldownMinutes)
	}
}

func TestSQLiteRepository_UpdateLastFired(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	repo := NewSQLiteRepository(db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, `
INSERT INTO alerts (id, project_id, name, enabled, webhook_url, condition)
VALUES (1, 1, 'Alert', 1, 'http://example.com', 'new_issue')`)
	if err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	firedAt := time.Now().UTC().Truncate(time.Second)
	if err := repo.UpdateLastFired(ctx, "alert-1", firedAt); err != nil {
		t.Fatalf("UpdateLastFired: %v", err)
	}

	var stored string
	if err := db.QueryRowContext(ctx, `SELECT last_fired_at FROM alerts WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatalf("select last_fired_at: %v", err)
	}
	if stored == "" {
		t.Error("expected last_fired_at to be set")
	}
}
