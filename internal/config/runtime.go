package config

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

type Runtime struct {
	APIToken        string // #nosec G117 -- configuration secrets are intentionally held in memory and never serialized.
	ConfirmationKey string // #nosec G117 -- configuration secrets are intentionally held in memory and never serialized.
	RouterOS        Endpoint
	Mihomo          Mihomo
	MosDNSURL       string
	BackupDir       string
}

type Endpoint struct {
	URL      string
	Username string
	Password string // #nosec G117 -- RouterOS authentication requires this secret-bearing field.
}

type Mihomo struct {
	URL               string
	ProxyURL          string
	Secret            string // #nosec G117 -- Mihomo authentication requires this secret-bearing field.
	BaseConfigPath    string
	LocalConfigPath   string
	RuntimeConfigPath string
	BackupDir         string
	ValidatorBinary   string
}

func Load() (Runtime, error) {
	cfg := Runtime{
		APIToken:        os.Getenv("FOXOS_API_TOKEN"),
		ConfirmationKey: os.Getenv("FOXOS_CONFIRMATION_KEY"),
		RouterOS:        Endpoint{URL: os.Getenv("FOXOS_ROUTEROS_URL"), Username: os.Getenv("FOXOS_ROUTEROS_USERNAME"), Password: os.Getenv("FOXOS_ROUTEROS_PASSWORD")},
		Mihomo:          Mihomo{URL: os.Getenv("FOXOS_MIHOMO_URL"), ProxyURL: os.Getenv("FOXOS_MIHOMO_PROXY_URL"), Secret: os.Getenv("FOXOS_MIHOMO_SECRET"), BaseConfigPath: os.Getenv("FOXOS_MIHOMO_BASE_CONFIG"), LocalConfigPath: os.Getenv("FOXOS_MIHOMO_LOCAL_CONFIG"), RuntimeConfigPath: os.Getenv("FOXOS_MIHOMO_RUNTIME_CONFIG"), BackupDir: os.Getenv("FOXOS_MIHOMO_BACKUP_DIR"), ValidatorBinary: os.Getenv("FOXOS_MIHOMO_VALIDATOR_BINARY")},
		MosDNSURL:       os.Getenv("FOXOS_MOSDNS_URL"),
		BackupDir:       os.Getenv("FOXOS_BACKUP_DIR"),
	}
	if cfg.BackupDir == "" {
		cfg.BackupDir = "backups"
	}
	if cfg.Mihomo.URL != "" {
		if cfg.Mihomo.BaseConfigPath == "" {
			cfg.Mihomo.BaseConfigPath = "/data/mihomo/base.yaml"
		}
		if cfg.Mihomo.ValidatorBinary == "" {
			cfg.Mihomo.ValidatorBinary = "/usr/local/bin/mihomo"
		}
	}
	if len(cfg.APIToken) < 32 {
		return Runtime{}, errors.New("FOXOS_API_TOKEN must contain at least 32 characters")
	}
	if len(cfg.ConfirmationKey) < 32 {
		return Runtime{}, errors.New("FOXOS_CONFIRMATION_KEY must contain at least 32 characters")
	}
	if cfg.APIToken == cfg.ConfirmationKey {
		return Runtime{}, errors.New("FOXOS_API_TOKEN and FOXOS_CONFIRMATION_KEY must differ")
	}
	if strings.TrimSpace(cfg.APIToken) != cfg.APIToken || strings.TrimSpace(cfg.ConfirmationKey) != cfg.ConfirmationKey {
		return Runtime{}, errors.New("FoxOS secrets must not contain surrounding whitespace")
	}
	if err := validateOptionalEndpoint(cfg.RouterOS.URL); err != nil {
		return Runtime{}, errors.New("invalid FOXOS_ROUTEROS_URL")
	}
	if cfg.RouterOS.URL != "" && (cfg.RouterOS.Username == "" || cfg.RouterOS.Password == "") {
		return Runtime{}, errors.New("RouterOS credentials are required when RouterOS is configured")
	}
	if err := validateOptionalEndpoint(cfg.Mihomo.URL); err != nil {
		return Runtime{}, errors.New("invalid FOXOS_MIHOMO_URL")
	}
	if cfg.Mihomo.URL != "" {
		if len(cfg.Mihomo.Secret) < 32 || len(cfg.Mihomo.Secret) > 4096 || strings.TrimSpace(cfg.Mihomo.Secret) != cfg.Mihomo.Secret {
			return Runtime{}, errors.New("FOXOS_MIHOMO_SECRET must contain 32 to 4096 characters without surrounding whitespace")
		}
	}
	if err := validateOptionalEndpoint(cfg.Mihomo.ProxyURL); err != nil {
		return Runtime{}, errors.New("invalid FOXOS_MIHOMO_PROXY_URL")
	}
	if err := validateOptionalEndpoint(cfg.MosDNSURL); err != nil {
		return Runtime{}, errors.New("invalid FOXOS_MOSDNS_URL")
	}
	if cfg.Mihomo.URL != "" && (cfg.Mihomo.BaseConfigPath == "" || cfg.Mihomo.LocalConfigPath == "" || cfg.Mihomo.RuntimeConfigPath == "" || cfg.Mihomo.BackupDir == "" || cfg.Mihomo.ValidatorBinary == "") {
		return Runtime{}, errors.New("Mihomo base, runtime, validator and backup paths are required when Mihomo is configured")
	}
	return cfg, nil
}

func validateOptionalEndpoint(value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value {
		return errors.New("endpoint contains surrounding whitespace")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return errors.New("invalid endpoint")
	}
	host := parsed.Hostname()
	if host == "" || strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".local") {
		return errors.New("endpoint host is not allowed")
	}
	ip := net.ParseIP(host)
	if ip == nil || !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return errors.New("endpoint must use a private or loopback IP literal")
	}
	return nil
}
