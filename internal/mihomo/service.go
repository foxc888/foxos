package mihomo

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	"gopkg.in/yaml.v3"
)

var (
	ErrMihomoUnavailable     = errors.New("Mihomo runtime is not configured")
	ErrMihomoSnapshotCorrupt = errors.New("Mihomo snapshot integrity check failed")
	ErrMihomoDigestKey       = errors.New("Mihomo digest key must contain at least 32 bytes")
)

const snapshotPersistenceTimeout = 10 * time.Second

const digestDomain = "foxos:mihomo-config:v1\x00"

type ConfigStore interface {
	Nodes(context.Context) ([]domain.Node, error)
	Groups(context.Context) ([]domain.Group, error)
	DevicePolicies(context.Context) ([]domain.DevicePolicy, error)
	MihomoDraft(context.Context) (domain.MihomoDraft, error)
	SaveMihomoDraft(context.Context, domain.MihomoDraft) error
	SaveMihomoSnapshot(context.Context, domain.MihomoSnapshot) error
	MihomoSnapshot(context.Context, string) (domain.MihomoSnapshot, error)
	MihomoSnapshots(context.Context, int) ([]domain.MihomoSnapshot, error)
}

type Service struct {
	Store              ConfigStore
	Applier            *Applier
	BaseConfig         []byte
	DigestKey          []byte
	ProtectedAddresses []string
	Now                func() time.Time
}

type Preview struct {
	Draft     domain.MihomoDraft
	Digest    string
	YAML      string
	Diff      string
	HasSecret bool
}

func (s *Service) Preview(ctx context.Context, draft domain.MihomoDraft) (Preview, error) {
	if s == nil || s.Store == nil {
		return Preview{}, ErrMihomoUnavailable
	}
	if draft.ID == "" {
		draft.ID = "active"
	}
	if draft.Mode == "" {
		draft.Mode = "rule"
	}
	if draft.MixedPort == 0 {
		draft.MixedPort = 7890
	}
	nodes, err := s.Store.Nodes(ctx)
	if err != nil {
		return Preview{}, fmt.Errorf("read nodes: %w", err)
	}
	groups, err := s.Store.Groups(ctx)
	if err != nil {
		return Preview{}, fmt.Errorf("read proxy groups: %w", err)
	}
	policies, err := s.Store.DevicePolicies(ctx)
	if err != nil {
		return Preview{}, fmt.Errorf("read device policies: %w", err)
	}
	body, err := Generate(Input{Base: s.BaseConfig, Mode: draft.Mode, MixedPort: draft.MixedPort, AllowLAN: draft.AllowLAN, Nodes: nodes, Groups: groups, Policies: policies, ProtectedAddresses: s.ProtectedAddresses, Rules: draft.Rules})
	if err != nil {
		return Preview{}, err
	}
	digest, err := s.digest(body)
	if err != nil {
		return Preview{}, err
	}
	redacted, hasSecret := redactYAML(body)
	previous := ""
	if snapshots, snapshotErr := s.Store.MihomoSnapshots(ctx, 1); snapshotErr == nil && len(snapshots) > 0 {
		if item, err := s.Store.MihomoSnapshot(ctx, snapshots[0].ID); err == nil {
			old, _ := redactYAML(item.Body)
			previous = string(old)
		}
	}
	return Preview{Draft: draft, Digest: digest, YAML: string(redacted), Diff: unifiedDiff(previous, string(redacted)), HasSecret: hasSecret}, nil
}

func (s *Service) SaveDraft(ctx context.Context, draft domain.MihomoDraft) (domain.MihomoDraft, error) {
	if s == nil || s.Store == nil {
		return domain.MihomoDraft{}, ErrMihomoUnavailable
	}
	if draft.ID == "" {
		draft.ID = "active"
	}
	if draft.MixedPort == 0 {
		draft.MixedPort = 7890
	}
	if err := s.Store.SaveMihomoDraft(ctx, draft); err != nil {
		return domain.MihomoDraft{}, err
	}
	stored, err := s.Store.MihomoDraft(ctx)
	if err == nil {
		return stored, nil
	}
	return domain.MihomoDraft{}, err
}

