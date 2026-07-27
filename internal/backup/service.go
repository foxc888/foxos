package backup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

var (
	ErrInvalidBackup     = errors.New("backup is invalid")
	ErrBackupChanged     = errors.New("backup changed after confirmation")
	ErrRestoreInProgress = errors.New("backup restore is already in progress")
)

var restoreMu sync.Mutex

type Database interface {
	BackupDatabase(context.Context, string) error
	RestoreDatabase(context.Context, string) error
}

type Service struct {
	Database      Database
	MihomoPath    string
	MihomoRestore func(context.Context, []byte) error
	Directory     string
	Retention     int
	entropy       io.Reader
}

type Manifest struct {
	ID        string            `json:"id"`
	Label     string            `json:"label"`
	CreatedAt string            `json:"createdAt"`
	Database  string            `json:"database"`
	Mihomo    string            `json:"mihomo,omitempty"`
	FileCount int               `json:"fileCount"`
	Checksums map[string]string `json:"checksums"`
}

type Preview struct {
	Manifest Manifest `json:"manifest"`
	Digest   string   `json:"digest"`
}

func (s Service) Create(ctx context.Context, label string) (Manifest, error) {
	if s.Database == nil || s.Directory == "" {
		return Manifest{}, errors.New("backup service is not configured")
	}
	base, err := safeBase(s.Directory)
	if err != nil {
		return Manifest{}, err
	}
	label, err = normalizeLabel(label)
	if err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return Manifest{}, err
	}
	now := time.Now().UTC()
	id, err := s.newID("backup")
	if err != nil {
		return Manifest{}, fmt.Errorf("generate backup id: %w", err)
	}
	dir := filepath.Join(base, id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{ID: id, Label: label, CreatedAt: now.Format(time.RFC3339Nano), Database: "database.sqlite", Checksums: make(map[string]string)}
	if err := s.Database.BackupDatabase(ctx, filepath.Join(dir, manifest.Database)); err != nil {
		_ = os.RemoveAll(dir)
		return Manifest{}, err
	}
	if s.MihomoPath != "" {
		if _, err := os.Stat(s.MihomoPath); err == nil {
			manifest.Mihomo = "mihomo-config.yaml"
			if err := copyFile(s.MihomoPath, filepath.Join(dir, manifest.Mihomo)); err != nil {
				_ = os.RemoveAll(dir)
				return Manifest{}, err
			}
		}
	}
	for _, name := range manifestFiles(manifest) {
		digest, err := fileDigest(dir, name)
		if err != nil {
			_ = os.RemoveAll(dir)
			return Manifest{}, err
		}
		manifest.Checksums[name] = digest
	}
	manifest.FileCount = len(manifest.Checksums)
	manifestBody, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBody, 0o600); err != nil {
		return Manifest{}, err
	}
	if err := s.prune(base); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (s Service) List() ([]Manifest, error) {
	base, err := safeBase(s.Directory)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Manifest{}, nil
		}
		return nil, err
	}
	items := make([]Manifest, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		preview, err := s.Inspect(entry.Name())
		if err == nil {
			items = append(items, preview.Manifest)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt > items[j].CreatedAt })
	return items, nil
}

func (s Service) Inspect(id string) (Preview, error) {
	if s.Database == nil || !safeID(id) {
		return Preview{}, fmt.Errorf("%w: invalid backup id", ErrInvalidBackup)
	}
	base, err := safeBase(s.Directory)
	if err != nil {
		return Preview{}, err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return Preview{}, err
	}
	defer root.Close()
	manifestFile, err := root.Open(filepath.Join(id, "manifest.json"))
	if err != nil {
		return Preview{}, fmt.Errorf("%w: manifest unavailable", ErrInvalidBackup)
	}
	body, err := io.ReadAll(io.LimitReader(manifestFile, 1<<20))
	_ = manifestFile.Close()
	if err != nil || len(body) == 0 {
		return Preview{}, fmt.Errorf("%w: manifest unreadable", ErrInvalidBackup)
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || manifest.ID != id || manifest.Database != "database.sqlite" || (manifest.Mihomo != "" && manifest.Mihomo != "mihomo-config.yaml") {
		return Preview{}, fmt.Errorf("%w: manifest mismatch", ErrInvalidBackup)
	}
	files := manifestFiles(manifest)
	if manifest.FileCount != len(files) || len(manifest.Checksums) != len(files) {
		return Preview{}, fmt.Errorf("%w: file count mismatch", ErrInvalidBackup)
	}
	for _, name := range files {
		want := manifest.Checksums[name]
		if len(want) != sha256.Size*2 {
			return Preview{}, fmt.Errorf("%w: checksum missing", ErrInvalidBackup)
		}
		got, err := fileDigest(filepath.Join(base, id), name)
		if err != nil || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			return Preview{}, fmt.Errorf("%w: checksum mismatch", ErrInvalidBackup)
		}
	}
	digest := sha256.Sum256(body)
	return Preview{Manifest: manifest, Digest: hex.EncodeToString(digest[:])}, nil
}

