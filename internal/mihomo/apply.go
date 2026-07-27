package mihomo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

var (
	ErrApplyFailed    = errors.New("mihomo config apply failed")
	ErrRollbackFailed = errors.New("mihomo config rollback failed")
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
}

type Applier struct {
	ConfigPath string
	BackupDir  string
	Runtime    Runtime
	Now        func() time.Time
}

func (a Applier) Apply(ctx context.Context, body []byte) (ApplyResult, error) {
	if ctx == nil || a.Runtime == nil || len(body) == 0 {
		return ApplyResult{}, fmt.Errorf("%w: invalid input", ErrApplyFailed)
	}
	configPath, err := filepath.Abs(a.ConfigPath)
	if err != nil {
		return ApplyResult{}, err
	}
	backupDir, err := filepath.Abs(a.BackupDir)
	if err != nil {
		return ApplyResult{}, err
	}
	if configPath == backupDir || filepath.Dir(configPath) == configPath {
		return ApplyResult{}, fmt.Errorf("%w: unsafe path", ErrApplyFailed)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return ApplyResult{}, err
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return ApplyResult{}, err
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
	result := ApplyResult{AppliedAt: now}
	if _, err := os.Stat(configPath); err == nil {
		result.BackupPath = filepath.Join(backupDir, "config-"+now.Format("20060102T150405.000000000Z")+".yaml")
		if err := copyFile(configPath, result.BackupPath); err != nil {
			return ApplyResult{}, fmt.Errorf("%w: snapshot: %v", ErrApplyFailed, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ApplyResult{}, err
	}
	// #nosec G703 -- both paths are absolute, the temporary file is created in
	// the destination directory, and Rename is required for atomic replacement.
	if err := os.Rename(tempPath, configPath); err != nil {
		return ApplyResult{}, fmt.Errorf("%w: replace: %v", ErrApplyFailed, err)
	}
	if err := a.Runtime.Reload(ctx); err != nil {
		return a.rollback(ctx, result, configPath, err)
	}
	if err := a.Runtime.Healthy(ctx); err != nil {
		return a.rollback(ctx, result, configPath, err)
	}
	return result, nil
}

func (a Applier) rollback(ctx context.Context, result ApplyResult, configPath string, cause error) (ApplyResult, error) {
	if result.BackupPath == "" {
		return result, fmt.Errorf("%w: %v; no previous config", ErrRollbackFailed, cause)
	}
	if err := copyFile(result.BackupPath, configPath); err != nil {
		return result, fmt.Errorf("%w: restore: %v", ErrRollbackFailed, err)
	}
	if err := a.Runtime.Reload(ctx); err != nil {
		return result, fmt.Errorf("%w: reload restored config: %v", ErrRollbackFailed, err)
	}
	result.RolledBack = true
	return result, fmt.Errorf("%w: runtime check: %v", ErrApplyFailed, cause)
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
