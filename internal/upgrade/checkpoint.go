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
	"sync"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	_ "modernc.org/sqlite"
)

const manifestFormatVersion = 1

const (
	statusCheckpointReady   = "checkpoint_ready"
	statusPromotionProgress = "promotion_in_progress"
	statusPromoted          = "promoted"
	statusRestoreProgress   = "restore_in_progress"
	statusRestored          = "restored"
	statusAbortProgress     = "abort_in_progress"
	statusAborted           = "aborted"
)

var operationIDPattern = regexp.MustCompile(`^[A-Za-z0-9*._:-]{1,128}$`)

type DatabaseStore interface {
	BackupUpgradeDatabase(context.Context, string) error
	SchemaVersion(context.Context) (int, error)
	SaveUpgradeAudit(context.Context, domain.AuditEvent) error
}

type Quiescer interface {
	Quiesce(context.Context) (bool, error)
	Resume()
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
	Quiescer     Quiescer

	mu sync.Mutex
}

type RecoveryResult struct {
	Checkpoint Checkpoint
	Restored   bool
	Quiesce    bool
}

func (s *Service) Create(ctx context.Context, operationID string) (Checkpoint, error) {
	if s == nil {
		return Checkpoint{}, errors.New("upgrade checkpoint service is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Store == nil {
		return Checkpoint{}, errors.New("upgrade checkpoint store is required")
	}
	if s.Quiescer == nil {
		return Checkpoint{}, errors.New("upgrade quiescence coordinator is required")
	}
	if err := validateOperationID(operationID); err != nil {
		return Checkpoint{}, err
	}
	newlyQuiesced, err := s.Quiescer.Quiesce(ctx)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("quiesce upgrade writers: %w", err)
	}
	keepQuiesced := false
	defer func() {
		if newlyQuiesced && !keepQuiesced {
			s.Quiescer.Resume()
		}
	}()
	statePath, backupDir, err := s.paths()
	if err != nil {
		return Checkpoint{}, err
	}
	existing, readErr := readCheckpoint(statePath)
	if readErr == nil {
		if err := validateSnapshot(existing, backupDir); err != nil {
			return Checkpoint{}, fmt.Errorf("validate active upgrade checkpoint: %w", err)
		}
		if existing.OperationID == operationID {
			// A restored or aborted release may be attempted again, but an active
			// operation must keep using its original frozen snapshot.
			if existing.Status == statusCheckpointReady {
				keepQuiesced = true
				return existing, nil
			}
			if requiresQuiescence(existing.Status) {
				keepQuiesced = true
				return Checkpoint{}, fmt.Errorf("upgrade operation is in %s state", existing.Status)
			}
		} else if requiresQuiescence(existing.Status) {
			keepQuiesced = true
			return Checkpoint{}, errors.New("another upgrade operation owns the active checkpoint")
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Checkpoint{}, fmt.Errorf("read active upgrade checkpoint: %w", readErr)
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
	now := s.now()
	randomSuffix, err := randomHex(8)
	if err != nil {
		return Checkpoint{}, err
	}
	snapshotPath := filepath.Join(backupDir, fmt.Sprintf("foxos-%s-%s.sqlite", now.Format("20060102T150405.000000000Z"), randomSuffix))
	if err := s.Store.BackupUpgradeDatabase(ctx, snapshotPath); err != nil {
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
		Status:         statusCheckpointReady,
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
	keepQuiesced = true
	return checkpoint, nil
}

func (s *Service) MarkPromoted(ctx context.Context, operationID string) (Checkpoint, error) {
	if s == nil {
		return Checkpoint{}, errors.New("upgrade checkpoint service is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateOperationID(operationID); err != nil {
		return Checkpoint{}, err
	}
	if s.Store == nil || s.Quiescer == nil {
		return Checkpoint{}, errors.New("upgrade promotion dependencies are required")
	}
	newlyQuiesced, err := s.Quiescer.Quiesce(ctx)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("quiesce upgrade writers: %w", err)
	}
	keepQuiesced := false
	defer func() {
		if newlyQuiesced && !keepQuiesced {
			s.Quiescer.Resume()
		}
	}()
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
	if checkpoint.Status == statusPromoted {
		s.Quiescer.Resume()
		return checkpoint, nil
	}
	if checkpoint.Status != statusCheckpointReady && checkpoint.Status != statusPromotionProgress {
		keepQuiesced = requiresQuiescence(checkpoint.Status)
		return Checkpoint{}, fmt.Errorf("upgrade checkpoint cannot be promoted from %s", checkpoint.Status)
	}
	keepQuiesced = true
	schemaVersion, err := s.Store.SchemaVersion(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	if schemaVersion < checkpoint.SchemaVersion {
		return Checkpoint{}, errors.New("current database schema is older than the upgrade checkpoint")
	}
	if checkpoint.Status != statusPromotionProgress {
		checkpoint.Status = statusPromotionProgress
		checkpoint.UpdatedAt = s.now()
		if err := writeCheckpoint(statePath, checkpoint); err != nil {
			return Checkpoint{}, err
		}
	}
	if err := persistLifecycleAudits(ctx, s.Store, checkpoint, statusPromoted); err != nil {
		return Checkpoint{}, fmt.Errorf("persist upgrade promotion audit: %w", err)
	}
	checkpoint.Status = statusPromoted
	checkpoint.UpdatedAt = s.now()
	if err := writeCheckpoint(statePath, checkpoint); err != nil {
		return Checkpoint{}, err
	}
	s.Quiescer.Resume()
	keepQuiesced = false
	return checkpoint, nil
}

func (s *Service) Abort(ctx context.Context, operationID string) (Checkpoint, error) {
	if s == nil {
		return Checkpoint{}, errors.New("upgrade checkpoint service is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateOperationID(operationID); err != nil {
		return Checkpoint{}, err
	}
	if s.Store == nil || s.Quiescer == nil {
		return Checkpoint{}, errors.New("upgrade abort dependencies are required")
	}
	newlyQuiesced, err := s.Quiescer.Quiesce(ctx)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("quiesce upgrade writers: %w", err)
	}
	keepQuiesced := false
	defer func() {
		if newlyQuiesced && !keepQuiesced {
			s.Quiescer.Resume()
		}
	}()
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
	if checkpoint.Status == statusAborted || checkpoint.Status == statusRestored {
		s.Quiescer.Resume()
		return checkpoint, nil
	}
	if checkpoint.Status != statusCheckpointReady && checkpoint.Status != statusPromotionProgress && checkpoint.Status != statusAbortProgress {
		keepQuiesced = requiresQuiescence(checkpoint.Status)
		return Checkpoint{}, fmt.Errorf("upgrade checkpoint cannot be aborted from %s", checkpoint.Status)
	}
	keepQuiesced = true
	currentVersion := strings.TrimSpace(s.Version)
	if currentVersion == "" {
		currentVersion = "unknown"
	}
	if currentVersion != checkpoint.SourceVersion {
		return Checkpoint{}, errors.New("only the checkpoint source version may abort this upgrade")
	}
	schemaVersion, err := s.Store.SchemaVersion(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	if schemaVersion != checkpoint.SchemaVersion {
		return Checkpoint{}, errors.New("database schema changed after the checkpoint; rollback recovery is required")
	}
	if checkpoint.Status != statusAbortProgress {
		checkpoint.Status = statusAbortProgress
		checkpoint.UpdatedAt = s.now()
		if err := writeCheckpoint(statePath, checkpoint); err != nil {
			return Checkpoint{}, err
		}
	}
	if err := persistLifecycleAudits(ctx, s.Store, checkpoint, statusAborted); err != nil {
		return Checkpoint{}, fmt.Errorf("persist upgrade abort audit: %w", err)
	}
	checkpoint.Status = statusAborted
	checkpoint.UpdatedAt = s.now()
	if err := writeCheckpoint(statePath, checkpoint); err != nil {
		return Checkpoint{}, err
	}
	s.Quiescer.Resume()
	keepQuiesced = false
	return checkpoint, nil
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func ReconcileAudits(ctx context.Context, store interface {
	SaveUpgradeAudit(context.Context, domain.AuditEvent) error
}, checkpoint Checkpoint) error {
	if store == nil || checkpoint.OperationID == "" {
		return nil
	}
	switch checkpoint.Status {
	case statusPromoted, statusRestored, statusAborted:
		return persistLifecycleAudits(ctx, store, checkpoint, checkpoint.Status)
	default:
		return nil
	}
}

func persistLifecycleAudits(ctx context.Context, store interface {
	SaveUpgradeAudit(context.Context, domain.AuditEvent) error
}, checkpoint Checkpoint, terminalStatus string) error {
	checkpointEvent := domain.AuditEvent{
		ID:        "upgrade-checkpoint-" + checkpoint.OperationID,
		Action:    "upgrade.checkpoint",
		TargetID:  checkpoint.OperationID,
		Outcome:   domain.AuditSucceeded,
		CreatedAt: checkpoint.CreatedAt,
		Details: map[string]any{
			"schemaVersion": checkpoint.SchemaVersion,
			"sourceVersion": checkpoint.SourceVersion,
			"status":        statusCheckpointReady,
		},
	}
	if err := store.SaveUpgradeAudit(ctx, checkpointEvent); err != nil {
		return err
	}
	terminal := domain.AuditEvent{
		ID:        "upgrade-" + terminalStatus + "-" + checkpoint.OperationID,
		Action:    "upgrade." + terminalStatus,
		TargetID:  checkpoint.OperationID,
		Outcome:   domain.AuditSucceeded,
		CreatedAt: checkpoint.UpdatedAt,
		Details: map[string]any{
			"schemaVersion": checkpoint.SchemaVersion,
			"sourceVersion": checkpoint.SourceVersion,
		},
	}
	switch terminalStatus {
	case statusPromoted:
		terminal.ID = "upgrade-promoted-" + checkpoint.OperationID
	case statusRestored:
		terminal.ID = "upgrade-restored-" + checkpoint.OperationID
		terminal.Action = "upgrade.database_restored"
		terminal.Details["restoredByVersion"] = checkpoint.RestoredByVersion
	case statusAborted:
		terminal.ID = "upgrade-aborted-" + checkpoint.OperationID
	}
	return store.SaveUpgradeAudit(ctx, terminal)
}

type recoveryHooks struct {
	afterReplace func() error
}

func RecoverDatabase(databasePath, statePath, backupDir, binaryVersion string, supportedSchema int) (RecoveryResult, error) {
	return recoverDatabase(databasePath, statePath, backupDir, binaryVersion, supportedSchema, recoveryHooks{})
}

func recoverDatabase(databasePath, statePath, backupDir, binaryVersion string, supportedSchema int, hooks recoveryHooks) (RecoveryResult, error) {
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
	checkpoint, err := readCheckpoint(absoluteState)
	if errors.Is(err, os.ErrNotExist) && currentSchema <= supportedSchema {
		return RecoveryResult{}, nil
	}
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("database schema %d is newer than binary schema %d and no rollback checkpoint is usable: %w", currentSchema, supportedSchema, err)
	}
	if err := validateSnapshot(checkpoint, absoluteBackupDir); err != nil {
		return RecoveryResult{}, fmt.Errorf("validate rollback checkpoint: %w", err)
	}
	result := RecoveryResult{Checkpoint: checkpoint, Quiesce: requiresQuiescence(checkpoint.Status)}
	if checkpoint.Status == statusRestoreProgress {
		restored, err := databaseMatchesCheckpoint(absoluteDatabase, checkpoint)
		if err != nil {
			return RecoveryResult{}, fmt.Errorf("inspect rollback restoration: %w", err)
		}
		if restored {
			checkpoint.Status = statusRestored
			checkpoint.UpdatedAt = time.Now().UTC()
			if err := writeCheckpoint(absoluteState, checkpoint); err != nil {
				return RecoveryResult{}, fmt.Errorf("finalize rollback restoration: %w", err)
			}
			return RecoveryResult{Checkpoint: checkpoint, Restored: true}, nil
		}
	}
	if currentSchema <= supportedSchema && checkpoint.Status != statusRestoreProgress {
		return result, nil
	}
	if checkpoint.SchemaVersion > supportedSchema {
		return RecoveryResult{}, fmt.Errorf("database schema %d is newer than binary schema %d and checkpoint schema %d is incompatible", currentSchema, supportedSchema, checkpoint.SchemaVersion)
	}
	if checkpoint.Status != statusCheckpointReady && checkpoint.Status != statusPromotionProgress && checkpoint.Status != statusPromoted && checkpoint.Status != statusRestoreProgress {
		return RecoveryResult{}, fmt.Errorf("rollback checkpoint in %s state cannot restore a newer database", checkpoint.Status)
	}
	if checkpoint.Status != statusRestoreProgress {
		checkpoint.Status = statusRestoreProgress
		checkpoint.RestoredByVersion = strings.TrimSpace(binaryVersion)
		if checkpoint.RestoredByVersion == "" {
			checkpoint.RestoredByVersion = "unknown"
		}
		checkpoint.UpdatedAt = time.Now().UTC()
		if err := writeCheckpoint(absoluteState, checkpoint); err != nil {
			return RecoveryResult{}, fmt.Errorf("record rollback restoration intent: %w", err)
		}
	}
	if err := replaceDatabase(absoluteDatabase, checkpoint.SnapshotPath); err != nil {
		return RecoveryResult{}, fmt.Errorf("restore rollback checkpoint: %w", err)
	}
	if hooks.afterReplace != nil {
		if err := hooks.afterReplace(); err != nil {
			return RecoveryResult{}, err
		}
	}
	checkpoint.Status = statusRestored
	checkpoint.UpdatedAt = time.Now().UTC()
	if err := writeCheckpoint(absoluteState, checkpoint); err != nil {
		return RecoveryResult{}, fmt.Errorf("record rollback restoration: %w", err)
	}
	return RecoveryResult{Checkpoint: checkpoint, Restored: true}, nil
}

func (s *Service) paths() (string, string, error) {
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
	switch checkpoint.Status {
	case statusCheckpointReady, statusPromotionProgress, statusPromoted, statusRestoreProgress, statusRestored, statusAbortProgress, statusAborted:
	default:
		return errors.New("upgrade checkpoint status is invalid")
	}
	if (checkpoint.Status == statusRestoreProgress || checkpoint.Status == statusRestored) && checkpoint.RestoredByVersion == "" {
		return errors.New("upgrade checkpoint restoration metadata is invalid")
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

func databaseMatchesCheckpoint(path string, checkpoint Checkpoint) (bool, error) {
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return false, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	digest, err := fileSHA256(path)
	if err != nil {
		return false, err
	}
	return digest == checkpoint.SnapshotSHA256, nil
}

func replaceDatabase(destination, source string) error {
	sourceRoot, err := os.OpenRoot(filepath.Dir(source))
	if err != nil {
		return err
	}
	defer sourceRoot.Close()
	sourceName := filepath.Base(source)
	sourceInfo, err := sourceRoot.Lstat(sourceName)
	if err != nil {
		return err
	}
	if !sourceInfo.Mode().IsRegular() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("upgrade rollback source is not a regular file")
	}
	input, err := sourceRoot.Open(sourceName)
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
	if err := directory.Sync(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	cleanup = false
	return directory.Sync()
}

func readCheckpoint(path string) (Checkpoint, error) {
	const maxCheckpointBytes = 1 << 20
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return Checkpoint{}, err
	}
	defer root.Close()
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err != nil {
		return Checkpoint{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxCheckpointBytes {
		return Checkpoint{}, errors.New("upgrade checkpoint is not a bounded regular file")
	}
	file, err := root.Open(name)
	if err != nil {
		return Checkpoint{}, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxCheckpointBytes+1))
	if err != nil {
		return Checkpoint{}, err
	}
	if len(body) == 0 || len(body) > maxCheckpointBytes {
		return Checkpoint{}, errors.New("upgrade checkpoint size changed while reading")
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
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func requiresQuiescence(status string) bool {
	switch status {
	case statusCheckpointReady, statusPromotionProgress, statusRestoreProgress, statusAbortProgress:
		return true
	default:
		return false
	}
}

func fileSHA256(path string) (string, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	defer root.Close()
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("upgrade snapshot is not a regular file")
	}
	file, err := root.Open(name)
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
