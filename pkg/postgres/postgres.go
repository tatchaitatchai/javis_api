// Package postgres builds pgx connection pools and runs transactions. Errors
// never include the connection string, because pgx errors may contain it.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolOptions bounds the pool. Zero values keep pgx defaults.
type PoolOptions struct {
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	ConnectTimeout  time.Duration
}

// NewPool parses dsn and creates a pool. Connections are opened lazily, so a
// database outage does not prevent the process from starting.
func NewPool(ctx context.Context, dsn string, options PoolOptions) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection configuration")
	}
	if options.MaxConns > 0 {
		config.MaxConns = options.MaxConns
	}
	if options.MinConns > 0 {
		config.MinConns = options.MinConns
	}
	if options.MaxConnLifetime > 0 {
		config.MaxConnLifetime = options.MaxConnLifetime
	}
	if options.ConnectTimeout > 0 {
		config.ConnConfig.ConnectTimeout = options.ConnectTimeout
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("could not initialize PostgreSQL connection pool")
	}
	return pool, nil
}

// TxBeginner is satisfied by *pgxpool.Pool, *pgx.Conn and pgx.Tx (savepoints).
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// WithTx runs fn in a transaction. It commits when fn returns nil and rolls
// back when fn returns an error or panics.
func WithTx(ctx context.Context, db TxBeginner, fn func(pgx.Tx) error) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			// Rollback after a failed Commit is a harmless no-op.
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(recovered)
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
