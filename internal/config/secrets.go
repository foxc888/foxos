package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const productionSecretDirectory = "/run/secrets/foxos"

var runtimeSecretFiles = []struct {
	environment string
	file        string
}{
	{environment: "FOXOS_API_TOKEN", file: "api-token"},
	{environment: "FOXOS_CONFIRMATION_KEY", file: "confirmation-key"},
	{environment: "FOXOS_ROUTEROS_PASSWORD", file: "routeros-password"},
	{environment: "FOXOS_MIHOMO_SECRET", file: "mihomo-secret"},
}

type namedSecret struct {
	name  string
	value string
}

func rejectProductionSecretEnvironment(lookupEnv func(string) (string, bool)) error {
	for _, secret := range runtimeSecretFiles {
		if _, exists := lookupEnv(secret.environment); exists {
			return fmt.Errorf("%s must not be set in production; use the fixed secret file", secret.environment)
		}
	}
	return nil
}

func loadRuntimeSecret(production bool, environment, file, directory string, getenv func(string) string) (string, error) {
	if !production {
		return getenv(environment), nil
	}
	value, err := readSecretFile(directory, file)
	if err != nil {
		return "", fmt.Errorf("%s secret file is invalid: %w", environment, err)
	}
	return value, nil
}

func readSecretFile(directory, name string) (string, error) {
	if !filepath.IsAbs(directory) {
		return "", errors.New("secret directory must be absolute")
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil || directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return "", errors.New("secret directory is unavailable")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", errors.New("open secret directory")
	}
	defer root.Close()
	linkInfo, err := root.Lstat(name)
	if err != nil || linkInfo.Mode()&os.ModeSymlink != 0 || !linkInfo.Mode().IsRegular() || linkInfo.Size() < 32 || linkInfo.Size() > 4096 {
		return "", errors.New("secret file must be a regular file of 32 to 4096 bytes")
	}
	file, err := root.Open(name)
	if err != nil {
		return "", errors.New("secret file is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(linkInfo, info) || info.Size() < 32 || info.Size() > 4096 {
		return "", errors.New("secret file must be a regular file of 32 to 4096 bytes")
	}
	body, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(body) < 32 || len(body) > 4096 || int64(len(body)) != info.Size() {
		return "", errors.New("read complete secret file")
	}
	for _, character := range body {
		if character < 0x21 || character > 0x7e {
			return "", errors.New("secret file must contain one printable ASCII scalar without whitespace")
		}
	}
	return string(body), nil
}

func validateDistinctSecrets(secrets ...namedSecret) error {
	for left := range secrets {
		if secrets[left].value == "" {
			continue
		}
		for right := left + 1; right < len(secrets); right++ {
			if secrets[left].value == secrets[right].value {
				return fmt.Errorf("%s and %s must differ", secrets[left].name, secrets[right].name)
			}
		}
	}
	return nil
}
