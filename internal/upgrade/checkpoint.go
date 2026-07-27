package upgrade

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const manifestFormatVersion = 1

var operationIDPattern = regexp.MustCompile(`^[A-Za-z0-9*._:-]{1,128}$`)

type DatabaseStore interface {
	BackupDatabase(context.Context, string) error
	SchemaVersion(context.Context) (int, error)
}

type Checkpoint struct {
	FormatVersion     int       `json:"formatVersion"`
	OperationID       string    `json:"operationId"`
	SourceVersion     string    `json:"sourceVersion"`
	SchemaVersion     int       `json:"schemaVersion"`
	SnapshotPath      string    `json:"snapshotPath"`
	SnapshotSHA256    string    `json:"snapshotSha256"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	RestoredByVersion string    `json:"restoredByVersion,omitempty"`
}

type Service struct {
	Store        DatabaseStore
	DatabasePath string
	StatePath    string
	BackupDir    string
	Version      string
	Now          func() time.Time
}

type RecoveryResult struct {
	Checkpoint Checkpoint
	Restored   bool
}

func (s Service) Create(ctx context.Context, operationID string) (Checkpoint, error) {
	if s.Store == nil {
		return Checkpoint{}, errors.New("upgrade checkpoint store is required")
	}
	if err := validateOperationID(operationID); err != nil {
		return Checkpoint{}, err
	}
	statePath, backupDir, err := s.paths()
	if err != nil {
		return Checkpoint{}, err
	}
	if existing, err := readCheckpoint(statePath); err == nil && existing.OperationID == operationID {
		if err := validateSnapshot(existing, backupDir); err == nil {
			return existing, nil
		}
	}
	schemaVersion, err := s.Store.SchemaVersion(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	if schemaVersion < 1 {
		return Checkpoint{}, errors.New("database schema is not initialized")
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return Checkpoint{}, fmt.Errorf("create upgrade backup directory: %w", err)
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	randomSuffix, err := randomHex(8)
	if err != nil {
		return Checkpoint{}, err
	}
	snapshotPath := filepath.Join(backupDir, fmt.Sprintf("foxos-%s-%s.sqlite", now.Format("20060102T150405.000000000Z"), randomSuffix))
	if err := s.Store.BackupDatabase(ctx, snapshotPath); err != nil {
		return Checkpoint{}, fmt.Errorf("create SQLite upgrade checkpoint: %w", err)
	}
	digest, err := fileSHA256(snapshotPath)
	if err != nil {
		_ = os.Remove(snapshotPath)
		return Checkpoint{}, err
	}
	checkpoint := Checkpoint{
		FormatVersion:  manifestFormatVersion,
		OperationID:    operationID,
		SourceVersion:  strings.TrimSpace(s.Version),
		SchemaVersion:  schemaVersion,
		SnapshotPath:   snapshotPath,
		SnapshotSHA256: digest,
		Status:         "checkpoint_ready",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if checkpoint.SourceVersion == "" {
		checkpoint.SourceVersion = "unknown"
	}
	if err := writeCheckpoint(statePath, checkpoint); err != nil {
		_ = os.Remove(snapshotPath)
		return Checkpoint{}, err
	}
	return checkpoint, nil
}

func (s Service) MarkPromoted(operationID string) (Checkpoint, error) {
	if err := validateOperationID(operationID); err != nil {
		return Checkpoint{}, err
	}
	statePath, backupDir, err := s.paths()
	if err != nil {
		return Checkpoint{}, err
	}
	checkpoint, err := readCheckpoint(statePath)
	if err != nil {
		return Checkpoint{}, err
	}
	if checkpoint.OperationID != operationID {
		return Checkpoint{}, errors.New("upgrade operation does not match the active checkpoint")
	}
	if err := validateSnapshot(checkpoint, backupDir); err != nil {
		return Checkpoint{}, err
	}
	checkpoint.Status = "promoted"
	checkpoint.UpdatedAt = time.Now().UTC()
	if s.Now != nil {
		checkpoint.UpdatedAt = s.Now().UTC()
	}
	if err := writeCheckpoint(statePath, checkpoint); err != nil {
		return Checkpoint{}, err
	}
	return checkpoint, nil
}

func RecoverDatabase(databasePath, statePath, backupDir, binaryVersion string, supportedSchema int) (RecoveryResult, error) {
	if databasePath == "" || databasePath == ":memory:" {
		return RecoveryResult{}, nil
	}
	if supportedSchema < 1 {
		return RecoveryResult{}, errors.New("supported schema version is invalid")
	}
	absoluteDatabase, err := filepath.Abs(databasePath)
	if err != nil {
		return RecoveryResult{}, err
	}
	absoluteState, absoluteBackupDir, err := validatedPaths(absoluteDatabase, statePath, backupDir)
	if err != nil {
		return RecoveryResult{}, err
	}
	currentSchema, err := databaseSchemaVersion(absoluteDatabase)
	if errors.Is(err, os.ErrNotExist) {
		return RecoveryResult{}, nil
	}
	if err != nil {
		return RecoveryResult{}, err
	}
	if currentSchema <= supportedSchema {
		return RecoveryResult{}, nil
	}
	checkpoint, err := readCheckpoint(absoluteState)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("database schema %d is newer than binary schema %d and no rollback checkpoint is usable: %w", currentSchema, supportedSchema, err)
	}
	if checkpoint.SchemaVersion > supportedSchema {
		return RecoveryResult{}, fmt.Errorf("database schema %d is newer than binary schema %d and checkpoint schema %d is incompatible", currentSchema, supportedSchema, checkpoint.SchemaVersion)
	}
	if err := validateSnapshot(checkpoint, absoluteBackupDir); err != nil {
		return RecoveryResult{}, fmt.Errorf("validate rollback checkpoint: %w", err)
	}
	if err := replaceDatabase(absoluteDatabase, checkpoint.SnapshotPath); err != nil {
		return RecoveryResult{}, fmt.Errorf("restore rollback checkpoint: %w", err)
	}
	checkpoint.Status = "restored"
	checkpoint.RestoredByVersion = strings.TrimSpace(binaryVersion)
	if checkpoint.RestoredByVersion == "" {
		checkpoint.RestoredByVersion = "unknown"
	}
	checkpoint.UpdatedAt = time.Now().UTC()
	if err := writeCheckpoint(absoluteState, checkpoint); err != nil {
		return RecoveryResult{}, fmt.Errorf("record rollback restoration: %w", err)
	}
	return RecoveryResult{Checkpoint: checkpoint, Restored: true}, nil
}

func (s Service) paths() (string, string, error) {
	if s.DatabasePath == "" || s.DatabasePath == ":memory:" {
		return "", "", errors.New("upgrade checkpoint requires a file-backed database")
	}
	absoluteDatabase, err := filepath.Abs(s.DatabasePath)
	if err != nil {
		return "", "", err
	}
	return validatedPaths(absoluteDatabase, s.StatePath, s.BackupDir)
}

func validatedPaths(databasePath, statePath, backupDir string) (string, string, error) {
	if statePath == "" {
		statePath = filepath.Join(filepath.Dir(databasePath), "upgrade-checkpoint.json")
	}
	if backupDir == "" {
		return "", "", errors.New("upgrade checkpoint backup directory is required")
	}
	absoluteState, err := filepath.Abs(statePath)
	if err != nil {
		return "", "", err
	}
	absoluteBackupDir, err := filepath.Abs(backupDir)
	if err != nil {
		return "", "", err
	}
	if absoluteState == databasePath {
		return "", "", errors.New("upgrade checkpoint state path must differ from database path")
	}
	return absoluteState, absoluteBackupDir, nil
}

func validateOperationID(operationID string) error {
	if !operationIDPattern.MatchString(operationID) {
		return errors.New("operationId must contain 1 to 128 safe identifier characters")
	}
	return nil
}

func validateSnapshot(checkpoint Checkpoint, backupDir string) error {
	if checkpoint.FormatVersion != manifestFormatVersion || checkpoint.SchemaVersion < 1 || checkpoint.SnapshotSHA256 == "" {
		return errors.New("upgrade checkpoint metadata is invalid")
	}
	absoluteSnapshot, err := filepath.Abs(checkpoint.SnapshotPath)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(backupDir, absoluteSnapshot)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("upgrade snapshot is outside the configured backup directory")
	}
	info, err := os.Lstat(absoluteSnapshot)
	if err != nil {
		return errors.New("upgrade snapshot is unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("upgrade snapshot is not a regular file")
	}
	digest, err := fileSHA256(absoluteSnapshot)
	if err != nil {
		return err
	}
	if digest != checkpoint.SnapshotSHA256 {
		return errors.New("upgrade snapshot checksum mismatch")
	}
	schema, err := databaseSchemaVersion(absoluteSnapshot)
	if err != nil || schema != checkpoint.SchemaVersion {
		return errors.New("upgrade snapshot schema mismatch")
	}
	return nil
}

func databaseSchemaVersion(path string) (int, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, err
	}
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return 0, err
	}
	defer database.Close()
	var version int
	if err := database.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read database schema version: %w", err)
	}
	return version, nil
}

func replaceDatabase(destination, source string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".foxos-rollback-*.sqlite")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := io.Copy(temporary, input); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	cleanup = false
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(destination + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func readCheckpoint(path string) (Checkpoint, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Checkpoint{}, err
	}
	var checkpoint Checkpoint
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("decode upgrade checkpoint: %w", err)
	}
	return checkpoint, nil
}

func writeCheckpoint(path string, checkpoint Checkpoint) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".upgrade-checkpoint-*.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func randomHex(bytes int) (string, error) {
	body := make([]byte, bytes)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}
	return hex.EncodeToString(body), nil
}
