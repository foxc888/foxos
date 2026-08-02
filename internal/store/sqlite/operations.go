package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

var ErrReplayToken = errors.New("confirmation token already consumed")

func (s *Store) Ping(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return s.db.PingContext(ctx)
}

func (s *Store) SaveMihomoDraft(ctx context.Context, draft domain.MihomoDraft) error {
	if draft.ID == "" {
		draft.ID = "active"
	}
	if draft.Mode == "" {
		draft.Mode = "rule"
	}
	if draft.Mode != "rule" && draft.Mode != "global" && draft.Mode != "direct" {
		return errors.New("unsupported Mihomo mode")
	}
	if draft.MixedPort == 0 {
		draft.MixedPort = 7890
	}
	if draft.MixedPort < 0 || draft.MixedPort > 65535 {
		return errors.New("mixed port out of range")
	}
	if draft.Revision < 1 {
		draft.Revision = 1
	}
	rules, err := json.Marshal(draft.Rules)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if draft.UpdatedAt.IsZero() {
		draft.UpdatedAt = now
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO mihomo_drafts(id,mode,mixed_port,allow_lan,rules_json,revision,updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET mode=excluded.mode,mixed_port=excluded.mixed_port,
		allow_lan=excluded.allow_lan,rules_json=excluded.rules_json,
		revision=mihomo_drafts.revision+1,updated_at=excluded.updated_at
	`, draft.ID, draft.Mode, draft.MixedPort, boolInt(draft.AllowLAN), string(rules), draft.Revision, draft.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) MihomoDraft(ctx context.Context) (domain.MihomoDraft, error) {
	var draft domain.MihomoDraft
	var rules, updated string
	var allow int
	err := s.db.QueryRowContext(ctx, `SELECT id,mode,mixed_port,allow_lan,rules_json,revision,updated_at FROM mihomo_drafts WHERE id='active'`).Scan(
		&draft.ID, &draft.Mode, &draft.MixedPort, &allow, &rules, &draft.Revision, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.MihomoDraft{ID: "active", Mode: "rule", MixedPort: 7890, Rules: []string{"MATCH,DIRECT"}}, ErrNotFound
	}
	if err != nil {
		return domain.MihomoDraft{}, err
	}
	if err := json.Unmarshal([]byte(rules), &draft.Rules); err != nil {
		return domain.MihomoDraft{}, fmt.Errorf("decode Mihomo draft: %w", err)
	}
	draft.AllowLAN = allow != 0
	draft.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return draft, nil
}

func (s *Store) SaveMihomoSnapshot(ctx context.Context, snapshot domain.MihomoSnapshot) error {
	if snapshot.ID == "" || snapshot.Digest == "" || len(snapshot.Body) == 0 || len(snapshot.Body) > 16<<20 {
		return errors.New("invalid Mihomo snapshot")
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mihomo_snapshots(id,digest,label,body,created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET digest=excluded.digest,label=excluded.label,body=excluded.body
	`, snapshot.ID, snapshot.Digest, strings.TrimSpace(snapshot.Label), snapshot.Body, snapshot.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) MihomoSnapshot(ctx context.Context, id string) (domain.MihomoSnapshot, error) {
	var snapshot domain.MihomoSnapshot
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id,digest,label,body,created_at FROM mihomo_snapshots WHERE id=?`, id).Scan(
		&snapshot.ID, &snapshot.Digest, &snapshot.Label, &snapshot.Body, &created,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.MihomoSnapshot{}, ErrNotFound
	}
	if err != nil {
		return domain.MihomoSnapshot{}, err
	}
	snapshot.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return snapshot, nil
}

func (s *Store) MihomoSnapshots(ctx context.Context, limit int) ([]domain.MihomoSnapshot, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,digest,label,created_at FROM mihomo_snapshots ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.MihomoSnapshot, 0)
	for rows.Next() {
		var item domain.MihomoSnapshot
		var created string
		if err := rows.Scan(&item.ID, &item.Digest, &item.Label, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeleteMihomoSnapshot(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM mihomo_snapshots WHERE id=?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateJob(ctx context.Context, job domain.Job) (domain.Job, bool, error) {
	if job.ID == "" || strings.TrimSpace(job.Kind) == "" {
		return domain.Job{}, false, errors.New("job id and kind are required")
	}
	if job.Status == "" {
		job.Status = domain.JobQueued
	}
	if job.Progress < 0 || job.Progress > 100 {
		return domain.Job{}, false, errors.New("job progress out of range")
	}
	request, err := json.Marshal(job.Request)
	if err != nil {
		return domain.Job{}, false, err
	}
	result, err := json.Marshal(job.Result)
	if err != nil {
		return domain.Job{}, false, err
	}
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO jobs(id,kind,status,progress,idempotency_key,request_json,result_json,error_class,error_message,attempts,created_at,started_at,finished_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, job.ID, job.Kind, job.Status, job.Progress, job.IdempotencyKey, string(request), string(result), job.ErrorClass, job.ErrorMessage, job.Attempts,
		job.CreatedAt.Format(time.RFC3339Nano), formatOptionalTime(job.StartedAt), formatOptionalTime(job.FinishedAt), job.UpdatedAt.Format(time.RFC3339Nano))
	if err == nil {
		return job, false, nil
	}
	if job.IdempotencyKey == "" || !strings.Contains(strings.ToLower(err.Error()), "unique") {
		return domain.Job{}, false, err
	}
	existing, readErr := s.jobByIdempotency(ctx, job.Kind, job.IdempotencyKey)
	if readErr != nil {
		return domain.Job{}, false, err
	}
	return existing, true, nil
}

func (s *Store) jobByIdempotency(ctx context.Context, kind, key string) (domain.Job, error) {
	return s.scanJob(s.db.QueryRowContext(ctx, `SELECT id,kind,status,progress,idempotency_key,request_json,result_json,error_class,error_message,attempts,created_at,started_at,finished_at,updated_at FROM jobs WHERE kind=? AND idempotency_key=?`, kind, key))
}

func (s *Store) Job(ctx context.Context, id string) (domain.Job, error) {
	return s.scanJob(s.db.QueryRowContext(ctx, `SELECT id,kind,status,progress,idempotency_key,request_json,result_json,error_class,error_message,attempts,created_at,started_at,finished_at,updated_at FROM jobs WHERE id=?`, id))
}

func (s *Store) Jobs(ctx context.Context, limit int) ([]domain.Job, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,status,progress,idempotency_key,request_json,result_json,error_class,error_message,attempts,created_at,started_at,finished_at,updated_at FROM jobs ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Job, 0)
	for rows.Next() {
		item, err := s.scanJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) RecoverableJobs(ctx context.Context, kind string) ([]domain.Job, error) {
	if strings.TrimSpace(kind) == "" || kind != strings.TrimSpace(kind) {
		return nil, errors.New("recoverable job kind is required")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,kind,status,progress,idempotency_key,request_json,result_json,error_class,error_message,attempts,created_at,started_at,finished_at,updated_at
		FROM jobs
		WHERE kind=? AND status IN (?,?,?)
		ORDER BY created_at ASC, id ASC
	`, kind, domain.JobQueued, domain.JobRunning, domain.JobVerifying)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Job, 0)
	for rows.Next() {
		item, err := s.scanJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateJob(ctx context.Context, job domain.Job) error {
	request, err := json.Marshal(job.Request)
	if err != nil {
		return err
	}
	result, err := json.Marshal(job.Result)
	if err != nil {
		return err
	}
	job.UpdatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?,progress=?,request_json=?,result_json=?,error_class=?,error_message=?,attempts=?,started_at=?,finished_at=?,updated_at=? WHERE id=?`, job.Status, job.Progress, string(request), string(result), job.ErrorClass, job.ErrorMessage, job.Attempts, formatOptionalTime(job.StartedAt), formatOptionalTime(job.FinishedAt), job.UpdatedAt.Format(time.RFC3339Nano), job.ID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ConsumeReplay(ctx context.Context, digest string, expiresAt time.Time) error {
	if digest == "" || expiresAt.IsZero() {
		return errors.New("invalid replay record")
	}
	now := time.Now().UTC()
	_, _ = s.db.ExecContext(ctx, `DELETE FROM replay_tokens WHERE expires_at < ?`, now.Format(time.RFC3339Nano))
	_, err := s.db.ExecContext(ctx, `INSERT INTO replay_tokens(digest,expires_at,consumed_at) VALUES(?,?,?)`, digest, expiresAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrReplayToken
		}
		return err
	}
	return nil
}

func (s *Store) scanJob(row interface{ Scan(...any) error }) (domain.Job, error) {
	var job domain.Job
	var request, result, created, started, finished, updated string
	if err := row.Scan(&job.ID, &job.Kind, &job.Status, &job.Progress, &job.IdempotencyKey, &request, &result, &job.ErrorClass, &job.ErrorMessage, &job.Attempts, &created, &started, &finished, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Job{}, ErrNotFound
		}
		return domain.Job{}, err
	}
	if err := json.Unmarshal([]byte(request), &job.Request); err != nil {
		return domain.Job{}, fmt.Errorf("decode job request: %w", err)
	}
	if err := json.Unmarshal([]byte(result), &job.Result); err != nil {
		return domain.Job{}, fmt.Errorf("decode job result: %w", err)
	}
	job.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	job.StartedAt, _ = parseOptionalTime(started)
	job.FinishedAt, _ = parseOptionalTime(finished)
	job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return job, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}
