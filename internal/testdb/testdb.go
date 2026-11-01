// Package testdb provides isolated, migrated PostgreSQL schemas for tests.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ahsyarif/promptify/internal/db"
)

// New creates a throwaway schema in TEST_DATABASE_URL, runs all migrations in
// it and drops it when the test ends. Tests are skipped if the variable is unset.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database test")
	}
	ctx := context.Background()

	b := make([]byte, 6)
	rand.Read(b)
	schema := "test_" + hex.EncodeToString(b)

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		conn, err := pgx.Connect(context.Background(), url)
		if err != nil {
			t.Errorf("cleanup connect: %v", err)
			return
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}