func (s *Service) Draft(ctx context.Context) (domain.MihomoDraft, error) {
	if s == nil || s.Store == nil {
		return domain.MihomoDraft{}, ErrMihomoUnavailable
	}
	draft, err := s.Store.MihomoDraft(ctx)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.MihomoDraft{ID: "active", Mode: "rule", MixedPort: 7890, Rules: []string{"MATCH,DIRECT"}}, nil
	}
	return draft, err
}

// ApplyPreview applies a freshly generated configuration. The digest must be
// checked by the caller so a confirmation token cannot be reused for another
// database state.
func (s *Service) ApplyPreview(ctx context.Context, draft domain.MihomoDraft, expectedDigest, label string) (ApplyResult, domain.MihomoSnapshot, error) {
	if s == nil || s.Store == nil || s.Applier == nil || s.Applier.Runtime == nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, ErrMihomoUnavailable
	}
	nodes, err := s.Store.Nodes(ctx)
	if err != nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, err
	}
	groups, err := s.Store.Groups(ctx)
	if err != nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, err
	}
	policies, err := s.Store.DevicePolicies(ctx)
	if err != nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, err
	}
	body, err := Generate(Input{Base: s.BaseConfig, Mode: draft.Mode, MixedPort: draft.MixedPort, AllowLAN: draft.AllowLAN, Nodes: nodes, Groups: groups, Policies: policies, ProtectedAddresses: s.ProtectedAddresses, Rules: draft.Rules})
	if err != nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, err
	}
	digest, err := s.digest(body)
	if err != nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, err
	}
	if expectedDigest != "" && !strings.EqualFold(expectedDigest, digest) {
		return ApplyResult{}, domain.MihomoSnapshot{}, fmt.Errorf("configuration changed since preview")
	}
	id, err := snapshotID(digest)
	if err != nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, err
	}
	snapshotLabel, err := normalizeSnapshotLabel(label)
	if err != nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, err
	}
	operation, err := operationFromContext(ctx, "mihomo.apply", digest, snapshotLabel)
	if err != nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, err
	}
	snapshot := domain.MihomoSnapshot{ID: id, Digest: digest, Label: snapshotLabel, Body: append([]byte(nil), body...), CreatedAt: nowUTC(s.Now)}
	result, applyErr := s.Applier.ApplyPending(ctx, body, operation)
	if applyErr != nil {
		return result, snapshot, applyErr
	}
	return s.persistAppliedSnapshot(ctx, result, snapshot, "save Mihomo snapshot")
}

func (s *Service) Restore(ctx context.Context, id, label string) (ApplyResult, domain.MihomoSnapshot, error) {
	if s == nil || s.Store == nil || s.Applier == nil || s.Applier.Runtime == nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, ErrMihomoUnavailable
	}
	snapshot, err := s.Store.MihomoSnapshot(ctx, id)
	if err != nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, err
	}
	actual, err := s.digest(snapshot.Body)
	if err != nil {
		return ApplyResult{}, snapshot, err
	}
	if !matchingDigest(actual, snapshot.Digest) {
		return ApplyResult{}, snapshot, ErrMihomoSnapshotCorrupt
	}
	snapshot.Label, err = normalizeSnapshotLabel(snapshot.Label)
	if err != nil {
		return ApplyResult{}, snapshot, err
	}
	if label = strings.TrimSpace(label); label != "" {
		snapshot.Label, err = normalizeSnapshotLabel(label)
		if err != nil {
			return ApplyResult{}, snapshot, err
		}
	}
	operation, err := operationFromContext(ctx, "mihomo.restore", actual, snapshot.Label)
	if err != nil {
		return ApplyResult{}, snapshot, err
	}
	result, err := s.Applier.ApplyPending(ctx, snapshot.Body, operation)
	if err != nil {
		return result, snapshot, err
	}
	snapshot.Digest = actual
	snapshot.ID, err = snapshotID(snapshot.Digest)
	if err != nil {
		return result, snapshot, err
	}
	snapshot.CreatedAt = nowUTC(s.Now)
	return s.persistAppliedSnapshot(ctx, result, snapshot, "save restored Mihomo snapshot")
}

