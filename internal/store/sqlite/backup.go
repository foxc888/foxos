package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var ErrIncompatibleBackup = errors.New("restore source schema is incompatible")

var restoredTables = []string{
	"device_profiles",
	"device_presence_events",
	"nodes",
	"proxy_groups",
	"device_policies",
	"mihomo_drafts",
	"mihomo_snapshots",
	"subscriptions",
	"alerts",
	"alert_signals",
}

func (s *Store) BackupDatabase(ctx context.Context, destination string) error {
	return s.backupDatabase(ctx, destination)
}

// BackupUpgradeDatabase is the only live-database operation allowed through
// the write gate while an upgrade checkpoint owns the maintenance barrier.
func (s *Store) BackupUpgradeDatabase(ctx context.Context, destination string) error {
	return s.backupDatabase(allowUpgradeWrite(ctx), destination)
}

func (s *Store) backupDatabase(ctx context.Context, destination string) error {
	if destination == "" || destination == ":memory:" {
		return errors.New("backup destination is required")
	}
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(absolute); err == nil {
		return errors.New("backup destination already exists")
	}
	_, err = s.db.ExecContext(ctx, `VACUUM INTO ?`, absolute)
	if err != nil {
		return err
	}
	return os.Chmod(absolute, 0o600)
}

// DatabaseMatchesBackup compares only the application tables owned by backup
// restore. Jobs, replay tokens and audit events intentionally remain outside
// the comparison because a restore never replaces them.
func (s *Store) DatabaseMatchesBackup(ctx context.Context, source string) (returnMatch bool, returnErr error) {
	if source == "" || strings.Contains(source, "\x00") {
		return false, errors.New("backup comparison source is invalid")
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		return false, err
	}
	if info, err := os.Stat(absolute); err != nil || info.IsDir() {
		return false, errors.New("backup comparison source is unavailable")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS foxos_compare`, absolute); err != nil {
		return false, err
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), `DETACH DATABASE foxos_compare`); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("detach backup comparison source: %w", err)
		}
	}()
	if err := validateAttachedSchema(ctx, conn.Conn, "foxos_compare"); err != nil {
		return false, err
	}
	for _, table := range restoredTables {
		// The schema and table names are selected from fixed allowlists.
		query := "SELECT NOT EXISTS(SELECT * FROM main." + table + " EXCEPT SELECT * FROM foxos_compare." + table + ") AND NOT EXISTS(SELECT * FROM foxos_compare." + table + " EXCEPT SELECT * FROM main." + table + ")" // #nosec G202
		var matches bool
		if err := conn.QueryRowContext(ctx, query).Scan(&matches); err != nil {
			return false, err
		}
		if !matches {
			return false, nil
		}
	}
	return true, nil
}

// RestoreDatabase copies application tables from a verified SQLite backup in a
// single transaction. Schema migrations remain owned by the running binary.
func (s *Store) RestoreDatabase(ctx context.Context, source string) (returnErr error) {
	if source == "" {
		return errors.New("restore source is required")
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	if info, err := os.Stat(absolute); err != nil || info.IsDir() {
		return errors.New("restore source is unavailable")
	}
	if strings.Contains(absolute, "\x00") {
		return errors.New("invalid restore source")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS foxos_restore`, absolute); err != nil {
		return err
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), `DETACH DATABASE foxos_restore`); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("detach restore source: %w", err)
		}
	}()
	var integrity string
	if err := conn.QueryRowContext(ctx, `PRAGMA foxos_restore.integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("restore source failed SQLite integrity check")
	}
	if err := validateRestoreSchema(ctx, conn.Conn); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`DELETE FROM device_presence_events`,
		`DELETE FROM device_profiles`,
		`INSERT INTO device_profiles SELECT * FROM foxos_restore.device_profiles`,
		`INSERT INTO device_presence_events SELECT * FROM foxos_restore.device_presence_events`,
		`DELETE FROM nodes`, `INSERT INTO nodes SELECT * FROM foxos_restore.nodes`,
		`DELETE FROM proxy_groups`, `INSERT INTO proxy_groups SELECT * FROM foxos_restore.proxy_groups`,
		`DELETE FROM device_policies`, `INSERT INTO device_policies SELECT * FROM foxos_restore.device_policies`,
		`DELETE FROM mihomo_drafts`, `INSERT INTO mihomo_drafts SELECT * FROM foxos_restore.mihomo_drafts`,
		`DELETE FROM mihomo_snapshots`, `INSERT INTO mihomo_snapshots SELECT * FROM foxos_restore.mihomo_snapshots`,
		`DELETE FROM subscriptions`, `INSERT INTO subscriptions SELECT * FROM foxos_restore.subscriptions`,
		`DELETE FROM alerts`, `INSERT INTO alerts SELECT * FROM foxos_restore.alerts`,
		`DELETE FROM alert_signals`, `INSERT INTO alert_signals SELECT * FROM foxos_restore.alert_signals`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type restoreColumn struct {
	Name         string
	Type         string
	NotNull      int
	DefaultValue string
	HasDefault   bool
	PrimaryKey   int
}

func validateRestoreSchema(ctx context.Context, conn *sql.Conn) error {
	return validateAttachedSchema(ctx, conn, "foxos_restore")
}

func validateAttachedSchema(ctx context.Context, conn *sql.Conn, schema string) error {
	if schema != "foxos_restore" && schema != "foxos_compare" {
		return fmt.Errorf("%w: invalid schema", ErrIncompatibleBackup)
	}
	var version int
	// Schema is selected from the fixed allowlist above.
	if err := conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM "+schema+".schema_migrations").Scan(&version); err != nil { // #nosec G202
		return fmt.Errorf("%w: schema migration metadata is unavailable", ErrIncompatibleBackup)
	}
	if version != currentSchemaVersion {
		return fmt.Errorf("%w: schema version %d, expected %d", ErrIncompatibleBackup, version, currentSchemaVersion)
	}
	for _, table := range restoredTables {
		current, err := tableColumns(ctx, conn, "main", table)
		if err != nil {
			return err
		}
		incoming, err := tableColumns(ctx, conn, schema, table)
		if err != nil {
			return err
		}
		if len(current) == 0 || !slices.Equal(current, incoming) {
			return fmt.Errorf("%w: table %s does not match", ErrIncompatibleBackup, table)
		}
	}
	return nil
}

func tableColumns(ctx context.Context, conn *sql.Conn, schema, table string) ([]restoreColumn, error) {
	if schema != "main" && schema != "foxos_restore" && schema != "foxos_compare" {
		return nil, fmt.Errorf("%w: invalid schema", ErrIncompatibleBackup)
	}
	if !slices.Contains(restoredTables, table) {
		return nil, fmt.Errorf("%w: invalid table", ErrIncompatibleBackup)
	}
	// schema and table are selected from the fixed allowlists above.
	rows, err := conn.QueryContext(ctx, "PRAGMA "+schema+".table_info("+table+")") // #nosec G202
	if err != nil {
		return nil, fmt.Errorf("%w: inspect table %s", ErrIncompatibleBackup, table)
	}
	defer rows.Close()
	columns := make([]restoreColumn, 0)
	for rows.Next() {
		var cid int
		var column restoreColumn
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &column.Name, &column.Type, &column.NotNull, &defaultValue, &column.PrimaryKey); err != nil {
			return nil, fmt.Errorf("%w: inspect table %s", ErrIncompatibleBackup, table)
		}
		column.DefaultValue = defaultValue.String
		column.HasDefault = defaultValue.Valid
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: inspect table %s", ErrIncompatibleBackup, table)
	}
	return columns, nil
}
