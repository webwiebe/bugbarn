package sqlitedb

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests force SQLITE_READONLY_RECOVERY (264) the way a reader pod meets
// it (#261, BS2-161): a reader in another process holds the -shm file
// read-only, the wal-index header turns invalid, and only the writer can
// rebuild it. The reader runs as a child test process because a reader in the
// writer's process shares the writer's shm mapping and never sees the state.

const readerHelperEnv = "BUGBARN_READONLY_RECOVERY_DB"

// zeroWalIndexHeader overwrites both copies of the wal-index header at the start
// of the -shm file, the state a reader finds when the header needs recovery.
func zeroWalIndexHeader(t *testing.T, dbPath string) {
	t.Helper()
	f, err := os.OpenFile(dbPath+"-shm", os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open shm: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteAt(make([]byte, 136), 0); err != nil {
		t.Fatalf("zero shm header: %v", err)
	}
}

// TestReadonlyRecoveryReaderHelper is the child process: a reader pool on the
// read-only recovery driver, opened the way storage.OpenReadOnly opens it, plus
// readonly_shm=1, so its -shm is read-only as on a reader pod, whose volume is
// mounted read-only. Each stdin line is a command and gets one RESULT line back:
//
//	plain  one lookup on a connection without the retry driver
//	retry  one lookup through the retry driver; on the first 264 it prints
//	       RETRYING and waits for a "go" line before retrying
func TestReadonlyRecoveryReaderHelper(t *testing.T) {
	path := os.Getenv(readerHelperEnv)
	if path == "" {
		t.Skip("child process of TestReaderRetriesReadonlyRecovery")
	}
	dsn := readDSN(path) + "&readonly_shm=1"
	retrying, err := sql.Open(ReadOnlyDriverName(), dsn)
	if err != nil {
		fmt.Println("RESULT open", err)
		return
	}
	defer func() { _ = retrying.Close() }()
	// One connection per pool, so each read reuses the connection that was
	// open before the header was zeroed, as a long-lived reader pod does.
	retrying.SetMaxOpenConns(1)
	plain, err := sql.Open(BaseDriverName, dsn)
	if err != nil {
		fmt.Println("RESULT open plain", err)
		return
	}
	defer func() { _ = plain.Close() }()
	plain.SetMaxOpenConns(1)

	in := bufio.NewScanner(os.Stdin)
	readonlyRecoveryHook = func() {
		fmt.Println("RETRYING")
		in.Scan()
	}
	ctx := context.Background()
	for in.Scan() {
		switch in.Text() {
		case "plain":
			var id int64
			err := plain.QueryRowContext(ctx, `SELECT id FROM projects WHERE slug = ?`, "profotograaf").Scan(&id)
			fmt.Printf("RESULT recovery=%v err=%v\n", IsReadonlyRecovery(err), err)
		case "retry":
			var id int64
			err := retrying.QueryRowContext(ctx, `SELECT id FROM projects WHERE slug = ?`, "profotograaf").Scan(&id)
			fmt.Printf("RESULT recovery=%v slug=profotograaf err=%v\n", IsReadonlyRecovery(err), err)
		}
	}
}

type readerProcess struct {
	in  io.WriteCloser
	out *bufio.Scanner
}

func startReaderProcess(t *testing.T, path string) *readerProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestReadonlyRecoveryReaderHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), readerHelperEnv+"="+path)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start reader: %v", err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Wait() })
	return &readerProcess{in: in, out: bufio.NewScanner(out)}
}

func (r *readerProcess) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(r.in, line+"\n"); err != nil {
		t.Fatalf("write to reader: %v", err)
	}
}

// next returns the next RESULT or RETRYING line from the reader.
func (r *readerProcess) next(t *testing.T) string {
	t.Helper()
	for r.out.Scan() {
		line := r.out.Text()
		if strings.HasPrefix(line, "RESULT") || line == "RETRYING" {
			return line
		}
	}
	t.Fatalf("reader exited: %v", r.out.Err())
	return ""
}

// openReaderFixture opens a writer with one project and starts a reader that
// has read once, so its connections are open.
func openReaderFixture(t *testing.T) (*sql.DB, string, *readerProcess) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bugbarn.db")
	w := openWriter(t, path)
	if _, err := w.Exec(`INSERT INTO projects (slug, name, status, issue_prefix) VALUES ('profotograaf', 'P', 'active', 'P')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	r := startReaderProcess(t, path)
	for _, cmd := range []string{"plain", "retry"} {
		r.send(t, cmd)
		if got := r.next(t); !strings.Contains(got, "err=<nil>") {
			t.Fatalf("%s read before the header was zeroed: %s", cmd, got)
		}
	}
	return w, path, r
}

func TestReaderRetriesReadonlyRecovery(t *testing.T) {
	w, path, r := openReaderFixture(t)
	zeroWalIndexHeader(t, path)

	// Without the retry driver the reader fails, which proves the state is forced.
	r.send(t, "plain")
	if got := r.next(t); !strings.Contains(got, "recovery=true") {
		t.Fatalf("plain read with a zeroed header: want 264, got %s", got)
	}

	// The reader pool hits the same 264 and retries. The writer writes while it
	// waits, which rebuilds the header, and the retry then succeeds.
	r.send(t, "retry")
	if got := r.next(t); got != "RETRYING" {
		t.Fatalf("retry read with a zeroed header: want a retry, got %s", got)
	}
	if _, err := w.ExecContext(context.Background(),
		`INSERT INTO projects (slug, name, status, issue_prefix) VALUES ('after-swap', 'A', 'active', 'A')`); err != nil {
		t.Fatalf("writer write: %v", err)
	}
	r.send(t, "go")
	if got := r.next(t); !strings.Contains(got, "slug=profotograaf err=<nil>") {
		t.Fatalf("retry read after the writer wrote: want the project, got %s", got)
	}
}

func TestReaderReadonlyRecoveryThatPersistsIsUnavailable(t *testing.T) {
	_, path, r := openReaderFixture(t)
	zeroWalIndexHeader(t, path)

	// No writer write this time: every retry fails and the final error names
	// the retry count. storage maps it to Unavailable (503).
	r.send(t, "retry")
	if got := r.next(t); got != "RETRYING" {
		t.Fatalf("retry read with a zeroed header: want a retry, got %s", got)
	}
	r.send(t, "go")
	got := r.next(t)
	if !strings.Contains(got, "recovery=true") || !strings.Contains(got, "after 3 retries") {
		t.Fatalf("retry read with no writer write: want 264 after 3 retries, got %s", got)
	}
}
