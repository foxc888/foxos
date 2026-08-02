package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	testAPIToken        = "Q9v!2Lm#8Rk$4Dz%7Hs&1Wc@6Np*3Fx!"
	testConfirmationKey = "T4m@8Qz!1Vk#7Hs$3Np%9Dc&2Lw*6Ry!"
	testMihomoSecret    = "M7k#2Px!9Vr$4Dn%1Hs&8Qw@5Lc*3Tz!"
	testRouterPassword  = "R8s!4Vm#1Qx$7Nk%3Dz"
)

func TestLoadRequiresToken(t *testing.T) {
	t.Setenv("FOXOS_API_TOKEN", "")
	t.Setenv("FOXOS_CONFIRMATION_KEY", testConfirmationKey)
	if _, err := Load(); err == nil {
		t.Fatal("expected token error")
	}
}
func TestLoadOptionalAdapters(t *testing.T) {
	t.Setenv("FOXOS_API_TOKEN", testAPIToken)
	t.Setenv("FOXOS_CONFIRMATION_KEY", testConfirmationKey)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RouterOS.URL != "" || cfg.Mihomo.URL != "" {
		t.Fatalf("cfg=%+v", cfg)
	}
	if cfg.Site.PublicHostname != "foxos.home.arpa" || cfg.HTTPS.Enabled {
		t.Fatalf("unexpected site or HTTPS defaults: %+v", cfg)
	}
}
func TestLoadRejectsCredentialsInURL(t *testing.T) {
	t.Setenv("FOXOS_API_TOKEN", testAPIToken)
	t.Setenv("FOXOS_CONFIRMATION_KEY", testConfirmationKey)
	t.Setenv("FOXOS_ROUTEROS_URL", "http://admin:secret@10.0.0.1")
	t.Setenv("FOXOS_ROUTEROS_USERNAME", "admin")
	t.Setenv("FOXOS_ROUTEROS_PASSWORD", "secret")
	if _, err := Load(); err == nil {
		t.Fatal("expected URL error")
	}
}

func TestLoadRequiresStrongMihomoSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		wantErr bool
	}{
		{name: "missing", secret: "", wantErr: true},
		{name: "too short", secret: strings.Repeat("m", 31), wantErr: true},
		{name: "surrounding whitespace", secret: " " + strings.Repeat("m", 32), wantErr: true},
		{name: "valid", secret: testMihomoSecret},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("FOXOS_API_TOKEN", testAPIToken)
			t.Setenv("FOXOS_CONFIRMATION_KEY", testConfirmationKey)
			t.Setenv("FOXOS_MIHOMO_URL", "http://10.0.0.2:9090")
			t.Setenv("FOXOS_MIHOMO_SECRET", test.secret)
			t.Setenv("FOXOS_MIHOMO_LOCAL_CONFIG", "/var/lib/foxos/mihomo/config.yaml")
			t.Setenv("FOXOS_MIHOMO_RUNTIME_CONFIG", "/root/.config/mihomo/config.yaml")
			t.Setenv("FOXOS_MIHOMO_BACKUP_DIR", "/var/lib/foxos/mihomo/backups")
			cfg, err := Load()
			if (err != nil) != test.wantErr {
				t.Fatalf("Load() error = %v, wantErr %t", err, test.wantErr)
			}
			if err == nil && (cfg.Mihomo.BaseConfigPath != "/data/mihomo/base.yaml" || cfg.Mihomo.ValidatorBinary != "/usr/local/bin/mihomo") {
				t.Fatalf("unexpected Mihomo safe defaults: %+v", cfg.Mihomo)
			}
		})
	}
}

