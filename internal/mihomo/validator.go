package mihomo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var ErrSemanticValidation = errors.New("Mihomo semantic validation failed")

type ConfigValidator interface {
	Validate(context.Context, []byte) error
}

// CommandValidator runs Mihomo's own side-effect-free configuration test.
// It never calls the live Controller, so a rejected draft cannot disrupt the
// management endpoint needed for reload and rollback.
type CommandValidator struct {
	BinaryPath string
	DataDir    string
	Timeout    time.Duration
}

func (v CommandValidator) Validate(ctx context.Context, body []byte) error {
	if err := ValidateYAML(body); err != nil {
		return fmt.Errorf("%w: %v", ErrSemanticValidation, err)
	}
	binaryPath, err := filepath.Abs(v.BinaryPath)
	if err != nil || v.BinaryPath == "" {
		return fmt.Errorf("%w: validator binary is not configured", ErrSemanticValidation)
	}
	dataDir := v.DataDir
	removeDir := false
	if dataDir == "" {
		dataDir, err = os.MkdirTemp("", "foxos-mihomo-validate-*")
		if err != nil {
			return fmt.Errorf("%w: prepare data directory", ErrSemanticValidation)
		}
		removeDir = true
	} else {
		dataDir, err = filepath.Abs(dataDir)
		if err != nil {
			return fmt.Errorf("%w: invalid data directory", ErrSemanticValidation)
		}
	}
	if removeDir {
		defer os.RemoveAll(dataDir)
	}
	temp, err := os.CreateTemp(dataDir, ".foxos-validate-*.yaml")
	if err != nil {
		return fmt.Errorf("%w: create temporary config", ErrSemanticValidation)
	}
	configPath := temp.Name()
	defer os.Remove(configPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: protect temporary config", ErrSemanticValidation)
	}
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: write temporary config", ErrSemanticValidation)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("%w: close temporary config", ErrSemanticValidation)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := v.Timeout
	if timeout <= 0 || timeout > 2*time.Minute {
		timeout = 30 * time.Second
	}
	validationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(validationCtx, binaryPath, "-t", "-d", dataDir, "-f", configPath) // #nosec G204 -- the executable is an operator-configured absolute path and all arguments are fixed or private temporary paths.
	command.Stdout = nil
	command.Stderr = nil
	if err := command.Run(); err != nil {
		if errors.Is(validationCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w: validator timed out", ErrSemanticValidation)
		}
		return ErrSemanticValidation
	}
	return nil
}

// ValidatedRuntime combines Mihomo's local semantic validator with the live
// Controller operations used after the atomic file replacement.
type ValidatedRuntime struct {
	Runtime   Runtime
	Validator ConfigValidator
}

func (r ValidatedRuntime) Validate(ctx context.Context, body []byte) error {
	if r.Validator == nil {
		return fmt.Errorf("%w: validator is unavailable", ErrSemanticValidation)
	}
	return r.Validator.Validate(ctx, body)
}

func (r ValidatedRuntime) Reload(ctx context.Context) error {
	if r.Runtime == nil {
		return errors.New("Mihomo Controller is unavailable")
	}
	return r.Runtime.Reload(ctx)
}

func (r ValidatedRuntime) Healthy(ctx context.Context) error {
	if r.Runtime == nil {
		return errors.New("Mihomo Controller is unavailable")
	}
	return r.Runtime.Healthy(ctx)
}
