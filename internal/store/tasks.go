package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lumen-dev02/team-task-manager/internal/domain"
)

const taskColumns = `id,project_id,title,description,status,assignee_id,created_by,version,created_at,updated_at`

func scanTask(row pgx.Row) (domain.Task, error) {
	var t domain.Task
	err := row.Scan(&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.AssigneeID, &t.CreatedBy, &t.Version, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func checkAssignee(ctx context.Context, tx pgx.Tx, projectID int64, assignee *int64) error {
	if assignee == nil {
		return nil
	}
	var member bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_members WHERE project_id=$1 AND user_id=$2)`, projectID, *assignee).Scan(&member)
	if err != nil {
		return err
	}
	if !member {
		return domain.ErrInvalid
	}
	return nil
}

func (s *Store) CreateTask(ctx context.Context, projectID, userID int64, in domain.TaskInput) (domain.Task, error) {
	var t domain.Task
	err := s.inProject(ctx, projectID, userID, false, func(tx pgx.Tx) error {
		if err := checkAssignee(ctx, tx, projectID, in.AssigneeID); err != nil {
			return err
		}
		var err error
		t, err = scanTask(tx.QueryRow(ctx, `INSERT INTO tasks(project_id,title,description,status,assignee_id,created_by) VALUES ($1,$2,$3,'todo',$4,$5) RETURNING `+taskColumns, projectID, in.Title, in.Description, in.AssigneeID, userID))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO task_status_history(task_id,to_status,changed_by) VALUES ($1,'todo',$2)`, t.ID, userID)
		return err
	})
	return t, err
}

func (s *Store) Tasks(ctx context.Context, projectID, userID int64, f domain.TaskFilter) ([]domain.Task, error) {
	result := make([]domain.Task, 0)
	err := s.inProject(ctx, projectID, userID, false, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+taskColumns+` FROM tasks WHERE project_id=$1 AND ($2='' OR status=$2) AND ($3::bigint IS NULL OR assignee_id=$3) ORDER BY id LIMIT $4 OFFSET $5`, projectID, f.Status, f.AssigneeID, f.Limit, f.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTask(rows)
			if err != nil {
				return err
			}
			result = append(result, t)
		}
		return rows.Err()
	})
	return result, err
}

func (s *Store) Task(ctx context.Context, projectID, userID, taskID int64) (domain.Task, error) {
	var t domain.Task
	err := s.inProject(ctx, projectID, userID, false, func(tx pgx.Tx) error {
		var err error
		t, err = scanTask(tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM tasks WHERE project_id=$1 AND id=$2`, projectID, taskID))
		return err
	})
	return t, err
}

func (s *Store) UpdateTask(ctx context.Context, projectID, userID, taskID int64, in domain.TaskInput) (domain.Task, error) {
	var t domain.Task
	err := s.inProject(ctx, projectID, userID, false, func(tx pgx.Tx) error {
		old, err := scanTask(tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM tasks WHERE project_id=$1 AND id=$2`, projectID, taskID))
		if err != nil {
			return err
		}
		if old.Version != in.Version {
			return domain.ErrConflict
		}
		if err = checkAssignee(ctx, tx, projectID, in.AssigneeID); err != nil {
			return err
		}
		t, err = scanTask(tx.QueryRow(ctx, `UPDATE tasks SET title=$3,description=$4,status=$5,assignee_id=$6,version=version+1,updated_at=now() WHERE project_id=$1 AND id=$2 RETURNING `+taskColumns, projectID, taskID, in.Title, in.Description, in.Status, in.AssigneeID))
		if err != nil {
			return err
		}
		if old.Status != in.Status {
			_, err = tx.Exec(ctx, `INSERT INTO task_status_history(task_id,from_status,to_status,changed_by) VALUES ($1,$2,$3,$4)`, taskID, old.Status, in.Status, userID)
		}
		return err
	})
	return t, err
}

func (s *Store) DeleteTask(ctx context.Context, projectID, userID, taskID int64) error {
	return s.inProject(ctx, projectID, userID, false, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1 AND id=$2`, projectID, taskID)
		if err == nil && tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return err
	})
}

func (s *Store) History(ctx context.Context, projectID, userID, taskID int64, limit, offset int) ([]domain.StatusChange, error) {
	result := make([]domain.StatusChange, 0)
	err := s.inProject(ctx, projectID, userID, false, func(tx pgx.Tx) error {
		var id int64
		if err := tx.QueryRow(ctx, `SELECT id FROM tasks WHERE project_id=$1 AND id=$2`, projectID, taskID).Scan(&id); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,from_status,to_status,changed_by,changed_at FROM task_status_history WHERE task_id=$1 ORDER BY id LIMIT $2 OFFSET $3`, taskID, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h domain.StatusChange
			if err = rows.Scan(&h.ID, &h.FromStatus, &h.ToStatus, &h.ChangedBy, &h.ChangedAt); err != nil {
				return err
			}
			result = append(result, h)
		}
		return rows.Err()
	})
	return result, err
}