func TestLoadHTTPSGateway(t *testing.T) {
	t.Setenv("FOXOS_API_TOKEN", testAPIToken)
	t.Setenv("FOXOS_CONFIRMATION_KEY", testConfirmationKey)
	t.Setenv("FOXOS_HTTPS_ENABLED", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HTTPS.Enabled || cfg.HTTPS.InternalListen != "127.0.0.1:8090" || cfg.HTTPS.PublicListen != ":443" || cfg.HTTPS.HTTPRedirectListen != ":80" {
		t.Fatalf("unexpected HTTPS config: %+v", cfg.HTTPS)
	}
	t.Setenv("FOXOS_INTERNAL_LISTEN", "0.0.0.0:8090")
	if _, err := Load(); err == nil {
		t.Fatal("expected public internal-listener rejection")
	}
}

func TestLoadRejectsEndpointOutsideSiteManifest(t *testing.T) {
	t.Setenv("FOXOS_API_TOKEN", testAPIToken)
	t.Setenv("FOXOS_CONFIRMATION_KEY", testConfirmationKey)
	t.Setenv("FOXOS_ROUTEROS_URL", "http://192.168.1.1")
	t.Setenv("FOXOS_ROUTEROS_USERNAME", "foxos")
	t.Setenv("FOXOS_ROUTEROS_PASSWORD", testRouterPassword)
	if _, err := Load(); err == nil {
		t.Fatal("expected endpoint/site mismatch")
	}
}

func TestValidateSecretRejectsLowEntropyAndPlaceholders(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		strings.Repeat("a", 32),
		strings.Repeat("abcd", 8),
		"ChangeMe-This-Is-Not-A-Real-Secret-1234",
		"safe-looking-password-value-1234!ABC",
	} {
		if err := validateSecret("TEST_SECRET", value, 32); err == nil {
			t.Fatalf("unsafe secret accepted: %q", value)
		}
	}
	if err := validateSecret("TEST_SECRET", testAPIToken, 32); err != nil {
		t.Fatalf("strong fixture rejected: %v", err)
	}
}

func TestLoadProductionFailsClosed(t *testing.T) {
	secretDirectory := t.TempDir()
	writeSecretFixture(t, secretDirectory, "api-token", testAPIToken)
	writeSecretFixture(t, secretDirectory, "confirmation-key", testConfirmationKey)
	writeSecretFixture(t, secretDirectory, "routeros-password", "RouterPass_7vQ4mK9xP2cR8tW5yH3dF6jL")
	writeSecretFixture(t, secretDirectory, "mihomo-secret", "MihomoSecret_3pT8wY1kH6rD9sF2mV5xC7q")
	unsetProductionSecretEnvironment(t)
	t.Setenv("FOXOS_ENV", "production")
	t.Setenv("FOXOS_BACKUP_DIR", "/data/backups")
	if _, err := load(secretDirectory); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("production without HTTPS err=%v", err)
	}
	t.Setenv("FOXOS_HTTPS_ENABLED", "true")
	t.Setenv("FOXOS_BACKUP_DIR", "relative/backups")
	if _, err := load(secretDirectory); err == nil || !strings.Contains(err.Error(), "FOXOS_BACKUP_DIR") {
		t.Fatalf("production with relative backup path err=%v", err)
	}
	t.Setenv("FOXOS_BACKUP_DIR", "/data/backups")
	t.Setenv("FOXOS_UPGRADE_STATE_PATH", "relative/checkpoint.json")
	if _, err := load(secretDirectory); err == nil || !strings.Contains(err.Error(), "FOXOS_UPGRADE_STATE_PATH") {
		t.Fatalf("production with relative upgrade path err=%v", err)
	}
	t.Setenv("FOXOS_UPGRADE_STATE_PATH", "/data/upgrade-checkpoint.json")
	if _, err := load(secretDirectory); err != nil {
		t.Fatalf("valid production configuration rejected: %v", err)
	}
}

