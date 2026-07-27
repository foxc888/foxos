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
	if cfg.Site.PublicHostname != "foxos.home.arpa" || cfg.HTTPS.Enabled {
		t.Fatalf("unexpected site or HTTPS defaults: %+v", cfg)
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
	t.Setenv("FOXOS_API_TOKEN", strings.Repeat("a", 32))
	t.Setenv("FOXOS_CONFIRMATION_KEY", strings.Repeat("b", 32))
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
	t.Setenv("FOXOS_API_TOKEN", strings.Repeat("a", 32))
	t.Setenv("FOXOS_CONFIRMATION_KEY", strings.Repeat("b", 32))
	t.Setenv("FOXOS_ROUTEROS_URL", "http://192.168.1.1")
	t.Setenv("FOXOS_ROUTEROS_USERNAME", "foxos")
	t.Setenv("FOXOS_ROUTEROS_PASSWORD", strings.Repeat("c", 32))
	if _, err := Load(); err == nil {
		t.Fatal("expected endpoint/site mismatch")
	}
}
