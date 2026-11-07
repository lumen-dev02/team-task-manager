// Package migrations applies the initial schema atomically. Migration files are immutable.
package migrations

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed 001_init.sql
var schema string

func Up(ctx context.Context, db *pgxpool.Pool) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(731852091)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INT PRIMARY KEY, checksum TEXT NOT NULL)`); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(schema))
	checksum := hex.EncodeToString(sum[:])
	var existing string
	err = tx.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version = 1`).Scan(&existing)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err = tx.Exec(ctx, schema); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations VALUES (1, $1)`, checksum); err != nil {
			return err
		}
	case err != nil:
		return err
	case existing != checksum:
		return fmt.Errorf("migration 1 checksum mismatch: applied migrations must not be edited")
	}
	return tx.Commit(ctx)
}
