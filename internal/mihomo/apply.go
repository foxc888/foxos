package mihomo

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrApplyFailed             = errors.New("mihomo config apply failed")
	ErrRollbackFailed          = errors.New("mihomo config rollback failed")
	ErrPendingApply            = errors.New("mihomo pending apply requires recovery")
	ErrUnsupportedApplyJournal = errors.New("unsupported Mihomo apply journal version")
)

const (
	applyIntentVersion      = 2
	applyIntentName         = ".foxos-mihomo-apply.json"
	applyIntentLimit        = 32 << 10
	applySnapshotLabelLimit = 120
	applyIntegrityKeyDomain = "foxos:mihomo:journal-key:v2\x00"
	applyConfigMACDomain    = "foxos:mihomo:journal:v2\x00"
	applyJournalMACDomain   = "foxos:mihomo:journal-envelope:v2\x00"
	rollbackTimeout         = 30 * time.Second
)

type Runtime interface {
	Validate(context.Context, []byte) error
	Reload(context.Context) error
	Healthy(context.Context) error
}

type ApplyResult struct {
	BackupPath string
	AppliedAt  time.Time
	RolledBack bool
	IntentID   string
}

type ApplyOperation struct {
	ID            string
	Kind          string
	TargetDigest  string
	SnapshotLabel string
}

type applyOperationContextKey struct{}

// WithApplyOperation binds the durable journal to the task that initiated it.
// Invalid values are rejected later by ApplyPending rather than normalized.
func WithApplyOperation(ctx context.Context, kind, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, applyOperationContextKey{}, ApplyOperation{ID: id, Kind: kind})
}

func operationFromContext(ctx context.Context, fallbackKind, targetDigest, snapshotLabel string) (ApplyOperation, error) {
	operation, _ := ctx.Value(applyOperationContextKey{}).(ApplyOperation)
	if operation.ID == "" {
		id, err := randomApplyID("operation")
		if err != nil {
			return ApplyOperation{}, err
		}
		operation.ID = id
	}
	if operation.Kind == "" {
		operation.Kind = fallbackKind
	}
	operation.TargetDigest = targetDigest
	operation.SnapshotLabel = snapshotLabel
	return operation, nil
}

