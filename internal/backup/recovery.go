package backup

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrRestoreStateChanged   = errors.New("backup restore state changed during recovery")
	ErrRestoreRolledBack     = errors.New("backup restore was rolled back")
	ErrRestoreRollbackFailed = errors.New("backup restore rollback failed")
)

type RestoreRecoveryState string

const (
	RestoreRecoveryCompleted  RestoreRecoveryState = "completed"
	RestoreRecoveryPending    RestoreRecoveryState = "pending"
	RestoreRecoveryRolledBack RestoreRecoveryState = "rolled_back"
	RestoreRecoveryPartial    RestoreRecoveryState = "partial"
)

const (
	restoreInitializing     = "initializing"
	restoreRollbackReady    = "rollback_ready"
	restoreDatabaseWriting  = "database_writing"
	restoreDatabaseRestored = "database_restored"
	restoreMihomoApplying   = "mihomo_applying"
	restoreRollbackStarted  = "rollback_started"
	restoreRolledBack       = "rolled_back"
	restoreRollbackFailed   = "rollback_failed"
	restoreCompleted        = "completed"
)

type databaseReadback interface {
	DatabaseMatchesBackup(context.Context, string) (bool, error)
}

type restoreJournal struct {
	Version            int    `json:"version"`
	OperationID        string `json:"operationId"`
	BackupID           string `json:"backupId"`
	Digest             string `json:"digest"`
	RollbackFile       string `json:"rollbackFile"`
	MihomoBeforeDigest string `json:"mihomoBeforeDigest,omitempty"`
	Phase              string `json:"phase"`
	UpdatedAt          string `json:"updatedAt"`
}

func (s Service) ReconcileCreate(operationID string) (Manifest, bool, error) {
	if !safeID(operationID) {
		return Manifest{}, false, errors.New("backup operation id is invalid")
	}
	base, err := safeBase(s.Directory)
	if err != nil {
		return Manifest{}, false, err
	}
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	var found *Manifest
	for _, entry := range entries {
		if !entry.IsDir() || !safeID(entry.Name()) {
			continue
		}
		preview, inspectErr := inspectDirectory(base, entry.Name(), entry.Name())
		if inspectErr != nil || preview.Manifest.OperationID != operationID {
			continue
		}
		if found != nil {
			return Manifest{}, false, errors.New("backup operation produced multiple manifests")
		}
		copy := preview.Manifest
		found = &copy
	}
	if found != nil {
		return *found, true, nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(name, ".backup-") || !strings.HasSuffix(name, ".staging") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "."), ".staging")
		if !safeID(id) {
			continue
		}
		preview, inspectErr := inspectDirectory(base, name, id)
		if inspectErr != nil || preview.Manifest.OperationID != operationID {
			continue
		}
		finalPath := filepath.Join(base, id)
		if _, statErr := os.Stat(finalPath); statErr == nil {
			return Manifest{}, false, errors.New("backup publication target already exists")
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return Manifest{}, false, statErr
		}
		if err := os.Rename(filepath.Join(base, name), finalPath); err != nil {
			return Manifest{}, false, err
		}
		if err := syncDirectory(base); err != nil {
			return Manifest{}, false, err
		}
		return preview.Manifest, true, nil
	}
	return Manifest{}, false, nil
}

func (s Service) RestoreOperation(ctx context.Context, id, expectedDigest, operationID string) error {
	if !restoreMu.TryLock() {
		return ErrRestoreInProgress
	}
	defer restoreMu.Unlock()
	return s.restoreOperationLocked(ctx, id, expectedDigest, operationID)
}

