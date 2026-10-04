package telemetrydb

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func TestFixedNilIsUnavailable(t *testing.T) {
	if _, err := Fixed(nil).DB(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Fixed(nil).DB err = %v, want ErrUnavailable", err)
	}
	d := openTest(t, Security, 0)
	got, err := Fixed(d).DB(context.Background())
	if err != nil || got != d {
		t.Fatalf("Fixed(d).DB = %v, %v", got, err)
	}
}

func TestLazyOpensOnceTheWriterCreatedTheFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "security.db")
	lazy := NewLazy(Security, path)
	t.Cleanup(func() { _ = lazy.Close() })

	if _, err := lazy.DB(ctx); !errors.Is(err, ErrNotCreated) {
		t.Fatalf("before create: err = %v, want ErrNotCreated", err)
	}

	w, err := Open(ctx, Security, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })

	d, err := lazy.DB(ctx)
	if err != nil {
		t.Fatalf("after create: %v", err)
	}
	if d.Write() != nil {
		t.Fatal("lazy handle must be read-only")
	}
	again, err := lazy.DB(ctx)
	if err != nil || again != d {
		t.Fatalf("second DB call returned a different handle: %v", err)
	}
	if _, err := d.Read().ExecContext(ctx, `DELETE FROM security_logs`); err == nil {
		t.Fatal("write through the read-only handle succeeded")
	}
}

// The writer creates the file before it migrates it; a reader that opens in
// between must keep answering ErrNotCreated.
func TestOpenReadOnlyWithoutSchemaIsNotCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	raw, err := sql.Open(storage.DriverName(), writeDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE unrelated (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if _, err := OpenReadOnly(context.Background(), Metrics, path); !errors.Is(err, ErrNotCreated) {
		t.Fatalf("err = %v, want ErrNotCreated", err)
	}
}
