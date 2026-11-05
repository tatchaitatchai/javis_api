package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestNewPoolHidesConnectionString(t *testing.T) {
	_, err := NewPool(context.Background(), "postgres://user:secret@host:notaport/db", PoolOptions{})
	if err == nil {
		t.Fatal("expected an error for an invalid DSN")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaks credentials: %v", err)
	}
}

func TestNewPoolIsLazy(t *testing.T) {
	// Nothing listens on port 1; creating the pool must still succeed.
	pool, err := NewPool(context.Background(), "postgres://user:pw@127.0.0.1:1/db", PoolOptions{MaxConns: 2})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()
	if pool.Config().MaxConns != 2 {
		t.Fatalf("MaxConns = %d, want 2", pool.Config().MaxConns)
	}
}

// fakeTx overrides only the methods WithTx uses; the embedded interface is nil.
type fakeTx struct {
	pgx.Tx
	commitErr             error
	committed, rolledBack bool
}

func (f *fakeTx) Commit(context.Context) error   { f.committed = true; return f.commitErr }
func (f *fakeTx) Rollback(context.Context) error { f.rolledBack = true; return nil }

type fakeBeginner struct {
	tx       *fakeTx
	beginErr error
}

func (f fakeBeginner) Begin(context.Context) (pgx.Tx, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return f.tx, nil
}

func TestWithTx(t *testing.T) {
	failure := errors.New("failure")
	tests := []struct {
		name          string
		fnErr         error
		commitErr     error
		wantErr       error
		wantCommitted bool
		wantRollback  bool
	}{
		{"commits on success", nil, nil, nil, true, false},
		{"rolls back on fn error", failure, nil, failure, false, true},
		{"rolls back on commit error", nil, failure, failure, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &fakeTx{commitErr: tt.commitErr}
			err := WithTx(context.Background(), fakeBeginner{tx: tx}, func(pgx.Tx) error { return tt.fnErr })
			if !errors.Is(err, tt.wantErr) || tx.committed != tt.wantCommitted || tx.rolledBack != tt.wantRollback {
				t.Fatalf("err=%v committed=%v rolledBack=%v", err, tx.committed, tx.rolledBack)
			}
		})
	}
}

func TestWithTxBeginError(t *testing.T) {
	failure := errors.New("begin failed")
	err := WithTx(context.Background(), fakeBeginner{beginErr: failure}, func(pgx.Tx) error {
		t.Fatal("fn must not run when Begin fails")
		return nil
	})
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v", err)
	}
}

func TestWithTxRollsBackOnPanic(t *testing.T) {
	tx := &fakeTx{}
	defer func() {
		if recover() == nil || !tx.rolledBack || tx.committed {
			t.Fatalf("panic not propagated or not rolled back: committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
		}
	}()
	_ = WithTx(context.Background(), fakeBeginner{tx: tx}, func(pgx.Tx) error { panic("boom") })
}