func (s *Service) persistAppliedSnapshot(ctx context.Context, result ApplyResult, snapshot domain.MihomoSnapshot, action string) (ApplyResult, domain.MihomoSnapshot, error) {
	if err := s.Applier.beginSnapshotSave(result.IntentID); err != nil {
		compensated, rollbackErr := s.Applier.CompensatePending(result, fmt.Errorf("begin snapshot persistence: %w", err))
		return compensated, snapshot, rollbackErr
	}
	saveCtx, cancelSave := context.WithTimeout(context.Background(), snapshotPersistenceTimeout)
	saveErr := s.Store.SaveMihomoSnapshot(saveCtx, snapshot)
	cancelSave()
	if err := s.Applier.markSnapshotUnconfirmed(result.IntentID); err != nil {
		return result, snapshot, fmt.Errorf("%w: snapshot save returned but its journal could not be advanced: %v", ErrPendingApply, err)
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), snapshotPersistenceTimeout)
	stored, err := s.Store.MihomoSnapshot(readCtx, snapshot.ID)
	cancelRead()
	if errors.Is(err, domain.ErrNotFound) {
		cause := saveErr
		if cause == nil {
			cause = errors.New("snapshot missing after save")
		}
		compensated, rollbackErr := s.Applier.CompensatePending(result, fmt.Errorf("%s: %w", action, cause))
		return compensated, snapshot, rollbackErr
	}
	if err != nil {
		return result, snapshot, fmt.Errorf("%w: snapshot commit readback failed", ErrPendingApply)
	}
	if stored.ID != snapshot.ID || stored.Label != snapshot.Label || !matchingDigest(stored.Digest, snapshot.Digest) || !bytes.Equal(stored.Body, snapshot.Body) {
		return result, snapshot, fmt.Errorf("%w: snapshot save readback mismatch", ErrPendingApply)
	}
	if err := s.Applier.markSnapshotConfirmed(result.IntentID); err != nil {
		return result, snapshot, fmt.Errorf("%w: snapshot readback succeeded but its journal could not be confirmed: %v", ErrPendingApply, err)
	}
	if err := s.Applier.CompletePending(result.IntentID); err != nil {
		return result, snapshot, fmt.Errorf("%w: snapshot persisted but apply journal cleanup failed: %v", ErrPendingApply, err)
	}
	return result, snapshot, nil
}

// RecoverPending reconciles the filesystem journal before the HTTP server is
// allowed to listen. A committed snapshot wins; otherwise the old runtime
// configuration must be restored and verified.
func (s *Service) RecoverPending(ctx context.Context) error {
	if s == nil || s.Store == nil || s.Applier == nil || s.Applier.Runtime == nil {
		return ErrMihomoUnavailable
	}
	intent, found, err := s.Applier.Pending()
	if err != nil || !found {
		return err
	}

	current, currentErr := readCurrentConfig(s.Applier.ConfigPath)
	currentMAC := ""
	if currentErr == nil {
		currentMAC = s.Applier.configMAC(current)
	}
	if intent.Phase == "prepared" && ((intent.PreviousMAC == "" && errors.Is(currentErr, os.ErrNotExist)) || (currentErr == nil && matchingConfigMAC(currentMAC, intent.PreviousMAC))) {
		return s.Applier.discardPrepared(intent.ID)
	}
	if intent.Phase == "rolled_back" && currentErr == nil && matchingConfigMAC(currentMAC, intent.PreviousMAC) {
		if err := s.Applier.Runtime.Healthy(ctx); err != nil {
			return fmt.Errorf("verify recovered Mihomo runtime: %w", err)
		}
		return s.Applier.CompletePending(intent.ID)
	}

	if (intent.Phase == "snapshot_save_started" || intent.Phase == "snapshot_unconfirmed" || intent.Phase == "snapshot_confirmed") && currentErr == nil && matchingConfigMAC(currentMAC, intent.TargetMAC) {
		committed, err := s.pendingSnapshotCommitted(ctx, intent, current)
		if err != nil {
			return err
		}
		if committed {
			if err := s.Applier.Runtime.Healthy(ctx); err != nil {
				return fmt.Errorf("verify committed Mihomo runtime: %w", err)
			}
			if intent.Phase != "snapshot_confirmed" {
				if err := s.Applier.markSnapshotConfirmed(intent.ID); err != nil {
					return err
				}
			}
			return s.Applier.CompletePending(intent.ID)
		}
		result, rollbackErr := s.Applier.CompensatePending(ApplyResult{BackupPath: intent.BackupPath, IntentID: intent.ID}, ErrPendingApply)
		if result.RolledBack {
			return nil
		}
		if rollbackErr == nil {
			rollbackErr = ErrRollbackFailed
		}
		return fmt.Errorf("recover uncommitted Mihomo snapshot: %w", rollbackErr)
	}
	if intent.Phase == "snapshot_save_started" || intent.Phase == "snapshot_unconfirmed" || intent.Phase == "snapshot_confirmed" {
		return ErrPendingApply
	}
	if intent.Phase == "external_applied" && !intent.RequiresSnapshot && currentErr == nil && matchingConfigMAC(currentMAC, intent.TargetMAC) {
		if err := s.Applier.Runtime.Healthy(ctx); err != nil {
			return err
		}
		return s.Applier.CompletePending(intent.ID)
	}

	result, rollbackErr := s.Applier.CompensatePending(ApplyResult{BackupPath: intent.BackupPath, IntentID: intent.ID}, ErrPendingApply)
	if result.RolledBack {
		return nil
	}
	if rollbackErr == nil {
		rollbackErr = ErrRollbackFailed
	}
	return fmt.Errorf("recover pending Mihomo apply: %w", rollbackErr)
}

