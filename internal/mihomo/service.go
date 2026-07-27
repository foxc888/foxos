package mihomo

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	"gopkg.in/yaml.v3"
)

var (
	ErrMihomoUnavailable     = errors.New("Mihomo runtime is not configured")
	ErrMihomoSnapshotCorrupt = errors.New("Mihomo snapshot integrity check failed")
)

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
	digest, err := digest(body)
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
	digest, err := digest(body)
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
	result, applyErr := s.Applier.Apply(ctx, body)
	snapshot := domain.MihomoSnapshot{ID: id, Digest: digest, Label: label, Body: append([]byte(nil), body...), CreatedAt: nowUTC(s.Now)}
	if applyErr != nil {
		return result, snapshot, applyErr
	}
	if err := s.Store.SaveMihomoSnapshot(ctx, snapshot); err != nil {
		return result, snapshot, fmt.Errorf("save Mihomo snapshot: %w", err)
	}
	return result, snapshot, nil
}

func (s *Service) Restore(ctx context.Context, id, label string) (ApplyResult, domain.MihomoSnapshot, error) {
	if s == nil || s.Store == nil || s.Applier == nil || s.Applier.Runtime == nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, ErrMihomoUnavailable
	}
	snapshot, err := s.Store.MihomoSnapshot(ctx, id)
	if err != nil {
		return ApplyResult{}, domain.MihomoSnapshot{}, err
	}
	expected, err := hex.DecodeString(snapshot.Digest)
	if err != nil || len(expected) != sha256.Size {
		return ApplyResult{}, snapshot, ErrMihomoSnapshotCorrupt
	}
	actual := sha256.Sum256(snapshot.Body)
	if subtle.ConstantTimeCompare(actual[:], expected) != 1 {
		return ApplyResult{}, snapshot, ErrMihomoSnapshotCorrupt
	}
	result, err := s.Applier.Apply(ctx, snapshot.Body)
	if err != nil {
		return result, snapshot, err
	}
	if label != "" {
		snapshot.Label = label
	}
	snapshot.Digest = hex.EncodeToString(actual[:])
	snapshot.ID, err = snapshotID(snapshot.Digest)
	if err != nil {
		return result, snapshot, err
	}
	snapshot.CreatedAt = nowUTC(s.Now)
	if err := s.Store.SaveMihomoSnapshot(ctx, snapshot); err != nil {
		return result, snapshot, err
	}
	return result, snapshot, nil
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
	actual, _ := digest(body)
	if !validDigest(expectedDigest) || !strings.EqualFold(actual, expectedDigest) {
		return domain.MihomoSnapshot{}, false, nil
	}
	if err := s.Applier.Runtime.Healthy(ctx); err != nil {
		return domain.MihomoSnapshot{}, false, err
	}
	id, err := snapshotID(actual)
	if err != nil {
		return domain.MihomoSnapshot{}, false, err
	}
	snapshot := domain.MihomoSnapshot{ID: id, Digest: actual, Label: label, Body: body, CreatedAt: nowUTC(s.Now)}
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
	actual := sha256.Sum256(snapshot.Body)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), snapshot.Digest) {
		return domain.MihomoSnapshot{}, false, ErrMihomoSnapshotCorrupt
	}
	return s.ReconcileApplied(ctx, snapshot.Digest, snapshot.Label)
}

func readCurrentConfig(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 16<<20 {
		return nil, errors.New("invalid live Mihomo configuration")
	}
	return io.ReadAll(io.LimitReader(file, 16<<20+1))
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

func digest(body []byte) (string, error) {
	return domainDigest(body)
}

func domainDigest(body []byte) (string, error) {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
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

func isSecretKey(key string) bool {
	for _, candidate := range []string{"password", "uuid", "secret", "token", "private-key", "psk"} {
		if strings.Contains(key, candidate) {
			return true
		}
	}
	return false
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
