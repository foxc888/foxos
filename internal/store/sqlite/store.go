package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	_ "modernc.org/sqlite"
)

var ErrNotFound = domain.ErrNotFound

const currentSchemaVersion = 4

// CurrentSchemaVersion is the newest schema this binary can safely open.
// Upgrade recovery uses it before migrations run so an older binary never
// attempts to open a database migrated by a newer release.
func CurrentSchemaVersion() int { return currentSchemaVersion }

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if path != ":memory:" {
		if err := os.Chmod(path, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("secure database permissions: %w", err)
		}
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		PRAGMA foreign_keys = ON;
		PRAGMA busy_timeout = 5000;
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS proxy_groups (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS device_policies (
			id TEXT PRIMARY KEY,
			mac_address TEXT NOT NULL UNIQUE,
			static_ip TEXT NOT NULL UNIQUE,
			payload_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS audit_events (
			id TEXT PRIMARY KEY,
			action TEXT NOT NULL,
			target_id TEXT NOT NULL,
			outcome TEXT NOT NULL,
			details_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS audit_events_created_at ON audit_events(created_at DESC);
		INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, CURRENT_TIMESTAMP);
		CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			progress INTEGER NOT NULL DEFAULT 0,
			idempotency_key TEXT NOT NULL DEFAULT '',
			request_json TEXT NOT NULL DEFAULT '{}',
			result_json TEXT NOT NULL DEFAULT '{}',
			error_class TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT '',
			finished_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS jobs_updated_at ON jobs(updated_at DESC);
		CREATE UNIQUE INDEX IF NOT EXISTS jobs_idempotency_key ON jobs(kind, idempotency_key) WHERE idempotency_key <> '';
		CREATE TABLE IF NOT EXISTS mihomo_drafts (
			id TEXT PRIMARY KEY,
			mode TEXT NOT NULL,
			mixed_port INTEGER NOT NULL DEFAULT 0,
			allow_lan INTEGER NOT NULL DEFAULT 0,
			rules_json TEXT NOT NULL DEFAULT '[]',
			revision INTEGER NOT NULL DEFAULT 1,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS mihomo_snapshots (
			id TEXT PRIMARY KEY,
			digest TEXT NOT NULL UNIQUE,
			label TEXT NOT NULL DEFAULT '',
			body BLOB NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS mihomo_snapshots_created_at ON mihomo_snapshots(created_at DESC);
		CREATE TABLE IF NOT EXISTS replay_tokens (
			digest TEXT PRIMARY KEY,
			expires_at TEXT NOT NULL,
			consumed_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS subscriptions (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			url TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			interval_seconds INTEGER NOT NULL DEFAULT 21600,
			last_digest TEXT NOT NULL DEFAULT '',
			last_success_at TEXT NOT NULL DEFAULT '',
			last_attempt_at TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS alerts (
			id TEXT PRIMARY KEY,
			alert_key TEXT NOT NULL UNIQUE,
			severity TEXT NOT NULL,
			title TEXT NOT NULL,
			message TEXT NOT NULL,
			acknowledged INTEGER NOT NULL DEFAULT 0,
			first_seen TEXT NOT NULL,
			last_seen TEXT NOT NULL,
			resolved_at TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS alerts_last_seen ON alerts(last_seen DESC);
		INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(2, CURRENT_TIMESTAMP);
		CREATE TABLE IF NOT EXISTS device_profiles (
			mac_address TEXT PRIMARY KEY,
			alias TEXT NOT NULL DEFAULT '',
			tags_json TEXT NOT NULL DEFAULT '[]',
			vendor TEXT NOT NULL DEFAULT '',
			host_name TEXT NOT NULL DEFAULT '',
			ip_address TEXT NOT NULL DEFAULT '',
			interface_name TEXT NOT NULL DEFAULT '',
			dhcp_server TEXT NOT NULL DEFAULT '',
			first_seen TEXT NOT NULL,
			last_seen TEXT NOT NULL DEFAULT '',
			online INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS device_profiles_last_seen ON device_profiles(last_seen DESC);
		CREATE TABLE IF NOT EXISTS device_presence_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			mac_address TEXT NOT NULL REFERENCES device_profiles(mac_address) ON DELETE CASCADE,
			online INTEGER NOT NULL,
			observed_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS device_presence_events_device_time ON device_presence_events(mac_address, observed_at DESC);
		INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(3, CURRENT_TIMESTAMP);
		CREATE TABLE IF NOT EXISTS alert_signals (
			signal_key TEXT PRIMARY KEY,
			failure_count INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		);
		INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(4, CURRENT_TIMESTAMP);
	`)
	return err
}

func (s *Store) SaveNode(ctx context.Context, node domain.Node) error {
	if err := node.Validate(); err != nil {
		return err
	}
	return s.saveNodes(ctx, []domain.Node{node})
}

func (s *Store) SaveNodes(ctx context.Context, nodes []domain.Node) error {
	if len(nodes) == 0 {
		return errors.New("at least one node is required")
	}
	for _, node := range nodes {
		if err := node.Validate(); err != nil {
			return err
		}
	}
	return s.saveNodes(ctx, nodes)
}

func (s *Store) saveNodes(ctx context.Context, nodes []domain.Node) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, node := range nodes {
		body, err := json.Marshal(node) // #nosec G117 -- credentials are required runtime state in the mode-0600 local database; API output uses a redacted type.
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
		INSERT INTO nodes(id,name,type,payload_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,type=excluded.type,payload_json=excluded.payload_json,updated_at=excluded.updated_at
	`, node.ID, node.Name, node.Type, string(body), now, now)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Node(ctx context.Context, id string) (domain.Node, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM nodes WHERE id=?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Node{}, ErrNotFound
	}
	if err != nil {
		return domain.Node{}, err
	}
	var node domain.Node
	if err := json.Unmarshal([]byte(payload), &node); err != nil {
		return domain.Node{}, fmt.Errorf("decode node: %w", err)
	}
	return node, nil
}

func (s *Store) Nodes(ctx context.Context) ([]domain.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM nodes ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := make([]domain.Node, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var node domain.Node
		if err := json.Unmarshal([]byte(payload), &node); err != nil {
			return nil, fmt.Errorf("decode node: %w", err)
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (s *Store) DeleteNode(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id=?`, id)
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

func (s *Store) SaveGroup(ctx context.Context, group domain.Group) error {
	if err := group.Validate(); err != nil {
		return err
	}
	if err := s.validateGroupReferences(ctx, group); err != nil {
		return err
	}
	body, err := json.Marshal(group)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO proxy_groups(id,name,type,payload_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,type=excluded.type,payload_json=excluded.payload_json,updated_at=excluded.updated_at
	`, group.ID, group.Name, group.Type, string(body), now, now)
	return err
}

func (s *Store) validateGroupReferences(ctx context.Context, candidate domain.Group) error {
	nodes, err := s.Nodes(ctx)
	if err != nil {
		return fmt.Errorf("read proxy nodes: %w", err)
	}
	groups, err := s.Groups(ctx)
	if err != nil {
		return fmt.Errorf("read proxy groups: %w", err)
	}
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		nodeIDs[node.ID] = struct{}{}
	}
	byID := make(map[string]domain.Group, len(groups)+1)
	for _, group := range groups {
		byID[group.ID] = group
	}
	byID[candidate.ID] = candidate

	state := make(map[string]uint8, len(byID))
	var visit func(string) error
	visit = func(id string) error {
		group, exists := byID[id]
		if !exists {
			return fmt.Errorf("%w: unknown proxy group %q", domain.ErrInvalidGroup, id)
		}
		if state[id] == 1 {
			return fmt.Errorf("%w: proxy group reference cycle at %q", domain.ErrInvalidGroup, id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, nodeID := range group.NodeIDs {
			if _, exists := nodeIDs[nodeID]; !exists {
				return fmt.Errorf("%w: unknown proxy node %q", domain.ErrInvalidGroup, nodeID)
			}
		}
		for _, groupID := range group.GroupIDs {
			if err := visit(groupID); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	return visit(candidate.ID)
}

func (s *Store) Group(ctx context.Context, id string) (domain.Group, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM proxy_groups WHERE id=?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Group{}, ErrNotFound
	}
	if err != nil {
		return domain.Group{}, err
	}
	var group domain.Group
	if err := json.Unmarshal([]byte(payload), &group); err != nil {
		return domain.Group{}, fmt.Errorf("decode group: %w", err)
	}
	return group, nil
}

func (s *Store) Groups(ctx context.Context) ([]domain.Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM proxy_groups ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]domain.Group, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var group domain.Group
		if err := json.Unmarshal([]byte(payload), &group); err != nil {
			return nil, fmt.Errorf("decode group: %w", err)
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM proxy_groups WHERE id=?`, id)
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

func (s *Store) SaveDevicePolicy(ctx context.Context, policy domain.DevicePolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO device_policies(id,mac_address,static_ip,payload_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			mac_address=excluded.mac_address,static_ip=excluded.static_ip,payload_json=excluded.payload_json,updated_at=excluded.updated_at
	`, policy.ID, normalizeStoreMAC(policy.MACAddress), policy.StaticIP, string(body), now, now)
	return err
}
func (s *Store) DevicePolicy(ctx context.Context, id string) (domain.DevicePolicy, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM device_policies WHERE id=?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DevicePolicy{}, ErrNotFound
	}
	if err != nil {
		return domain.DevicePolicy{}, err
	}
	var policy domain.DevicePolicy
	if err := json.Unmarshal([]byte(payload), &policy); err != nil {
		return domain.DevicePolicy{}, fmt.Errorf("decode device policy: %w", err)
	}
	return policy, nil
}
func (s *Store) DevicePolicies(ctx context.Context) ([]domain.DevicePolicy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM device_policies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := make([]domain.DevicePolicy, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var policy domain.DevicePolicy
		if err := json.Unmarshal([]byte(payload), &policy); err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	return policies, rows.Err()
}
func (s *Store) DeleteDevicePolicy(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM device_policies WHERE id=?`, id)
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
func normalizeStoreMAC(value string) string { return strings.ToUpper(strings.TrimSpace(value)) }

func (s *Store) SaveAudit(ctx context.Context, event domain.AuditEvent) error {
	if event.ID == "" || event.Action == "" || event.TargetID == "" {
		return errors.New("invalid audit event")
	}
	details, err := json.Marshal(event.Details)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if event.CreatedAt.IsZero() {
		event.CreatedAt = now
	}
	event.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO audit_events(id,action,target_id,outcome,details_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET outcome=excluded.outcome,details_json=excluded.details_json,updated_at=excluded.updated_at
	`, event.ID, event.Action, event.TargetID, event.Outcome, string(details), event.CreatedAt.Format(time.RFC3339Nano), event.UpdatedAt.Format(time.RFC3339Nano))
	return err
}
func (s *Store) AuditEvents(ctx context.Context, limit int) ([]domain.AuditEvent, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,action,target_id,outcome,details_json,created_at,updated_at FROM audit_events ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]domain.AuditEvent, 0)
	for rows.Next() {
		var event domain.AuditEvent
		var details, created, updated string
		if err := rows.Scan(&event.ID, &event.Action, &event.TargetID, &event.Outcome, &details, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(details), &event.Details)
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		event.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		events = append(events, event)
	}
	return events, rows.Err()
}