func (s Service) restoreOperationLocked(ctx context.Context, id, expectedDigest, operationID string) error {
	if !safeID(operationID) {
		return errors.New("restore operation id is invalid")
	}
	preview, err := s.Inspect(id)
	if err != nil {
		return err
	}
	if len(expectedDigest) != sha256.Size*2 || subtle.ConstantTimeCompare([]byte(preview.Digest), []byte(expectedDigest)) != 1 {
		return ErrBackupChanged
	}
	base, err := safeBase(s.Directory)
	if err != nil {
		return err
	}
	journal, found, err := loadRestoreJournal(base, operationID)
	if err != nil {
		return err
	}
	if !found {
		journal = restoreJournal{
			Version:      1,
			OperationID:  operationID,
			BackupID:     id,
			Digest:       expectedDigest,
			RollbackFile: ".restore-" + operationID + ".rollback.sqlite",
			Phase:        restoreInitializing,
		}
		if preview.Manifest.Mihomo != "" {
			journal.MihomoBeforeDigest, err = s.liveMihomoDigest()
			if err != nil {
				return err
			}
		}
		if err := saveRestoreJournal(base, &journal); err != nil {
			return err
		}
	} else if journal.BackupID != id || subtle.ConstantTimeCompare([]byte(journal.Digest), []byte(expectedDigest)) != 1 {
		return ErrRestoreStateChanged
	}
	if journal.Phase == restoreCompleted {
		return nil
	}
	if journal.Phase == restoreRolledBack {
		return ErrRestoreRolledBack
	}
	if journal.Phase == restoreRollbackFailed {
		return ErrRestoreRollbackFailed
	}

	rollbackPath := filepath.Join(base, journal.RollbackFile)
	if _, statErr := os.Stat(rollbackPath); errors.Is(statErr, os.ErrNotExist) {
		if journal.Phase != restoreInitializing {
			return ErrRestoreStateChanged
		}
		if err := s.Database.BackupDatabase(ctx, rollbackPath); err != nil {
			return fmt.Errorf("create restore rollback point: %w", err)
		}
	} else if statErr != nil {
		return statErr
	}
	if journal.Phase == restoreInitializing {
		journal.Phase = restoreRollbackReady
		if err := saveRestoreJournal(base, &journal); err != nil {
			return err
		}
	}

	databasePath := filepath.Join(base, id, preview.Manifest.Database)
	targetMatches, err := s.databaseMatches(ctx, databasePath)
	if err != nil {
		return err
	}
	rollbackMatches, err := s.databaseMatches(ctx, rollbackPath)
	if err != nil {
		return err
	}
	if !targetMatches && !rollbackMatches {
		return ErrRestoreStateChanged
	}
	if rollbackMatches {
		if journal.Phase == restoreRollbackStarted {
			if ok, _ := s.mihomoMatchesDigest(ctx, journal.MihomoBeforeDigest); !ok {
				journal.Phase = restoreRollbackFailed
				_ = saveRestoreJournal(base, &journal)
				return ErrRestoreRollbackFailed
			}
			journal.Phase = restoreRolledBack
			if err := saveRestoreJournal(base, &journal); err != nil {
				return err
			}
			return ErrRestoreRolledBack
		}
		if journal.Phase != restoreRollbackReady && journal.Phase != restoreDatabaseWriting {
			return ErrRestoreStateChanged
		}
		journal.Phase = restoreDatabaseWriting
		if err := saveRestoreJournal(base, &journal); err != nil {
			return err
		}
		if err := s.Database.RestoreDatabase(ctx, databasePath); err != nil {
			return err
		}
		targetMatches, err = s.databaseMatches(ctx, databasePath)
		if err != nil || !targetMatches {
			if err != nil {
				return err
			}
			return ErrRestoreStateChanged
		}
		journal.Phase = restoreDatabaseRestored
		if err := saveRestoreJournal(base, &journal); err != nil {
			return err
		}
	}

	if preview.Manifest.Mihomo != "" {
		if s.MihomoRestore == nil {
			return errors.New("Mihomo restore runtime is unavailable")
		}
		mihomoBody, err := readBackupFile(filepath.Join(base, id), preview.Manifest.Mihomo)
		if err != nil {
			return err
		}
		targetDigest := preview.Manifest.Checksums[preview.Manifest.Mihomo]
		mihomoMatches, healthErr := s.mihomoMatchesDigest(ctx, targetDigest)
		if healthErr != nil {
			mihomoMatches = false
		}
		if !mihomoMatches {
			beforeMatches, beforeErr := s.mihomoMatchesDigest(ctx, journal.MihomoBeforeDigest)
			if beforeErr != nil || !beforeMatches {
				return ErrRestoreStateChanged
			}
			journal.Phase = restoreMihomoApplying
			if err := saveRestoreJournal(base, &journal); err != nil {
				return err
			}
			if err := s.MihomoRestore(ctx, mihomoBody); err != nil {
				return s.rollbackRestore(ctx, base, rollbackPath, &journal, err)
			}
			mihomoMatches, healthErr = s.mihomoMatchesDigest(ctx, targetDigest)
			if healthErr != nil || !mihomoMatches {
				cause := errors.New("Mihomo restore readback failed")
				if healthErr != nil {
					cause = healthErr
				}
				return s.rollbackRestore(ctx, base, rollbackPath, &journal, cause)
			}
		}
	}
	journal.Phase = restoreCompleted
	if err := saveRestoreJournal(base, &journal); err != nil {
		return err
	}
	_ = os.Remove(rollbackPath)
	_ = syncDirectory(base)
	return nil
}

