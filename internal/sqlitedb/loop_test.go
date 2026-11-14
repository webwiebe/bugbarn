package sqlitedb

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// chanHandler forwards every record's message to a channel, so a test waits on
// the loop's own output instead of sleeping.
type chanHandler struct{ msgs chan string }

func (h chanHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h chanHandler) Handle(_ context.Context, r slog.Record) error {
	select {
	case h.msgs <- r.Message:
	default:
	}
	return nil
}
func (h chanHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h chanHandler) WithGroup(string) slog.Handler      { return h }

func awaitMessage(t *testing.T, msgs <-chan string, want string) {
	t.Helper()
	timeout := time.After(30 * time.Second)
	for {
		select {
		case m := <-msgs:
			if m == want {
				return
			}
		case <-timeout:
			t.Fatalf("no %q log record within 30s", want)
		}
	}
}

func runLoop(t *testing.T, run func(ctx context.Context)) (cancel func()) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); run(ctx) }()
	return func() {
		stop()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Fatal("checkpoint loop did not return after cancel")
		}
	}
}

func TestRunCheckpointLoopTruncatesWALOnTick(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	db := openWriter(t, dbPath)
	growWAL(t, db)
	if walSize(t, dbPath) == 0 {
		t.Fatal("setup produced no WAL")
	}
	h := chanHandler{msgs: make(chan string, 64)}
	stop := runLoop(t, func(ctx context.Context) {
		RunCheckpointLoop(ctx, db, 5*time.Millisecond, slog.New(h))
	})
	awaitMessage(t, h.msgs, "wal checkpoint")
	stop()
	if got := walSize(t, dbPath); got != 0 {
		t.Fatalf("WAL size after a TRUNCATE tick = %d, want 0", got)
	}
}

func TestRunPolicyLoopDefaultsIntervalAndVacuumsPages(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	db := openWriter(t, dbPath)
	growWAL(t, db)
	// Interval 0 means the 60s default, so the loop must stop on cancel without
	// ever ticking.
	stop := runLoop(t, func(ctx context.Context) {
		RunPolicyLoop(ctx, db, 0, CheckpointPolicy{}, nil, 10)
	})
	stop()
}

func TestRunPolicyLoopPassiveTicksAndVacuum(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	db := openWriter(t, dbPath)
	growWAL(t, db)
	h := chanHandler{msgs: make(chan string, 64)}
	policy := CheckpointPolicy{TruncateEvery: 1000, TruncateBusyTimeout: time.Second, WALSize: WALFileSize(dbPath)}
	stop := runLoop(t, func(ctx context.Context) {
		RunPolicyLoop(ctx, db, 5*time.Millisecond, policy, slog.New(h), 16)
	})
	awaitMessage(t, h.msgs, "wal checkpoint")
	stop()
}

func TestCheckpointDBResults(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	db := openWriter(t, dbPath)
	growWAL(t, db)
	if frames := CheckpointDB(context.Background(), db, 0, quietLogger()); frames < 0 {
		t.Fatalf("CheckpointDB on a healthy database = %d", frames)
	}
	if got := walSize(t, dbPath); got != 0 {
		t.Fatalf("WAL size after TRUNCATE = %d, want 0", got)
	}
}