func (s Service) Restore(ctx context.Context, id, expectedDigest string) error {
	if !restoreMu.TryLock() {
		return ErrRestoreInProgress
	}
	defer restoreMu.Unlock()
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
	var mihomoBody []byte
	if preview.Manifest.Mihomo != "" {
		if s.MihomoRestore == nil {
			return errors.New("Mihomo restore runtime is unavailable")
		}
		mihomoBody, err = readBackupFile(filepath.Join(base, id), preview.Manifest.Mihomo)
		if err != nil {
			return err
		}
	}
	rollbackID, err := s.newID("restore-rollback")
	if err != nil {
		return fmt.Errorf("generate restore rollback id: %w", err)
	}
	rollbackPath := filepath.Join(base, "."+rollbackID+".sqlite")
	if err := s.Database.BackupDatabase(ctx, rollbackPath); err != nil {
		return fmt.Errorf("create restore rollback point: %w", err)
	}
	defer os.Remove(rollbackPath)
	databasePath := filepath.Join(base, id, preview.Manifest.Database)
	if err := s.Database.RestoreDatabase(ctx, databasePath); err != nil {
		return err
	}
	if len(mihomoBody) > 0 {
		if err := s.MihomoRestore(ctx, mihomoBody); err != nil {
			if rollbackErr := s.Database.RestoreDatabase(ctx, rollbackPath); rollbackErr != nil {
				return fmt.Errorf("Mihomo restore failed: %v; database rollback failed: %w", err, rollbackErr)
			}
			return fmt.Errorf("Mihomo restore failed and database was rolled back: %w", err)
		}
	}
	return nil
}

func (s Service) Path(id string) string {
	base, err := safeBase(s.Directory)
	if !safeID(id) || err != nil {
		return ""
	}
	return filepath.Join(base, id)
}

func (s Service) prune(base string) error {
	if s.Retention <= 0 {
		s.Retention = 20
	}
	items, err := s.List()
	if err != nil {
		return err
	}
	for index := s.Retention; index < len(items); index++ {
		if !safeID(items[index].ID) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(base, items[index].ID)); err != nil {
			return err
		}
	}
	return nil
}

func safeBase(directory string) (string, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if absolute == filepath.Dir(absolute) {
		return "", errors.New("backup directory must not be a filesystem root")
	}
	return absolute, nil
}

func normalizeLabel(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > 120 {
		return "", errors.New("backup label must not exceed 120 bytes")
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return "", errors.New("backup label contains control characters")
		}
	}
	return value, nil
}

func manifestFiles(manifest Manifest) []string {
	files := []string{manifest.Database}
	if manifest.Mihomo != "" {
		files = append(files, manifest.Mihomo)
	}
	return files
}

func fileDigest(directory, name string) (string, error) {
	body, err := readBackupFile(directory, name)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func readBackupFile(directory, name string) ([]byte, error) {
	if name != "database.sqlite" && name != "mihomo-config.yaml" {
		return nil, fmt.Errorf("%w: unsupported file", ErrInvalidBackup)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 256<<20 {
		return nil, fmt.Errorf("%w: invalid backup file", ErrInvalidBackup)
	}
	return io.ReadAll(io.LimitReader(file, 256<<20+1))
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
	output, err := destinationRoot.OpenFile(filepath.Base(destinationPath), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
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
	return output.Close()
}

func (s Service) newID(prefix string) (string, error) {
	reader := s.entropy
	if reader == nil {
		reader = rand.Reader
	}
	var body [12]byte
	if _, err := io.ReadFull(reader, body[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(body[:]), nil
}

func safeID(value string) bool {
	if value == "" || len(value) > 80 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return false
		}
	}
	return true
}
