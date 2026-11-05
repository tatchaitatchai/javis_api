// Package testdb runs database tests against a real PostgreSQL container with
// the real migrations applied. `go test -short` skips them.
package testdb

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const image = "postgres:17"

var pool *pgxpool.Pool

// Run starts PostgreSQL, runs the package's tests and removes the container.
// Call it from TestMain and pass the result to os.Exit.
func Run(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		return m.Run()
	}
	// The container is removed below, so the reaper image is not needed.
	_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	ctx := context.Background()
	container, err := postgres.Run(ctx, image,
		postgres.WithDatabase("javis_test"),
		postgres.WithUsername("javis"),
		postgres.WithPassword("test-only"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "start PostgreSQL container:", err)
		return 1
	}
	defer func() { _ = container.Terminate(ctx) }()

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "read container address:", err)
		return 1
	}
	pool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		return 1
	}
	defer pool.Close()
	if err := migrate(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		return 1
	}
	return m.Run()
}

// Pool returns a connection pool on an empty schema.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("database test skipped with -short")
	}
	if err := truncateAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	return pool
}

// migrate applies the Up section of every migration in order, the same SQL
// goose runs, without pulling goose's driver dependencies into this module.
func migrate(ctx context.Context) error {
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	slices.Sort(files)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		_, afterUp, found := strings.Cut(string(content), "-- +goose Up")
		if !found {
			return fmt.Errorf("%s has no goose Up section", filepath.Base(file))
		}
		up, _, _ := strings.Cut(afterUp, "-- +goose Down")
		if _, err := conn.Conn().PgConn().Exec(ctx, up).ReadAll(); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(file), err)
		}
	}
	return nil
}

func truncateAll(ctx context.Context) error {
	rows, err := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'javis'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return err
		}
		tables = append(tables, "javis."+table)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(tables) == 0 {
		return nil
	}
	_, err = pool.Exec(ctx, "TRUNCATE "+strings.Join(tables, ", ")+" CASCADE")
	return err
}