func (s Service) rollbackRestore(ctx context.Context, base, rollbackPath string, journal *restoreJournal, cause error) error {
	journal.Phase = restoreRollbackStarted
	if err := saveRestoreJournal(base, journal); err != nil {
		return errors.Join(cause, err)
	}
	if err := s.Database.RestoreDatabase(ctx, rollbackPath); err != nil {
		journal.Phase = restoreRollbackFailed
		_ = saveRestoreJournal(base, journal)
		return fmt.Errorf("%w: Mihomo restore failed: %v; database rollback: %v", ErrRestoreRollbackFailed, cause, err)
	}
	rollbackMatches, err := s.databaseMatches(ctx, rollbackPath)
	if err != nil || !rollbackMatches {
		journal.Phase = restoreRollbackFailed
		_ = saveRestoreJournal(base, journal)
		return fmt.Errorf("%w: database rollback readback failed", ErrRestoreRollbackFailed)
	}
	beforeMatches, beforeErr := s.mihomoMatchesDigest(ctx, journal.MihomoBeforeDigest)
	if beforeErr != nil || !beforeMatches {
		journal.Phase = restoreRollbackFailed
		_ = saveRestoreJournal(base, journal)
		return fmt.Errorf("%w: Mihomo rollback readback failed", ErrRestoreRollbackFailed)
	}
	journal.Phase = restoreRolledBack
	if err := saveRestoreJournal(base, journal); err != nil {
		return errors.Join(cause, err)
	}
	return fmt.Errorf("%w: %v", ErrRestoreRolledBack, cause)
}

func (s Service) ReconcileRestore(ctx context.Context, id, expectedDigest, operationID string) (RestoreRecoveryState, error) {
	if !safeID(id) || !safeID(operationID) {
		return RestoreRecoveryPartial, errors.New("restore recovery identifiers are invalid")
	}
	preview, err := s.Inspect(id)
	if err != nil {
		return RestoreRecoveryPartial, err
	}
	if subtle.ConstantTimeCompare([]byte(preview.Digest), []byte(expectedDigest)) != 1 {
		return RestoreRecoveryPartial, ErrBackupChanged
	}
	base, err := safeBase(s.Directory)
	if err != nil {
		return RestoreRecoveryPartial, err
	}
	journal, found, err := loadRestoreJournal(base, operationID)
	if err != nil {
		return RestoreRecoveryPartial, err
	}
	if !found {
		return RestoreRecoveryPartial, nil
	}
	if journal.BackupID != id || subtle.ConstantTimeCompare([]byte(journal.Digest), []byte(expectedDigest)) != 1 {
		return RestoreRecoveryPartial, nil
	}
	switch journal.Phase {
	case restoreCompleted:
		return RestoreRecoveryCompleted, nil
	case restoreRolledBack:
		return RestoreRecoveryRolledBack, nil
	case restoreRollbackFailed:
		return RestoreRecoveryPartial, nil
	}
	rollbackPath := filepath.Join(base, journal.RollbackFile)
	if _, err := os.Stat(rollbackPath); err != nil {
		return RestoreRecoveryPartial, nil
	}
	databasePath := filepath.Join(base, id, preview.Manifest.Database)
	targetMatches, err := s.databaseMatches(ctx, databasePath)
	if err != nil {
		return RestoreRecoveryPartial, err
	}
	rollbackMatches, err := s.databaseMatches(ctx, rollbackPath)
	if err != nil {
		return RestoreRecoveryPartial, err
	}
	if targetMatches {
		if preview.Manifest.Mihomo == "" {
			journal.Phase = restoreCompleted
			if err := saveRestoreJournal(base, &journal); err != nil {
				return RestoreRecoveryPartial, err
			}
			_ = os.Remove(rollbackPath)
			return RestoreRecoveryCompleted, nil
		}
		targetDigest := preview.Manifest.Checksums[preview.Manifest.Mihomo]
		if matches, healthErr := s.mihomoMatchesDigest(ctx, targetDigest); matches && healthErr == nil {
			journal.Phase = restoreCompleted
			if err := saveRestoreJournal(base, &journal); err != nil {
				return RestoreRecoveryPartial, err
			}
			_ = os.Remove(rollbackPath)
			return RestoreRecoveryCompleted, nil
		}
		if matches, _ := s.mihomoMatchesDigest(ctx, journal.MihomoBeforeDigest); matches {
			return RestoreRecoveryPending, nil
		}
		return RestoreRecoveryPartial, nil
	}
	if rollbackMatches {
		if journal.Phase == restoreRollbackStarted {
			if matches, _ := s.mihomoMatchesDigest(ctx, journal.MihomoBeforeDigest); matches {
				journal.Phase = restoreRolledBack
				if err := saveRestoreJournal(base, &journal); err != nil {
					return RestoreRecoveryPartial, err
				}
				return RestoreRecoveryRolledBack, nil
			}
			return RestoreRecoveryPartial, nil
		}
		if journal.Phase == restoreInitializing || journal.Phase == restoreRollbackReady || journal.Phase == restoreDatabaseWriting {
			return RestoreRecoveryPending, nil
		}
	}
	return RestoreRecoveryPartial, nil
}

