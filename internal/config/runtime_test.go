package config

import (
	"strings"
	"testing"
)

func TestLoadRequiresToken(t *testing.T) {
	t.Setenv("FOXOS_API_TOKEN", "")
	t.Setenv("FOXOS_CONFIRMATION_KEY", "01234567890123456789012345678901")
	if _, err := Load(); err == nil {
		t.Fatal("expected token error")
	}
}
func TestLoadOptionalAdapters(t *testing.T) {
	t.Setenv("FOXOS_API_TOKEN", "01234567890123456789012345678901")
	t.Setenv("FOXOS_CONFIRMATION_KEY", "abcdefghijklmnopqrstuvwxyz012345")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RouterOS.URL != "" || cfg.Mihomo.URL != "" {
		t.Fatalf("cfg=%+v", cfg)
	}
}
func TestLoadRejectsCredentialsInURL(t *testing.T) {
	t.Setenv("FOXOS_API_TOKEN", "01234567890123456789012345678901")
	t.Setenv("FOXOS_CONFIRMATION_KEY", "abcdefghijklmnopqrstuvwxyz012345")
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
		{name: "valid", secret: strings.Repeat("m", 32)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("FOXOS_API_TOKEN", strings.Repeat("a", 32))
			t.Setenv("FOXOS_CONFIRMATION_KEY", strings.Repeat("c", 32))
			t.Setenv("FOXOS_MIHOMO_URL", "http://10.0.0.2:9090")
			t.Setenv("FOXOS_MIHOMO_SECRET", test.secret)
			t.Setenv("FOXOS_MIHOMO_LOCAL_CONFIG", "/var/lib/foxos/mihomo/config.yaml")
			t.Setenv("FOXOS_MIHOMO_RUNTIME_CONFIG", "/root/.config/mihomo/config.yaml")
			t.Setenv("FOXOS_MIHOMO_BACKUP_DIR", "/var/lib/foxos/mihomo/backups")
			_, err := Load()
			if (err != nil) != test.wantErr {
				t.Fatalf("Load() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}
