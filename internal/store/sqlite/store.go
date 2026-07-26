package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"\n\t"strings"

	"github.com/foxc888/foxos/internal/domain"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if path == "" { return nil, errors.New("database path is required") }
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { return nil, err }
	}
	db, err := sql.Open("sqlite", path)
	if err != nil { return nil, err }
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil { _ = db.Close(); return nil, err }\n\tif path != \":memory:\" { _ = os.Chmod(path, 0o600) }
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

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
		INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, CURRENT_TIMESTAMP);
	`)
	return err
}

func (s *Store) SaveNode(ctx context.Context, node domain.Node) error {
	if err := node.Validate(); err != nil { return err }
	body, err := json.Marshal(node)
	if err != nil { return err }
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO nodes(id,name,type,payload_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,type=excluded.type,payload_json=excluded.payload_json,updated_at=excluded.updated_at
	`, node.ID, node.Name, node.Type, string(body), now, now)
	return err
}

func (s *Store) Node(ctx context.Context, id string) (domain.Node, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM nodes WHERE id=?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) { return domain.Node{}, ErrNotFound }
	if err != nil { return domain.Node{}, err }
	var node domain.Node
	if err := json.Unmarshal([]byte(payload), &node); err != nil { return domain.Node{}, fmt.Errorf("decode node: %w", err) }
	return node, nil
}

func (s *Store) Nodes(ctx context.Context) ([]domain.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM nodes ORDER BY name COLLATE NOCASE`)
	if err != nil { return nil, err }
	defer rows.Close()
	nodes := make([]domain.Node, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil { return nil, err }
		var node domain.Node
		if err := json.Unmarshal([]byte(payload), &node); err != nil { return nil, fmt.Errorf("decode node: %w", err) }
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (s *Store) DeleteNode(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id=?`, id)
	if err != nil { return err }
	affected, err := result.RowsAffected()
	if err != nil { return err }
	if affected == 0 { return ErrNotFound }
	return nil
}


func (s *Store) SaveGroup(ctx context.Context, group domain.Group) error {
	if err := group.Validate(); err != nil { return err }
	body, err := json.Marshal(group)
	if err != nil { return err }
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO proxy_groups(id,name,type,payload_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,type=excluded.type,payload_json=excluded.payload_json,updated_at=excluded.updated_at
	`, group.ID, group.Name, group.Type, string(body), now, now)
	return err
}

func (s *Store) Group(ctx context.Context, id string) (domain.Group, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM proxy_groups WHERE id=?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) { return domain.Group{}, ErrNotFound }
	if err != nil { return domain.Group{}, err }
	var group domain.Group
	if err := json.Unmarshal([]byte(payload), &group); err != nil { return domain.Group{}, fmt.Errorf("decode group: %w", err) }
	return group, nil
}

func (s *Store) Groups(ctx context.Context) ([]domain.Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM proxy_groups ORDER BY name COLLATE NOCASE`)
	if err != nil { return nil, err }
	defer rows.Close()
	groups := make([]domain.Group, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil { return nil, err }
		var group domain.Group
		if err := json.Unmarshal([]byte(payload), &group); err != nil { return nil, fmt.Errorf("decode group: %w", err) }
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM proxy_groups WHERE id=?`, id)
	if err != nil { return err }
	affected, err := result.RowsAffected()
	if err != nil { return err }
	if affected == 0 { return ErrNotFound }
	return nil
}


func (s *Store) SaveDevicePolicy(ctx context.Context, policy domain.DevicePolicy) error {
	if err := policy.Validate(); err != nil { return err }
	body, err := json.Marshal(policy)
	if err != nil { return err }
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO device_policies(id,mac_address,static_ip,payload_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			mac_address=excluded.mac_address,static_ip=excluded.static_ip,payload_json=excluded.payload_json,updated_at=excluded.updated_at
	`, policy.ID, normalizeStoreMAC(policy.MACAddress), policy.StaticIP, string(body), now, now)
	return err
}
func (s *Store) DevicePolicy(ctx context.Context,id string)(domain.DevicePolicy,error){
	var payload string
	err:=s.db.QueryRowContext(ctx,`SELECT payload_json FROM device_policies WHERE id=?`,id).Scan(&payload)
	if errors.Is(err,sql.ErrNoRows){return domain.DevicePolicy{},ErrNotFound}
	if err!=nil{return domain.DevicePolicy{},err}
	var policy domain.DevicePolicy
	if err:=json.Unmarshal([]byte(payload),&policy);err!=nil{return domain.DevicePolicy{},fmt.Errorf("decode device policy: %w",err)}
	return policy,nil
}
func (s *Store) DevicePolicies(ctx context.Context)([]domain.DevicePolicy,error){
	rows,err:=s.db.QueryContext(ctx,`SELECT payload_json FROM device_policies ORDER BY id`);if err!=nil{return nil,err};defer rows.Close()
	policies:=make([]domain.DevicePolicy,0)
	for rows.Next(){var payload string;if err:=rows.Scan(&payload);err!=nil{return nil,err};var policy domain.DevicePolicy;if err:=json.Unmarshal([]byte(payload),&policy);err!=nil{return nil,err};policies=append(policies,policy)}
	return policies,rows.Err()
}
func (s *Store) DeleteDevicePolicy(ctx context.Context,id string)error{
	result,err:=s.db.ExecContext(ctx,`DELETE FROM device_policies WHERE id=?`,id);if err!=nil{return err};affected,err:=result.RowsAffected();if err!=nil{return err};if affected==0{return ErrNotFound};return nil
}
func normalizeStoreMAC(value string)string{return strings.ToUpper(strings.TrimSpace(value))}
