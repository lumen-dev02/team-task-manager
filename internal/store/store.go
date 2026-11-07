// Package store contains SQL and project-level authorization in one place.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lumen-dev02/team-task-manager/internal/domain"
)

type Store struct{ DB *pgxpool.Pool }

func New(db *pgxpool.Pool) *Store { return &Store{DB: db} }

func normalize(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return domain.ErrConflict
	}
	return err
}

func (s *Store) CreateUser(ctx context.Context, email, name, hash string) (domain.User, error) {
	var u domain.User
	err := s.DB.QueryRow(ctx, `INSERT INTO users(email, name, password_hash) VALUES ($1,$2,$3) RETURNING id,email,name,created_at`, email, name, hash).
		Scan(&u.ID, &u.Email, &u.Name, &u.CreatedAt)
	return u, normalize(err)
}

func (s *Store) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	var u domain.User
	err := s.DB.QueryRow(ctx, `SELECT id,email,name,password_hash,created_at FROM users WHERE email=$1`, email).
		Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.CreatedAt)
	return u, normalize(err)
}

func (s *Store) User(ctx context.Context, id int64) (domain.User, error) {
	var u domain.User
	err := s.DB.QueryRow(ctx, `SELECT id,email,name,created_at FROM users WHERE id=$1`, id).
		Scan(&u.ID, &u.Email, &u.Name, &u.CreatedAt)
	return u, normalize(err)
}

// Every project operation takes the same row lock, so membership checks and the
// subsequent SQL see a consistent ACL. This trades per-project concurrency for simplicity.
func (s *Store) inProject(ctx context.Context, projectID, userID int64, ownerOnly bool, fn func(pgx.Tx) error) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout = '5s'`); err != nil {
		return err
	}
	var owner int64
	err = tx.QueryRow(ctx, `SELECT owner_id FROM projects WHERE id=$1 FOR UPDATE`, projectID).Scan(&owner)
	if err != nil {
		return normalize(err)
	}
	var member bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_members WHERE project_id=$1 AND user_id=$2)`, projectID, userID).Scan(&member); err != nil {
		return err
	}
	// Conceal the existence of private projects from non-members.
	if !member {
		return domain.ErrNotFound
	}
	if ownerOnly && owner != userID {
		return domain.ErrForbidden
	}
	if err = fn(tx); err != nil {
		return normalize(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit project operation: %w", err)
	}
	return nil
}
