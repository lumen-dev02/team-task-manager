package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lumen-dev02/team-task-manager/internal/domain"
)

func (s *Store) CreateProject(ctx context.Context, userID int64, name, description string) (domain.Project, error) {
	var p domain.Project
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return p, err
	}
	defer tx.Rollback(context.Background())
	err = tx.QueryRow(ctx, `INSERT INTO projects(name,description,owner_id) VALUES ($1,$2,$3) RETURNING id,name,description,owner_id,created_at`, name, description, userID).
		Scan(&p.ID, &p.Name, &p.Description, &p.OwnerID, &p.CreatedAt)
	if err != nil {
		return p, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO project_members(project_id,user_id) VALUES ($1,$2)`, p.ID, userID); err != nil {
		return p, err
	}
	err = tx.Commit(ctx)
	return p, err
}

func (s *Store) Projects(ctx context.Context, userID int64, limit, offset int) ([]domain.Project, error) {
	rows, err := s.DB.Query(ctx, `SELECT p.id,p.name,p.description,p.owner_id,p.created_at FROM projects p JOIN project_members m ON m.project_id=p.id WHERE m.user_id=$1 ORDER BY p.id LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Project, 0)
	for rows.Next() {
		var p domain.Project
		if err = rows.Scan(&p.ID, &p.Name, &p.Description, &p.OwnerID, &p.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *Store) Project(ctx context.Context, projectID, userID int64) (domain.Project, error) {
	var p domain.Project
	err := s.inProject(ctx, projectID, userID, false, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,name,description,owner_id,created_at FROM projects WHERE id=$1`, projectID).
			Scan(&p.ID, &p.Name, &p.Description, &p.OwnerID, &p.CreatedAt)
	})
	return p, err
}

func (s *Store) DeleteProject(ctx context.Context, projectID, userID int64) error {
	return s.inProject(ctx, projectID, userID, true, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID)
		return err
	})
}

func (s *Store) Members(ctx context.Context, projectID, userID int64) ([]domain.Member, error) {
	result := make([]domain.Member, 0)
	err := s.inProject(ctx, projectID, userID, false, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT u.id,u.name,CASE WHEN u.id=p.owner_id THEN 'owner' ELSE 'member' END FROM project_members m JOIN users u ON u.id=m.user_id JOIN projects p ON p.id=m.project_id WHERE m.project_id=$1 ORDER BY u.id`, projectID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m domain.Member
			if err = rows.Scan(&m.UserID, &m.Name, &m.Role); err != nil {
				return err
			}
			result = append(result, m)
		}
		return rows.Err()
	})
	return result, err
}

func (s *Store) AddMember(ctx context.Context, projectID, userID int64, email string) error {
	return s.inProject(ctx, projectID, userID, true, func(tx pgx.Tx) error {
		var id int64
		if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, email).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO project_members(project_id,user_id) VALUES ($1,$2)`, projectID, id)
		return err
	})
}

func (s *Store) RemoveMember(ctx context.Context, projectID, userID, memberID int64) error {
	return s.inProject(ctx, projectID, userID, true, func(tx pgx.Tx) error {
		if memberID == userID {
			return domain.ErrInvalid
		}
		// Preserve tasks and invalidate stale client versions when unassigning them.
		if _, err := tx.Exec(ctx, `UPDATE tasks SET assignee_id=NULL,version=version+1,updated_at=now() WHERE project_id=$1 AND assignee_id=$2`, projectID, memberID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, memberID)
		if err == nil && tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return err
	})
}
