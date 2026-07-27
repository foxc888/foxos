package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

func (s *Store) SaveSubscription(ctx context.Context, item domain.Subscription) error {
	if item.ID == "" || strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.URL) == "" {
		return errors.New("subscription id, name and URL are required")
	}
	if item.Interval < 300 || item.Interval > 7*24*60*60 {
		return errors.New("subscription interval must be between 5 minutes and 7 days")
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO subscriptions(id,name,url,enabled,interval_seconds,last_digest,last_success_at,last_attempt_at,last_error,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,url=excluded.url,enabled=excluded.enabled,
		interval_seconds=excluded.interval_seconds,last_digest=excluded.last_digest,last_success_at=excluded.last_success_at,
		last_attempt_at=excluded.last_attempt_at,last_error=excluded.last_error,updated_at=excluded.updated_at
	`, item.ID, item.Name, item.URL, boolInt(item.Enabled), item.Interval, item.LastDigest, formatOptionalTime(item.LastSuccessAt), formatOptionalTime(item.LastAttemptAt), item.LastError, item.CreatedAt.Format(time.RFC3339Nano), item.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) Subscription(ctx context.Context, id string) (domain.Subscription, error) {
	return s.scanSubscription(s.db.QueryRowContext(ctx, `SELECT id,name,url,enabled,interval_seconds,last_digest,last_success_at,last_attempt_at,last_error,created_at,updated_at FROM subscriptions WHERE id=?`, id))
}

func (s *Store) SetSubscriptionEnabled(ctx context.Context, id string, enabled bool) (domain.Subscription, error) {
	if strings.TrimSpace(id) == "" {
		return domain.Subscription{}, ErrNotFound
	}
	result, err := s.db.ExecContext(ctx, `UPDATE subscriptions SET enabled=?,updated_at=? WHERE id=?`, boolInt(enabled), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return domain.Subscription{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return domain.Subscription{}, err
	}
	if count != 1 {
		return domain.Subscription{}, ErrNotFound
	}
	return s.Subscription(ctx, id)
}

func (s *Store) Subscriptions(ctx context.Context) ([]domain.Subscription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,url,enabled,interval_seconds,last_digest,last_success_at,last_attempt_at,last_error,created_at,updated_at FROM subscriptions ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Subscription, 0)
	for rows.Next() {
		item, err := s.scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeleteSubscription(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM subscriptions WHERE id=?`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SaveAlert(ctx context.Context, alert domain.Alert) error {
	if alert.ID == "" || alert.Key == "" || alert.Title == "" {
		return errors.New("alert id, key and title are required")
	}
	now := time.Now().UTC()
	if alert.FirstSeen.IsZero() {
		alert.FirstSeen = now
	}
	if alert.LastSeen.IsZero() {
		alert.LastSeen = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO alerts(id,alert_key,severity,title,message,acknowledged,first_seen,last_seen,resolved_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(alert_key) DO UPDATE SET severity=excluded.severity,title=excluded.title,message=excluded.message,
		first_seen=CASE WHEN alerts.resolved_at<>'' AND excluded.resolved_at='' THEN excluded.first_seen ELSE alerts.first_seen END,
		acknowledged=CASE WHEN alerts.resolved_at<>'' AND excluded.resolved_at='' THEN 0 ELSE alerts.acknowledged END,
		last_seen=excluded.last_seen,resolved_at=excluded.resolved_at
	`, alert.ID, alert.Key, alert.Severity, alert.Title, alert.Message, boolInt(alert.Acknowledged), alert.FirstSeen.Format(time.RFC3339Nano), alert.LastSeen.Format(time.RFC3339Nano), formatOptionalTime(alert.ResolvedAt))
	return err
}

func (s *Store) ResolveAlert(ctx context.Context, key string, resolvedAt time.Time) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("alert key is required")
	}
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	stamp := resolvedAt.UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `UPDATE alerts SET resolved_at=?,last_seen=? WHERE alert_key=? AND resolved_at=''`, stamp, stamp, key)
	return err
}

func (s *Store) TrackAlertSignal(ctx context.Context, key string, failing bool, observedAt time.Time) (int, error) {
	if strings.TrimSpace(key) == "" || len(key) > 256 {
		return 0, errors.New("alert signal key is invalid")
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	initial := 0
	if failing {
		initial = 1
	}
	var count int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO alert_signals(signal_key,failure_count,updated_at) VALUES(?,?,?)
		ON CONFLICT(signal_key) DO UPDATE SET failure_count=CASE WHEN ?=1 THEN alert_signals.failure_count+1 ELSE 0 END,updated_at=excluded.updated_at
		RETURNING failure_count
	`, key, initial, observedAt.UTC().Format(time.RFC3339Nano), boolInt(failing)).Scan(&count)
	return count, err
}

func (s *Store) AlertSignalKeys(ctx context.Context, prefix string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT signal_key FROM alert_signals WHERE signal_key LIKE ? ORDER BY signal_key`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		items = append(items, key)
	}
	return items, rows.Err()
}

func (s *Store) Alerts(ctx context.Context, includeResolved bool) ([]domain.Alert, error) {
	query := `SELECT id,alert_key,severity,title,message,acknowledged,first_seen,last_seen,resolved_at FROM alerts`
	if !includeResolved {
		query += ` WHERE resolved_at=''`
	}
	query += ` ORDER BY last_seen DESC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Alert, 0)
	for rows.Next() {
		var item domain.Alert
		var acknowledged int
		var first, last, resolved string
		if err := rows.Scan(&item.ID, &item.Key, &item.Severity, &item.Title, &item.Message, &acknowledged, &first, &last, &resolved); err != nil {
			return nil, err
		}
		item.Acknowledged = acknowledged != 0
		item.FirstSeen, _ = time.Parse(time.RFC3339Nano, first)
		item.LastSeen, _ = time.Parse(time.RFC3339Nano, last)
		item.ResolvedAt, _ = parseOptionalTime(resolved)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) AcknowledgeAlert(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE alerts SET acknowledged=1 WHERE id=?`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) scanSubscription(row interface{ Scan(...any) error }) (domain.Subscription, error) {
	var item domain.Subscription
	var enabled int
	var success, attempt, created, updated string
	if err := row.Scan(&item.ID, &item.Name, &item.URL, &enabled, &item.Interval, &item.LastDigest, &success, &attempt, &item.LastError, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Subscription{}, ErrNotFound
		}
		return domain.Subscription{}, err
	}
	item.Enabled = enabled != 0
	item.LastSuccessAt, _ = parseOptionalTime(success)
	item.LastAttemptAt, _ = parseOptionalTime(attempt)
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return item, nil
}

// UpdateSubscriptionResult keeps the old node set when a remote update fails.
func (s *Store) UpdateSubscriptionResult(ctx context.Context, id, digest, errorMessage string, success bool) error {
	item, err := s.Subscription(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	item.LastAttemptAt = now
	item.UpdatedAt = now
	if success {
		item.LastDigest = digest
		item.LastSuccessAt = now
		item.LastError = ""
	} else {
		item.LastError = errorMessage
	}
	return s.SaveSubscription(ctx, item)
}