func (s Service) databaseMatches(ctx context.Context, path string) (bool, error) {
	reader, ok := s.Database.(databaseReadback)
	if !ok {
		return false, errors.New("database restore readback is unavailable")
	}
	return reader.DatabaseMatchesBackup(ctx, path)
}

func (s Service) liveMihomoDigest() (string, error) {
	if strings.TrimSpace(s.MihomoPath) == "" {
		return "", errors.New("Mihomo live configuration path is unavailable")
	}
	body, err := readRegularFile(s.MihomoPath, 16<<20)
	if errors.Is(err, os.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func (s Service) mihomoMatchesDigest(ctx context.Context, expected string) (bool, error) {
	if expected == "" {
		return true, nil
	}
	actual, err := s.liveMihomoDigest()
	if err != nil {
		return false, err
	}
	if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
		return false, nil
	}
	if expected != "absent" && s.MihomoHealthy != nil {
		if err := s.MihomoHealthy(ctx); err != nil {
			return false, err
		}
	}
	return true, nil
}

func loadRestoreJournal(base, operationID string) (restoreJournal, bool, error) {
	root, err := os.OpenRoot(base)
	if err != nil {
		return restoreJournal{}, false, err
	}
	defer root.Close()
	file, err := root.Open(restoreJournalName(operationID))
	if errors.Is(err, os.ErrNotExist) {
		return restoreJournal{}, false, nil
	}
	if err != nil {
		return restoreJournal{}, false, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(body) == 0 || len(body) > 1<<20 {
		return restoreJournal{}, false, errors.New("restore journal is invalid")
	}
	var journal restoreJournal
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil || journal.Version != 1 || journal.OperationID != operationID || !safeID(journal.BackupID) || journal.RollbackFile != ".restore-"+operationID+".rollback.sqlite" {
		return restoreJournal{}, false, errors.New("restore journal is invalid")
	}
	return journal, true, nil
}

func saveRestoreJournal(base string, journal *restoreJournal) error {
	if journal == nil || !safeID(journal.OperationID) || !safeID(journal.BackupID) {
		return errors.New("restore journal is invalid")
	}
	journal.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	body, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(base, restoreJournalName(journal.OperationID))
	temporary := target + ".tmp"
	if err := writeDurableFile(temporary, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(base)
}

func restoreJournalName(operationID string) string {
	return ".restore-" + operationID + ".json"
}

func writeDurableFile(path string, body []byte, mode os.FileMode) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.OpenFile(filepath.Base(path), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func readRegularFile(path string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, errors.New("file is not a bounded regular file")
	}
	return io.ReadAll(io.LimitReader(file, limit+1))
}
