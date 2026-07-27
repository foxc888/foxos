package mihomo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type validatorFunc func(context.Context, []byte) error

func (f validatorFunc) Validate(ctx context.Context, body []byte) error {
	return f(ctx, body)
}

func TestCommandValidatorRunsSideEffectFreeMihomoTest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	binary := filepath.Join(root, "mihomo-test")
	script := `#!/bin/sh
if [ "$1" != "-t" ] || [ "$2" != "-d" ] || [ "$4" != "-f" ] || [ ! -f "$5" ]; then
  exit 64
fi
if grep -q '^mode: broken$' "$5"; then
  exit 2
fi
exit 0
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	validator := CommandValidator{BinaryPath: binary, DataDir: root}
	if err := validator.Validate(context.Background(), []byte("mode: rule\n")); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := validator.Validate(context.Background(), []byte("mode: broken\n")); !errors.Is(err, ErrSemanticValidation) {
		t.Fatalf("semantic error=%v", err)
	}
}

func TestValidatedRuntimeRejectsBeforeControllerMutation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runtime := &fakeRuntime{}
	validated := ValidatedRuntime{
		Runtime: runtime,
		Validator: validatorFunc(func(context.Context, []byte) error {
			return ErrSemanticValidation
		}),
	}
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("mode: rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (Applier{ConfigPath: config, BackupDir: filepath.Join(root, "backups"), Runtime: validated}).Apply(context.Background(), []byte("mode: broken\n"))
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("Apply() error=%v", err)
	}
	if runtime.reloads != 0 {
		t.Fatalf("Controller was mutated during validation: reloads=%d", runtime.reloads)
	}
	body, readErr := os.ReadFile(config)
	if readErr != nil || string(body) != "mode: rule\n" {
		t.Fatalf("live config changed before semantic validation: %q err=%v", body, readErr)
	}
}