type PendingApply struct {
	Version          int       `json:"version"`
	ID               string    `json:"id"`
	OperationID      string    `json:"operationId"`
	OperationKind    string    `json:"operationKind"`
	TargetMAC        string    `json:"targetMac"`
	PreviousMAC      string    `json:"previousMac,omitempty"`
	TargetDigest     string    `json:"targetDigest,omitempty"`
	SnapshotLabel    string    `json:"snapshotLabel"`
	RequiresSnapshot bool      `json:"requiresSnapshot"`
	BackupPath       string    `json:"backupPath,omitempty"`
	Phase            string    `json:"phase"`
	LastError        string    `json:"lastError,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	JournalMAC       string    `json:"journalMac"`
}

// pendingApplyMACPayload is the canonical authenticated representation. Keep
// every recovery-relevant field explicit so JSON formatting and omitted empty
// values cannot change what the journal signature covers.
type pendingApplyMACPayload struct {
	Version          int       `json:"version"`
	ID               string    `json:"id"`
	OperationID      string    `json:"operationId"`
	OperationKind    string    `json:"operationKind"`
	TargetMAC        string    `json:"targetMac"`
	PreviousMAC      string    `json:"previousMac"`
	TargetDigest     string    `json:"targetDigest"`
	SnapshotLabel    string    `json:"snapshotLabel"`
	RequiresSnapshot bool      `json:"requiresSnapshot"`
	BackupPath       string    `json:"backupPath"`
	Phase            string    `json:"phase"`
	LastError        string    `json:"lastError"`
	CreatedAt        time.Time `json:"createdAt"`
}

type Applier struct {
	ConfigPath   string
	BackupDir    string
	Runtime      Runtime
	IntegrityKey []byte // #nosec G117 -- derived secret is held in memory and never serialized.
	Now          func() time.Time

	mu               sync.Mutex
	clearPendingHook func(string) error
}

// NewApplyIntegrityKey derives a dedicated key so journal authentication
// cannot be confused with confirmation tokens or configuration previews.
func NewApplyIntegrityKey(rootKey []byte) ([]byte, error) {
	if len(rootKey) < sha256.Size {
		return nil, errors.New("Mihomo apply integrity root key must contain at least 32 bytes")
	}
	mac := hmac.New(sha256.New, rootKey)
	_, _ = mac.Write([]byte(applyIntegrityKeyDomain))
	return mac.Sum(nil), nil
}

func (a *Applier) PrepareStorage() error {
	if a == nil {
		return errors.New("Mihomo applier is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, backupDir, err := a.paths()
	if err != nil {
		return err
	}
	return prepareWritableDirectory(backupDir)
}

func (a *Applier) Apply(ctx context.Context, body []byte) (ApplyResult, error) {
	operation, err := operationFromContext(ctx, "mihomo.direct", "", "")
	if err != nil {
		return ApplyResult{}, err
	}
	result, err := a.ApplyPending(ctx, body, operation)
	if err != nil {
		return result, err
	}
	if err := a.CompletePending(result.IntentID); err != nil {
		return result, fmt.Errorf("%w: complete apply journal: %v", ErrPendingApply, err)
	}
	return result, nil
}

// ApplyPending validates and applies a configuration while leaving a durable
// journal. Callers that persist a snapshot must do so before CompletePending.
func (a *Applier) ApplyPending(ctx context.Context, body []byte, operation ApplyOperation) (ApplyResult, error) {
	if a == nil || ctx == nil || a.Runtime == nil || len(body) == 0 {
		return ApplyResult{}, fmt.Errorf("%w: invalid input", ErrApplyFailed)
	}
	if err := a.requireIntegrityKey(); err != nil {
		return ApplyResult{}, err
	}
	operation.ID = strings.TrimSpace(operation.ID)
	operation.Kind = strings.TrimSpace(operation.Kind)
	operation.TargetDigest = strings.ToLower(strings.TrimSpace(operation.TargetDigest))
	if !validJournalValue(operation.ID) || !validJournalValue(operation.Kind) || (operation.TargetDigest != "" && !validDigest(operation.TargetDigest)) || !validSnapshotLabel(operation.SnapshotLabel) || (operation.TargetDigest == "" && operation.SnapshotLabel != "") {
		return ApplyResult{}, fmt.Errorf("%w: invalid operation identity", ErrApplyFailed)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	configPath, backupDir, err := a.paths()
	if err != nil {
		return ApplyResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return ApplyResult{}, err
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return ApplyResult{}, err
	}
	if err := requireRealDirectory(filepath.Dir(configPath)); err != nil {
		return ApplyResult{}, err
	}
	if err := requireRealDirectory(backupDir); err != nil {
		return ApplyResult{}, err
	}
	if _, found, err := a.pendingUnlocked(configPath, backupDir); err != nil {
		return ApplyResult{}, err
	} else if found {
		return ApplyResult{}, ErrPendingApply
	}

	temp, err := os.CreateTemp(filepath.Dir(configPath), ".foxos-config-*.yaml")
	if err != nil {
		return ApplyResult{}, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return ApplyResult{}, err
	}
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return ApplyResult{}, err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return ApplyResult{}, err
	}
	if err := temp.Close(); err != nil {
		return ApplyResult{}, err
	}
	if err := a.Runtime.Validate(ctx, body); err != nil {
		return ApplyResult{}, fmt.Errorf("%w: validate: %v", ErrApplyFailed, err)
	}

	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	intentID, err := randomApplyID("apply")
	if err != nil {
		return ApplyResult{}, err
	}
	result := ApplyResult{AppliedAt: now, IntentID: intentID}
	previousMAC := ""
	if _, err := os.Stat(configPath); err == nil {
		previousBody, readErr := readCurrentConfig(configPath)
		if readErr != nil {
			return ApplyResult{}, fmt.Errorf("%w: read previous config: %v", ErrApplyFailed, readErr)
		}
		previousMAC = a.configMAC(previousBody)
		result.BackupPath = filepath.Join(backupDir, "config-"+now.Format("20060102T150405.000000000Z")+"-"+intentID+".yaml")
		if err := copyFile(configPath, result.BackupPath); err != nil {
			return ApplyResult{}, fmt.Errorf("%w: snapshot: %v", ErrApplyFailed, err)
		}
		backupBody, err := readRestrictedConfig(result.BackupPath, backupDir)
		if err != nil || !matchingConfigMAC(a.configMAC(backupBody), previousMAC) {
			return ApplyResult{}, fmt.Errorf("%w: snapshot readback mismatch", ErrApplyFailed)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ApplyResult{}, err
	}

	intent := PendingApply{
		Version:          applyIntentVersion,
		ID:               intentID,
		OperationID:      operation.ID,
		OperationKind:    operation.Kind,
		TargetMAC:        a.configMAC(body),
		PreviousMAC:      previousMAC,
		TargetDigest:     operation.TargetDigest,
		SnapshotLabel:    operation.SnapshotLabel,
		RequiresSnapshot: operation.TargetDigest != "",
		BackupPath:       result.BackupPath,
		Phase:            "prepared",
		CreatedAt:        now,
	}
	if err := a.writePendingUnlocked(backupDir, intent); err != nil {
		return ApplyResult{}, fmt.Errorf("%w: persist intent: %v", ErrApplyFailed, err)
	}
	// #nosec G703 -- both paths are absolute, the temporary file is created in
	// the destination directory, and Rename is required for atomic replacement.
	if err := os.Rename(tempPath, configPath); err != nil {
		_ = a.clearPendingUnlocked(backupDir, result.IntentID)
		return ApplyResult{}, fmt.Errorf("%w: replace: %v", ErrApplyFailed, err)
	}
	if err := syncDirectory(filepath.Dir(configPath)); err != nil {
		return a.rollbackPendingUnlocked(result, intent, fmt.Errorf("sync replaced config: %w", err))
	}
	intent.Phase = "config_replaced"
	if err := a.writePendingUnlocked(backupDir, intent); err != nil {
		return a.rollbackPendingUnlocked(result, intent, fmt.Errorf("persist replaced phase: %w", err))
	}
	if err := a.Runtime.Reload(ctx); err != nil {
		return a.rollbackPendingUnlocked(result, intent, err)
	}
	if err := a.Runtime.Healthy(ctx); err != nil {
		return a.rollbackPendingUnlocked(result, intent, err)
	}
	current, err := readCurrentConfig(configPath)
	if err != nil || !matchingConfigMAC(a.configMAC(current), intent.TargetMAC) {
		return a.rollbackPendingUnlocked(result, intent, errors.New("target config readback mismatch"))
	}
	intent.Phase = "external_applied"
	if err := a.writePendingUnlocked(backupDir, intent); err != nil {
		return a.rollbackPendingUnlocked(result, intent, fmt.Errorf("persist applied phase: %w", err))
	}
	return result, nil
}

func (a *Applier) Pending() (PendingApply, bool, error) {
	if a == nil {
		return PendingApply{}, false, errors.New("Mihomo applier is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	configPath, backupDir, err := a.paths()
	if err != nil {
		return PendingApply{}, false, err
	}
	return a.pendingUnlocked(configPath, backupDir)
}

func (a *Applier) CompletePending(intentID string) error {
	if a == nil {
		return errors.New("Mihomo applier is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	configPath, backupDir, err := a.paths()
	if err != nil {
		return err
	}
	intent, found, err := a.pendingUnlocked(configPath, backupDir)
	if err != nil {
		return err
	}
	if !found || intent.ID != intentID {
		return ErrPendingApply
	}
	if intent.Phase != "snapshot_confirmed" && intent.Phase != "external_applied" && intent.Phase != "rolled_back" {
		return ErrPendingApply
	}
	if intent.RequiresSnapshot && intent.Phase != "snapshot_confirmed" && intent.Phase != "rolled_back" {
		return ErrPendingApply
	}
	return a.clearPendingUnlocked(backupDir, intentID)
}

func (a *Applier) beginSnapshotSave(intentID string) error {
	return a.transitionPending(intentID, "external_applied", "snapshot_save_started")
}

func (a *Applier) markSnapshotUnconfirmed(intentID string) error {
	return a.transitionPending(intentID, "snapshot_save_started", "snapshot_unconfirmed")
}

func (a *Applier) markSnapshotConfirmed(intentID string) error {
	if err := a.transitionPending(intentID, "snapshot_unconfirmed", "snapshot_confirmed"); err == nil {
		return nil
	}
	return a.transitionPending(intentID, "snapshot_save_started", "snapshot_confirmed")
}

func (a *Applier) transitionPending(intentID, from, to string) error {
	if a == nil {
		return errors.New("Mihomo applier is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	configPath, backupDir, err := a.paths()
	if err != nil {
		return err
	}
	intent, found, err := a.pendingUnlocked(configPath, backupDir)
	if err != nil {
		return err
	}
	if !found || intent.ID != intentID || intent.Phase != from || !intent.RequiresSnapshot {
		return ErrPendingApply
	}
	intent.Phase = to
	return a.writePendingUnlocked(backupDir, intent)
}

func (a *Applier) discardPrepared(intentID string) error {
	if a == nil {
		return errors.New("Mihomo applier is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	configPath, backupDir, err := a.paths()
	if err != nil {
		return err
	}
	intent, found, err := a.pendingUnlocked(configPath, backupDir)
	if err != nil {
		return err
	}
	if !found || intent.ID != intentID || intent.Phase != "prepared" {
		return ErrPendingApply
	}
	current, err := readCurrentConfig(configPath)
	if intent.PreviousMAC == "" {
		if !errors.Is(err, os.ErrNotExist) {
			return ErrPendingApply
		}
	} else if err != nil || !matchingConfigMAC(a.configMAC(current), intent.PreviousMAC) {
		return ErrPendingApply
	}
	return a.clearPendingUnlocked(backupDir, intentID)
}

// CompensatePending always uses a fresh bounded context so request
// cancellation cannot prevent rollback of an already-applied configuration.
func (a *Applier) CompensatePending(result ApplyResult, cause error) (ApplyResult, error) {
	if a == nil {
		return result, ErrRollbackFailed
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	configPath, backupDir, err := a.paths()
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrRollbackFailed, err)
	}
	intent, found, err := a.pendingUnlocked(configPath, backupDir)
	if err != nil || !found || intent.ID != result.IntentID {
		return result, fmt.Errorf("%w: apply journal unavailable", ErrRollbackFailed)
	}
	return a.rollbackPendingUnlocked(result, intent, cause)
}

func (a *Applier) rollbackPendingUnlocked(result ApplyResult, intent PendingApply, cause error) (ApplyResult, error) {
	_, backupDir, pathErr := a.paths()
	if pathErr != nil {
		return result, fmt.Errorf("%w: %v", ErrRollbackFailed, pathErr)
	}
	intent.Phase = "rollback_required"
	intent.LastError = "rollback required"
	journalErr := a.writePendingUnlocked(backupDir, intent)
	if intent.PreviousMAC == "" || intent.BackupPath == "" {
		return result, fmt.Errorf("%w: no previous configuration is available", ErrRollbackFailed)
	}
	previous, err := readRestrictedConfig(intent.BackupPath, backupDir)
	if err != nil || !matchingConfigMAC(a.configMAC(previous), intent.PreviousMAC) {
		intent.LastError = "previous configuration verification failed"
		_ = a.writePendingUnlocked(backupDir, intent)
		return result, fmt.Errorf("%w: previous configuration verification failed", ErrRollbackFailed)
	}
	configPath, _, err := a.paths()
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrRollbackFailed, err)
	}
	if err := atomicReplace(configPath, previous); err != nil {
		intent.LastError = "previous configuration restore failed"
		_ = a.writePendingUnlocked(backupDir, intent)
		return result, fmt.Errorf("%w: restore previous configuration: %v", ErrRollbackFailed, err)
	}

	rollbackContext, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	if err := a.Runtime.Reload(rollbackContext); err != nil {
		intent.LastError = "previous configuration reload failed"
		_ = a.writePendingUnlocked(backupDir, intent)
		return result, fmt.Errorf("%w: reload previous configuration: %v", ErrRollbackFailed, err)
	}
	if err := a.Runtime.Healthy(rollbackContext); err != nil {
		intent.LastError = "previous configuration health check failed"
		_ = a.writePendingUnlocked(backupDir, intent)
		return result, fmt.Errorf("%w: verify previous configuration health: %v", ErrRollbackFailed, err)
	}
	current, err := readCurrentConfig(configPath)
	if err != nil || !matchingConfigMAC(a.configMAC(current), intent.PreviousMAC) {
		intent.LastError = "previous configuration readback failed"
		_ = a.writePendingUnlocked(backupDir, intent)
		return result, fmt.Errorf("%w: previous configuration readback mismatch", ErrRollbackFailed)
	}

	result.RolledBack = true
	intent.Phase = "rolled_back"
	intent.LastError = ""
	if err := a.writePendingUnlocked(backupDir, intent); err != nil {
		return result, fmt.Errorf("%w: persist completed rollback: %v", ErrRollbackFailed, err)
	}
	if journalErr != nil {
		return result, fmt.Errorf("%w: persist rollback journal: %v", ErrRollbackFailed, journalErr)
	}
	if err := a.clearPendingUnlocked(backupDir, intent.ID); err != nil {
		return result, fmt.Errorf("%w: %v", ErrPendingApply, err)
	}
	return result, fmt.Errorf("%w: %v", ErrApplyFailed, cause)
}

func (a *Applier) pendingUnlocked(configPath, backupDir string) (PendingApply, bool, error) {
	if err := a.requireIntegrityKey(); err != nil {
		return PendingApply{}, false, err
	}
	if err := requireRealDirectory(backupDir); errors.Is(err, os.ErrNotExist) {
		return PendingApply{}, false, nil
	} else if err != nil {
		return PendingApply{}, false, err
	}
	if err := requireRealDirectory(filepath.Dir(configPath)); err != nil {
		return PendingApply{}, false, err
	}
	path := filepath.Join(backupDir, applyIntentName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return PendingApply{}, false, nil
	}
	if err != nil {
		return PendingApply{}, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > applyIntentLimit {
		return PendingApply{}, false, errors.New("invalid Mihomo apply journal")
	}
	root, err := os.OpenRoot(backupDir)
	if err != nil {
		return PendingApply{}, false, err
	}
	defer root.Close()
	file, err := root.Open(applyIntentName)
	if err != nil {
		return PendingApply{}, false, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, applyIntentLimit+1))
	if err != nil || len(body) == 0 || len(body) > applyIntentLimit {
		return PendingApply{}, false, errors.New("invalid Mihomo apply journal")
	}
	var envelope struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return PendingApply{}, false, fmt.Errorf("decode Mihomo apply journal: %w", err)
	}
	if envelope.Version != applyIntentVersion {
		return PendingApply{}, false, fmt.Errorf("%w: found v%d, require v%d; finish recovery with the previous FoxOS version before upgrading", ErrUnsupportedApplyJournal, envelope.Version, applyIntentVersion)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var intent PendingApply
	if err := decoder.Decode(&intent); err != nil {
		return PendingApply{}, false, fmt.Errorf("decode Mihomo apply journal: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return PendingApply{}, false, errors.New("invalid trailing Mihomo apply journal data")
	}
	if err := a.verifyPendingMAC(intent); err != nil {
		return PendingApply{}, false, err
	}
	if err := a.validatePending(intent, configPath, backupDir); err != nil {
		return PendingApply{}, false, err
	}
	return intent, true, nil
}

func (a *Applier) validatePending(intent PendingApply, configPath, backupDir string) error {
	if intent.Version != applyIntentVersion || !validJournalValue(intent.ID) || !validJournalValue(intent.OperationID) || !validJournalValue(intent.OperationKind) || !validConfigMAC(intent.TargetMAC) || intent.CreatedAt.IsZero() {
		return errors.New("invalid Mihomo apply journal identity")
	}
	switch intent.Phase {
	case "prepared", "config_replaced", "external_applied", "snapshot_save_started", "snapshot_unconfirmed", "snapshot_confirmed", "rollback_required", "rolled_back":
	default:
		return errors.New("invalid Mihomo apply journal phase")
	}
	if intent.RequiresSnapshot != (intent.TargetDigest != "") || (intent.TargetDigest != "" && (!validDigest(intent.TargetDigest) || strings.ToLower(intent.TargetDigest) != intent.TargetDigest)) || !validSnapshotLabel(intent.SnapshotLabel) || (!intent.RequiresSnapshot && intent.SnapshotLabel != "") || len(intent.LastError) > 128 {
		return errors.New("invalid Mihomo apply journal metadata")
	}
	if intent.PreviousMAC == "" {
		if intent.BackupPath != "" {
			return errors.New("invalid Mihomo apply journal backup")
		}
		return nil
	}
	if !validConfigMAC(intent.PreviousMAC) || intent.BackupPath == "" {
		return errors.New("invalid Mihomo apply journal previous MAC")
	}
	backupPath, err := filepath.Abs(intent.BackupPath)
	if err != nil || filepath.Dir(backupPath) != backupDir || filepath.Clean(backupPath) == filepath.Clean(configPath) {
		return errors.New("Mihomo apply journal backup escapes the backup directory")
	}
	name := filepath.Base(backupPath)
	if !strings.HasPrefix(name, "config-") || !strings.HasSuffix(name, ".yaml") {
		return errors.New("invalid Mihomo apply journal backup name")
	}
	body, err := readRestrictedConfig(backupPath, backupDir)
	if err != nil || !matchingConfigMAC(a.configMAC(body), intent.PreviousMAC) {
		return errors.New("Mihomo apply journal backup integrity check failed")
	}
	return nil
}

func (a *Applier) writePendingUnlocked(backupDir string, intent PendingApply) error {
	journalMAC, err := a.pendingMAC(intent)
	if err != nil {
		return err
	}
	intent.JournalMAC = hex.EncodeToString(journalMAC)
	body, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	if len(body) > applyIntentLimit {
		return errors.New("Mihomo apply journal exceeds size limit")
	}
	path := filepath.Join(backupDir, applyIntentName)
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("Mihomo apply journal path is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(backupDir, ".foxos-mihomo-apply-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return syncDirectory(backupDir)
}

func (a *Applier) clearPendingUnlocked(backupDir, intentID string) error {
	if a.clearPendingHook != nil {
		if err := a.clearPendingHook(intentID); err != nil {
			return err
		}
	}
	path := filepath.Join(backupDir, applyIntentName)
	configPath, _, err := a.paths()
	if err != nil {
		return err
	}
	body, found, err := a.pendingUnlocked(configPath, backupDir)
	if err != nil {
		return err
	}
	if !found || body.ID != intentID {
		return ErrPendingApply
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(backupDir)
}

func (a *Applier) paths() (string, string, error) {
	configPath, err := filepath.Abs(a.ConfigPath)
	if err != nil {
		return "", "", err
	}
	backupDir, err := filepath.Abs(a.BackupDir)
	if err != nil {
		return "", "", err
	}
	if configPath == backupDir || filepath.Dir(configPath) == configPath || backupDir == filepath.Dir(backupDir) || filepath.Clean(backupDir) == filepath.Clean(filepath.Dir(configPath)) {
		return "", "", fmt.Errorf("%w: unsafe path", ErrApplyFailed)
	}
	return configPath, backupDir, nil
}

func readRestrictedConfig(path, rootPath string) ([]byte, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(rootPath)
	if err != nil || filepath.Dir(absPath) != absRoot {
		return nil, errors.New("configuration path escapes its restricted directory")
	}
	return readCurrentConfig(absPath)
}

func atomicReplace(path string, body []byte) error {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".foxos-rollback-*.yaml")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func copyFile(source, destination string) error {
	sourcePath, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	destinationPath, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	sourceRoot, err := os.OpenRoot(filepath.Dir(sourcePath))
	if err != nil {
		return err
	}
	defer sourceRoot.Close()
	input, err := sourceRoot.Open(filepath.Base(sourcePath))
	if err != nil {
		return err
	}
	defer input.Close()
	destinationRoot, err := os.OpenRoot(filepath.Dir(destinationPath))
	if err != nil {
		return err
	}
	defer destinationRoot.Close()
	output, err := destinationRoot.OpenFile(filepath.Base(destinationPath), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destinationPath))
}

func (a *Applier) requireIntegrityKey() error {
	if len(a.IntegrityKey) < sha256.Size {
		return fmt.Errorf("%w: Mihomo apply integrity key must contain at least 32 bytes", ErrApplyFailed)
	}
	return nil
}

func (a *Applier) configMAC(body []byte) string {
	mac := hmac.New(sha256.New, a.IntegrityKey)
	_, _ = mac.Write([]byte(applyConfigMACDomain))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *Applier) pendingMAC(intent PendingApply) ([]byte, error) {
	if err := a.requireIntegrityKey(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(pendingApplyMACPayload{
		Version:          intent.Version,
		ID:               intent.ID,
		OperationID:      intent.OperationID,
		OperationKind:    intent.OperationKind,
		TargetMAC:        intent.TargetMAC,
		PreviousMAC:      intent.PreviousMAC,
		TargetDigest:     intent.TargetDigest,
		SnapshotLabel:    intent.SnapshotLabel,
		RequiresSnapshot: intent.RequiresSnapshot,
		BackupPath:       intent.BackupPath,
		Phase:            intent.Phase,
		LastError:        intent.LastError,
		CreatedAt:        intent.CreatedAt,
	})
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, a.IntegrityKey)
	_, _ = mac.Write([]byte(applyJournalMACDomain))
	_, _ = mac.Write(payload)
	return mac.Sum(nil), nil
}

func (a *Applier) verifyPendingMAC(intent PendingApply) error {
	expected, err := a.pendingMAC(intent)
	if err != nil {
		return err
	}
	actual := make([]byte, sha256.Size)
	decoded, decodeErr := hex.DecodeString(intent.JournalMAC)
	validEncoding := decodeErr == nil && len(decoded) == sha256.Size && strings.ToLower(intent.JournalMAC) == intent.JournalMAC
	if validEncoding {
		copy(actual, decoded)
	}
	if !hmac.Equal(expected, actual) || !validEncoding {
		return errors.New("Mihomo apply journal authentication failed")
	}
	return nil
}

func matchingConfigMAC(left, right string) bool {
	leftBody, leftErr := hex.DecodeString(left)
	rightBody, rightErr := hex.DecodeString(right)
	return leftErr == nil && rightErr == nil && hmac.Equal(leftBody, rightBody)
}

func validConfigMAC(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validJournalValue(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '.' && char != '_' && char != '-' && char != ':' {
			return false
		}
	}
	return true
}

func validSnapshotLabel(value string) bool {
	if len(value) > applySnapshotLabelLimit || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func randomApplyID(prefix string) (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(entropy[:]), nil
}

func syncDirectory(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent := filepath.Dir(absPath)
	if parent == absPath {
		return errors.New("refusing to sync a filesystem root")
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer root.Close()
	name := filepath.Base(absPath)
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("sync target is not a real directory")
	}
	directory, err := root.Open(name)
	if err != nil {
		return err
	}
	defer directory.Close()
	openedInfo, err := directory.Stat()
	if err != nil {
		return err
	}
	if !openedInfo.IsDir() {
		return errors.New("sync target changed before open")
	}
	return directory.Sync()
}

func requireRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Mihomo configuration directory must not be a symlink")
	}
	return nil
}

func prepareWritableDirectory(path string) error {
	if err := requireRealDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := requireRealDirectory(path); err != nil {
		return err
	}
	probe, err := os.CreateTemp(path, ".foxos-storage-probe-*")
	if err != nil {
		return err
	}
	probePath := probe.Name()
	if err := probe.Chmod(0o600); err != nil {
		_ = probe.Close()
		_ = os.Remove(probePath)
		return err
	}
	if err := probe.Close(); err != nil {
		_ = os.Remove(probePath)
		return err
	}
	if err := os.Remove(probePath); err != nil {
		return err
	}
	return syncDirectory(path)
}
