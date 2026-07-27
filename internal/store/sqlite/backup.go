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
	if err := validateRestoreSchema(ctx, conn); err != nil {
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
	var version int
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM foxos_restore.schema_migrations`).Scan(&version); err != nil {
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
		incoming, err := tableColumns(ctx, conn, "foxos_restore", table)
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
	if schema != "main" && schema != "foxos_restore" {
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