func (s *Service) pendingSnapshotCommitted(ctx context.Context, intent PendingApply, current []byte) (bool, error) {
	if !intent.RequiresSnapshot {
		return true, nil
	}
	id, err := snapshotID(intent.TargetDigest)
	if err != nil {
		return false, err
	}
	snapshot, err := s.Store.MihomoSnapshot(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read pending Mihomo snapshot: %w", err)
	}
	if snapshot.ID != id || snapshot.Label != intent.SnapshotLabel || !matchingDigest(snapshot.Digest, intent.TargetDigest) || !matchingConfigMAC(s.Applier.configMAC(snapshot.Body), intent.TargetMAC) || !bytes.Equal(snapshot.Body, current) {
		return false, errors.New("pending Mihomo snapshot does not match the applied configuration")
	}
	return true, nil
}

func normalizeSnapshotLabel(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !validSnapshotLabel(value) {
		return "", errors.New("Mihomo snapshot label must be valid UTF-8, contain no control characters, and not exceed 120 bytes")
	}
	return value, nil
}

// ReconcileApplied closes the crash window between an atomic config replace
// and the task's terminal database update. It performs only readback, health
// verification and idempotent snapshot persistence.
func (s *Service) ReconcileApplied(ctx context.Context, expectedDigest, label string) (domain.MihomoSnapshot, bool, error) {
	if s == nil || s.Store == nil || s.Applier == nil || s.Applier.Runtime == nil {
		return domain.MihomoSnapshot{}, false, ErrMihomoUnavailable
	}
	body, err := readCurrentConfig(s.Applier.ConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		return domain.MihomoSnapshot{}, false, nil
	}
	if err != nil {
		return domain.MihomoSnapshot{}, false, err
	}
	actual, err := s.digest(body)
	if err != nil {
		return domain.MihomoSnapshot{}, false, err
	}
	if !matchingDigest(actual, expectedDigest) {
		return domain.MihomoSnapshot{}, false, nil
	}
	snapshotLabel, err := normalizeSnapshotLabel(label)
	if err != nil {
		return domain.MihomoSnapshot{}, false, err
	}
	if err := s.Applier.Runtime.Healthy(ctx); err != nil {
		return domain.MihomoSnapshot{}, false, err
	}
	id, err := snapshotID(actual)
	if err != nil {
		return domain.MihomoSnapshot{}, false, err
	}
	snapshot := domain.MihomoSnapshot{ID: id, Digest: actual, Label: snapshotLabel, Body: body, CreatedAt: nowUTC(s.Now)}
	if err := s.Store.SaveMihomoSnapshot(ctx, snapshot); err != nil {
		return domain.MihomoSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func (s *Service) ReconcileRestore(ctx context.Context, id string) (domain.MihomoSnapshot, bool, error) {
	if s == nil || s.Store == nil {
		return domain.MihomoSnapshot{}, false, ErrMihomoUnavailable
	}
	snapshot, err := s.Store.MihomoSnapshot(ctx, id)
	if err != nil {
		return domain.MihomoSnapshot{}, false, err
	}
	actual, err := s.digest(snapshot.Body)
	if err != nil {
		return domain.MihomoSnapshot{}, false, err
	}
	if !matchingDigest(actual, snapshot.Digest) {
		return domain.MihomoSnapshot{}, false, ErrMihomoSnapshotCorrupt
	}
	return s.ReconcileApplied(ctx, snapshot.Digest, snapshot.Label)
}

func readCurrentConfig(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := filepath.Base(path)
	entry, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 || entry.Size() <= 0 || entry.Size() > 16<<20 {
		return nil, errors.New("invalid live Mihomo configuration")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 16<<20 {
		return nil, errors.New("invalid live Mihomo configuration")
	}
	body, err := io.ReadAll(io.LimitReader(file, 16<<20+1))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || len(body) > 16<<20 {
		return nil, errors.New("live Mihomo configuration size changed while reading")
	}
	return body, nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func ValidateYAML(body []byte) error {
	if len(body) == 0 || len(body) > 16<<20 {
		return errors.New("Mihomo configuration size is invalid")
	}
	var document yaml.Node
	if err := yaml.Unmarshal(body, &document); err != nil {
		return fmt.Errorf("YAML: %w", err)
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("Mihomo configuration root must be a mapping")
	}
	return nil
}

func (s *Service) digest(body []byte) (string, error) {
	if s == nil {
		return "", ErrMihomoDigestKey
	}
	return domainDigest(s.DigestKey, body)
}

func domainDigest(key, body []byte) (string, error) {
	if len(key) < 32 {
		return "", ErrMihomoDigestKey
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(digestDomain))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func matchingDigest(actual, expected string) bool {
	if !validDigest(actual) || !validDigest(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(strings.ToLower(expected))) == 1
}

func redactYAML(body []byte) ([]byte, bool) {
	var node yaml.Node
	if err := yaml.Unmarshal(body, &node); err != nil {
		return []byte("# configuration preview unavailable\n"), false
	}
	secret := false
	var walk func(*yaml.Node, string)
	walk = func(current *yaml.Node, key string) {
		if current.Kind == yaml.DocumentNode {
			for _, child := range current.Content {
				walk(child, key)
			}
			return
		}
		if current.Kind == yaml.MappingNode {
			for index := 0; index+1 < len(current.Content); index += 2 {
				name, value := current.Content[index], current.Content[index+1]
				walk(value, strings.ToLower(name.Value))
			}
			return
		}
		if current.Kind == yaml.SequenceNode {
			for _, child := range current.Content {
				walk(child, key)
			}
			return
		}
		if current.Kind == yaml.ScalarNode && isSecretKey(key) && current.Value != "" {
			current.Value = "***"
			secret = true
		}
	}
	walk(&node, "")
	redacted, err := yaml.Marshal(&node)
	if err != nil {
		return []byte("# configuration preview unavailable\n"), secret
	}
	return redacted, secret
}

var sensitiveConfigKeys = map[string]struct{}{
	"accesskey":      {},
	"accesstoken":    {},
	"apikey":         {},
	"authentication": {},
	"authorization":  {},
	"clientsecret":   {},
	"obfspassword":   {},
	"password":       {},
	"presharedkey":   {},
	"privatekey":     {},
	"psk":            {},
	"refreshtoken":   {},
	"secret":         {},
	"token":          {},
	"uuid":           {},
}

func isSecretKey(key string) bool {
	_, sensitive := sensitiveConfigKeys[normalizeConfigKey(key)]
	return sensitive
}

func normalizeConfigKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	var normalized strings.Builder
	normalized.Grow(len(key))
	for _, character := range key {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			normalized.WriteRune(character)
		}
	}
	return normalized.String()
}

func unifiedDiff(old, current string) string {
	if old == current {
		return ""
	}
	oldLines := strings.Split(strings.TrimSuffix(old, "\n"), "\n")
	newLines := strings.Split(strings.TrimSuffix(current, "\n"), "\n")
	var out strings.Builder
	out.WriteString("--- previous\n+++ proposed\n")
	for _, line := range oldLines {
		if line != "" {
			out.WriteString("-" + line + "\n")
		}
	}
	for _, line := range newLines {
		if line != "" {
			out.WriteString("+" + line + "\n")
		}
	}
	return out.String()
}

func snapshotID(value string) (string, error) {
	if len(value) != sha256.Size*2 {
		return "", ErrMihomoSnapshotCorrupt
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", ErrMihomoSnapshotCorrupt
	}
	return "snapshot-" + strings.ToLower(value[:24]), nil
}

func nowUTC(now func() time.Time) time.Time {
	if now != nil {
		return now().UTC()
	}
	return time.Now().UTC()
}