func TestLoadProductionUsesSecretFilesAndRejectsLegacyEnvironment(t *testing.T) {
	secretDirectory := t.TempDir()
	writeSecretFixture(t, secretDirectory, "api-token", testAPIToken)
	writeSecretFixture(t, secretDirectory, "confirmation-key", testConfirmationKey)
	writeSecretFixture(t, secretDirectory, "routeros-password", "RouterPass_7vQ4mK9xP2cR8tW5yH3dF6jL")
	writeSecretFixture(t, secretDirectory, "mihomo-secret", "MihomoSecret_3pT8wY1kH6rD9sF2mV5xC7q")
	unsetProductionSecretEnvironment(t)
	t.Setenv("FOXOS_ENV", "production")
	t.Setenv("FOXOS_HTTPS_ENABLED", "true")
	t.Setenv("FOXOS_BACKUP_DIR", "/data/backups")
	config, err := load(secretDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if config.APIToken != testAPIToken || config.ConfirmationKey != testConfirmationKey {
		t.Fatal("production did not load the fixed secret files")
	}
	t.Setenv("FOXOS_API_TOKEN", "")
	if _, err := load(secretDirectory); err == nil || !strings.Contains(err.Error(), "must not be set in production") {
		t.Fatalf("empty legacy production secret environment was accepted: %v", err)
	}
	t.Setenv("FOXOS_API_TOKEN", testAPIToken)
	if _, err := load(secretDirectory); err == nil || !strings.Contains(err.Error(), "must not be set in production") || strings.Contains(err.Error(), testAPIToken) {
		t.Fatalf("legacy production secret error = %v", err)
	}
}

func TestReadSecretFileFailsClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "missing", setup: func(*testing.T, string) {}},
		{name: "directory", setup: func(t *testing.T, root string) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(root, "api-token"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink", setup: func(t *testing.T, root string) {
			t.Helper()
			writeSecretFixture(t, root, "target", testAPIToken)
			if err := os.Symlink("target", filepath.Join(root, "api-token")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "trailing newline", setup: func(t *testing.T, root string) {
			t.Helper()
			writeSecretFixture(t, root, "api-token", testAPIToken+"\n")
		}},
		{name: "too short", setup: func(t *testing.T, root string) {
			t.Helper()
			writeSecretFixture(t, root, "api-token", "short-printable-secret")
		}},
		{name: "oversized", setup: func(t *testing.T, root string) {
			t.Helper()
			writeSecretFixture(t, root, "api-token", strings.Repeat("A", 4097))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			test.setup(t, root)
			if _, err := readSecretFile(root, "api-token"); err == nil {
				t.Fatal("invalid secret file was accepted")
			}
		})
	}
}

func TestValidateDistinctSecrets(t *testing.T) {
	t.Parallel()
	if err := validateDistinctSecrets(namedSecret{name: "a", value: testAPIToken}, namedSecret{name: "b", value: testAPIToken}); err == nil {
		t.Fatal("duplicate secret values were accepted")
	}
	if err := validateDistinctSecrets(namedSecret{name: "a", value: testAPIToken}, namedSecret{name: "b", value: testConfirmationKey}); err != nil {
		t.Fatalf("distinct secret values were rejected: %v", err)
	}
}

func writeSecretFixture(t *testing.T, directory, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func unsetProductionSecretEnvironment(t *testing.T) {
	t.Helper()
	for _, secret := range runtimeSecretFiles {
		t.Setenv(secret.environment, "")
		if err := os.Unsetenv(secret.environment); err != nil {
			t.Fatalf("unset %s: %v", secret.environment, err)
		}
	}
}

func TestParsePrivateCIDRs(t *testing.T) {
	t.Parallel()
	got, err := parsePrivateCIDRs("10.20.0.0/16,172.16.4.0/24,fd42:1234::/48")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.20.0.0/16", "172.16.4.0/24", "fd42:1234::/48"}
	values := make([]string, 0, len(got))
	for _, prefix := range got {
		values = append(values, prefix.String())
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("prefixes=%v want=%v", values, want)
	}
	for _, invalid := range []string{
		"10.20.1.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"1.1.1.0/24",
		"10.0.0.0/8,10.0.0.0/8",
		"10.0.0.0/8, 172.16.0.0/12",
	} {
		if _, err := parsePrivateCIDRs(invalid); err == nil {
			t.Fatalf("invalid private CIDR list accepted: %q", invalid)
		}
	}
}
