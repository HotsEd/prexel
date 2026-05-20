package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned when a deployment lookup misses.
var ErrNotFound = errors.New("deploy: not found")

// Deployment represents one row in the `deployments` table.
type Deployment struct {
	ID           string  `json:"id"`
	AppID        string  `json:"app_id"`
	CommitSHA    *string `json:"commit_sha,omitempty"`
	CommitMsg    *string `json:"commit_msg,omitempty"`
	Branch       *string `json:"branch,omitempty"`
	ImageTag     *string `json:"image_tag,omitempty"`
	RollbackOf   *string `json:"rollback_of,omitempty"`
	Status       string  `json:"status"`
	LogPath      *string `json:"log_path,omitempty"`
	// ErrorMessage is the wrapped error chain captured by failDeploy
	// when status="failed". NULL for in-flight or successful deploys.
	// Used by the UI to surface the failure cause without parsing
	// text heuristics over the build log.
	ErrorMessage *string `json:"error_message,omitempty"`
	StartedAt    *int64  `json:"started_at,omitempty"`
	FinishedAt   *int64  `json:"finished_at,omitempty"`
	CreatedAt    int64   `json:"created_at"`
}

type repo struct{ db *sql.DB }

func newRepo(db *sql.DB) *repo { return &repo{db: db} }

func (r *repo) insert(ctx context.Context, d *Deployment) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO deployments(id, app_id, commit_sha, commit_msg, branch, image_tag,
		                         rollback_of, status, log_path, started_at, finished_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.AppID, d.CommitSHA, d.CommitMsg, d.Branch, d.ImageTag,
		d.RollbackOf, d.Status, d.LogPath, d.StartedAt, d.FinishedAt,
	)
	return err
}

func (r *repo) get(ctx context.Context, id string) (*Deployment, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, app_id, commit_sha, commit_msg, branch, image_tag, rollback_of,
		        status, log_path, error_message, started_at, finished_at, created_at
		 FROM deployments WHERE id = ?`, id)
	return scanDeployment(row)
}

func (r *repo) listByApp(ctx context.Context, appID string, limit int) ([]Deployment, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, app_id, commit_sha, commit_msg, branch, image_tag, rollback_of,
		        status, log_path, error_message, started_at, finished_at, created_at
		 FROM deployments
		 WHERE app_id = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// lastSuccess returns the most recent deployment with status='success' for the
// app. excludeID may be empty.
func (r *repo) lastSuccess(ctx context.Context, appID, excludeID string) (*Deployment, error) {
	q := `SELECT id, app_id, commit_sha, commit_msg, branch, image_tag, rollback_of,
	            status, log_path, error_message, started_at, finished_at, created_at
	     FROM deployments
	     WHERE app_id = ? AND status = 'success'`
	args := []any{appID}
	if excludeID != "" {
		q += ` AND id != ?`
		args = append(args, excludeID)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT 1`
	row := r.db.QueryRowContext(ctx, q, args...)
	return scanDeployment(row)
}

// updateFields runs a partial UPDATE.
func (r *repo) updateFields(ctx context.Context, id string, sets map[string]any) error {
	if len(sets) == 0 {
		return nil
	}
	cols := make([]string, 0, len(sets))
	args := make([]any, 0, len(sets)+1)
	for col, v := range sets {
		cols = append(cols, col+" = ?")
		args = append(args, v)
	}
	args = append(args, id)
	q := fmt.Sprintf(`UPDATE deployments SET %s WHERE id = ?`, strings.Join(cols, ", "))
	res, err := r.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// failPending marks every deployment in pending/building/deploying as failed —
// invoked at startup to reconcile crash-induced ghost rows.
func (r *repo) failPending(ctx context.Context) (int, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE deployments
		 SET status = 'failed', finished_at = unixepoch()
		 WHERE status IN ('pending', 'building', 'deploying')`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDeployment(s scanner) (*Deployment, error) {
	var d Deployment
	var (
		commit, msg, branch, image, rollback, logPath, errMsg sql.NullString
		started, finished                                     sql.NullInt64
	)
	err := s.Scan(&d.ID, &d.AppID, &commit, &msg, &branch, &image, &rollback,
		&d.Status, &logPath, &errMsg, &started, &finished, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if commit.Valid {
		d.CommitSHA = &commit.String
	}
	if msg.Valid {
		d.CommitMsg = &msg.String
	}
	if branch.Valid {
		d.Branch = &branch.String
	}
	if image.Valid {
		d.ImageTag = &image.String
	}
	if rollback.Valid {
		d.RollbackOf = &rollback.String
	}
	if logPath.Valid {
		d.LogPath = &logPath.String
	}
	if errMsg.Valid {
		d.ErrorMessage = &errMsg.String
	}
	if started.Valid {
		d.StartedAt = &started.Int64
	}
	if finished.Valid {
		d.FinishedAt = &finished.Int64
	}
	return &d, nil
}