func TestCheckpointDBAndVacuumReportErrorsOnClosedDB(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	db := openWriter(t, dbPath)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	log, buf := capturingLogger()
	if got := CheckpointDB(context.Background(), db, 0, log); got != -1 {
		t.Fatalf("CheckpointDB on a closed database = %d, want -1", got)
	}
	IncrementalVacuum(context.Background(), db, 5, log)
	checkpointPassive(context.Background(), db, log)
	out := buf.String()
	for _, want := range []string{"wal checkpoint error", "incremental vacuum error"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}

func TestCheckpointPassiveStaysQuietWhenContextIsDone(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	db := openWriter(t, dbPath)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	log, buf := capturingLogger()
	checkpointPassive(ctx, db, log)
	IncrementalVacuum(ctx, db, 5, log)
	if buf.Len() != 0 {
		t.Fatalf("a canceled tick logged:\n%s", buf.String())
	}
}

func TestIncrementalVacuumShrinksIncrementalDatabase(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	db, err := sql.Open(DriverName(), writeDSN(dbPath)+"&_pragma=auto_vacuum(2)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`VACUUM`); err != nil { // applies auto_vacuum=INCREMENTAL
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE blobs (id INTEGER PRIMARY KEY, b BLOB)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := db.Exec(`INSERT INTO blobs (b) VALUES (zeroblob(65536))`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DELETE FROM blobs`); err != nil {
		t.Fatal(err)
	}
	var before, after int
	free := func() int {
		var n int
		if err := db.QueryRow(`PRAGMA freelist_count`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before = free()
	if before == 0 {
		t.Fatal("setup left no freelist pages")
	}
	IncrementalVacuum(context.Background(), db, before, quietLogger())
	after = free()
	if after >= before {
		t.Fatalf("freelist pages %d -> %d, want fewer", before, after)
	}
}

func TestBudgetedDriverOpensAndQueries(t *testing.T) {
	t.Parallel()
	name := BudgetedDriverName()
	if name == "" || name == BaseDriverName {
		t.Fatalf("BudgetedDriverName = %q, want a registered wrapper", name)
	}
	db, err := sql.Open(name, writeDSN(filepath.Join(t.TempDir(), "b.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var one int
	if err := db.QueryRow(`SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("SELECT 1 = %d, %v", one, err)
	}
	// A deadline that already expired is not an error trace; the query still fails.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err == nil {
		t.Fatal("query under an expired deadline succeeded")
	}
}

func TestReadonlyRetryConnForwardsEveryInterface(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	writer := openWriter(t, dbPath)
	growWAL(t, writer)

	reader := openReader(t, dbPath)
	ctx := context.Background()
	if err := reader.PingContext(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	stmt, err := reader.PrepareContext(ctx, `SELECT count(*) FROM projects WHERE id > ?`)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer stmt.Close()
	var n int
	if err := stmt.QueryRowContext(ctx, 0).Scan(&n); err != nil || n != 200 {
		t.Fatalf("prepared query = %d, %v", n, err)
	}
	if _, err := stmt.ExecContext(ctx, 0); err != nil {
		t.Fatalf("prepared exec: %v", err)
	}
	if _, err := reader.ExecContext(ctx, `PRAGMA query_only`); err != nil {
		t.Fatalf("exec: %v", err)
	}
	tx, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM projects`).Scan(&n); err != nil || n != 200 {
		t.Fatalf("tx query = %d, %v", n, err)
	}
	_ = tx.Rollback()
}

// The wrapper's own methods are reached through database/sql only when the
// otelsql layer forwards them, so drive them on the raw driver connection.
func TestReadonlyRetryConnMethodsOnRawConnection(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	growWAL(t, openWriter(t, dbPath))

	conn, err := readonlyRetryDriver{base: &sqlite.Driver{}}.Open(readDSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	c, ok := conn.(*readonlyRetryConn)
	if !ok {
		t.Fatalf("Open returned %T", conn)
	}
	if want := dbPath + "-wal"; c.walPath != want {
		t.Fatalf("walPath = %q, want %q", c.walPath, want)
	}
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := c.ResetSession(ctx); err != nil {
		t.Fatalf("ResetSession: %v", err)
	}
	if !c.IsValid() {
		t.Fatal("IsValid = false on an open connection")
	}
	if _, err := c.ExecContext(ctx, `PRAGMA query_only`, nil); err != nil {
		t.Fatalf("ExecContext: %v", err)
	}
	st, err := c.PrepareContext(ctx, `SELECT 1`)
	if err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	defer st.Close()
	rs, ok := st.(*readonlyRetryStmt)
	if !ok {
		t.Fatalf("PrepareContext returned %T", st)
	}
	if _, err := rs.ExecContext(ctx, nil); err != nil {
		t.Fatalf("stmt ExecContext: %v", err)
	}
	if _, err := c.PrepareContext(ctx, `SELECT nope FROM missing`); err == nil {
		t.Fatal("PrepareContext of invalid SQL succeeded")
	}
}

func TestSetReadonlyRecoveryHookInstallsAndClears(t *testing.T) {
	calls := 0
	SetReadonlyRecoveryHook(func() { calls++ })
	if readonlyRecoveryHook == nil {
		t.Fatal("hook not installed")
	}
	readonlyRecoveryHook()
	SetReadonlyRecoveryHook(nil)
	if readonlyRecoveryHook != nil || calls != 1 {
		t.Fatalf("hook after clear = %v, calls %d", readonlyRecoveryHook != nil, calls)
	}
}
