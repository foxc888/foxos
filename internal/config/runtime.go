package config

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/foxc888/foxos/internal/site"
)

type Runtime struct {
	Production               bool
	APIToken                 string // #nosec G117 -- configuration secrets are intentionally held in memory and never serialized.
	ConfirmationKey          string // #nosec G117 -- configuration secrets are intentionally held in memory and never serialized.
	RouterOS                 Endpoint
	Mihomo                   Mihomo
	MosDNSURL                string
	BackupDir                string
	UpgradeStatePath         string
	Site                     site.Config
	HTTPS                    HTTPS
	SubscriptionPrivateCIDRs []netip.Prefix
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

type HTTPS struct {
	Enabled            bool
	CertDir            string
	InternalListen     string
	PublicListen       string
	HTTPRedirectListen string
}

func Load() (Runtime, error) {
	return load(productionSecretDirectory)
}

func load(secretDirectory string) (Runtime, error) {
	siteConfig, err := site.Load(os.Getenv)
	if err != nil {
		return Runtime{}, err
	}
	https, err := loadHTTPS()
	if err != nil {
		return Runtime{}, err
	}
	production := strings.EqualFold(strings.TrimSpace(os.Getenv("FOXOS_ENV")), "production")
	if production {
		if err := rejectProductionSecretEnvironment(os.LookupEnv); err != nil {
			return Runtime{}, err
		}
	}
	cfg := Runtime{
		Production:       production,
		RouterOS:         Endpoint{URL: os.Getenv("FOXOS_ROUTEROS_URL"), Username: os.Getenv("FOXOS_ROUTEROS_USERNAME")},
		Mihomo:           Mihomo{URL: os.Getenv("FOXOS_MIHOMO_URL"), ProxyURL: os.Getenv("FOXOS_MIHOMO_PROXY_URL"), BaseConfigPath: os.Getenv("FOXOS_MIHOMO_BASE_CONFIG"), LocalConfigPath: os.Getenv("FOXOS_MIHOMO_LOCAL_CONFIG"), RuntimeConfigPath: os.Getenv("FOXOS_MIHOMO_RUNTIME_CONFIG"), BackupDir: os.Getenv("FOXOS_MIHOMO_BACKUP_DIR"), ValidatorBinary: os.Getenv("FOXOS_MIHOMO_VALIDATOR_BINARY")},
		MosDNSURL:        os.Getenv("FOXOS_MOSDNS_URL"),
		BackupDir:        os.Getenv("FOXOS_BACKUP_DIR"),
		UpgradeStatePath: os.Getenv("FOXOS_UPGRADE_STATE_PATH"),
		Site:             siteConfig,
		HTTPS:            https,
	}
	if cfg.APIToken, err = loadRuntimeSecret(production, "FOXOS_API_TOKEN", "api-token", secretDirectory, os.Getenv); err != nil {
		return Runtime{}, err
	}
	if cfg.ConfirmationKey, err = loadRuntimeSecret(production, "FOXOS_CONFIRMATION_KEY", "confirmation-key", secretDirectory, os.Getenv); err != nil {
		return Runtime{}, err
	}
	if production || cfg.RouterOS.URL != "" {
		if cfg.RouterOS.Password, err = loadRuntimeSecret(production, "FOXOS_ROUTEROS_PASSWORD", "routeros-password", secretDirectory, os.Getenv); err != nil {
			return Runtime{}, err
		}
	}
	if production || cfg.Mihomo.URL != "" {
		if cfg.Mihomo.Secret, err = loadRuntimeSecret(production, "FOXOS_MIHOMO_SECRET", "mihomo-secret", secretDirectory, os.Getenv); err != nil {
			return Runtime{}, err
		}
	}
	environment := strings.ToLower(strings.TrimSpace(os.Getenv("FOXOS_ENV")))
	if environment == "" {
		environment = "development"
	}
	if environment != "development" && environment != "production" {
		return Runtime{}, errors.New("FOXOS_ENV must be development or production")
	}
	privateCIDRs, err := parsePrivateCIDRs(os.Getenv("FOXOS_SUBSCRIPTION_PRIVATE_CIDRS"))
	if err != nil {
		return Runtime{}, err
	}
	cfg.SubscriptionPrivateCIDRs = privateCIDRs
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
	if err := validateSecret("FOXOS_API_TOKEN", cfg.APIToken, 32); err != nil {
		return Runtime{}, err
	}
	if err := validateSecret("FOXOS_CONFIRMATION_KEY", cfg.ConfirmationKey, 32); err != nil {
		return Runtime{}, err
	}
	if err := validateDistinctSecrets(
		namedSecret{name: "FOXOS_API_TOKEN", value: cfg.APIToken},
		namedSecret{name: "FOXOS_CONFIRMATION_KEY", value: cfg.ConfirmationKey},
		namedSecret{name: "FOXOS_ROUTEROS_PASSWORD", value: cfg.RouterOS.Password},
		namedSecret{name: "FOXOS_MIHOMO_SECRET", value: cfg.Mihomo.Secret},
	); err != nil {
		return Runtime{}, err
	}
	if err := validateOptionalEndpoint(cfg.RouterOS.URL); err != nil {
		return Runtime{}, errors.New("invalid FOXOS_ROUTEROS_URL")
	}
	if cfg.RouterOS.URL != "" && (cfg.RouterOS.Username == "" || cfg.RouterOS.Password == "") {
		return Runtime{}, errors.New("RouterOS credentials are required when RouterOS is configured")
	}
	if cfg.RouterOS.URL != "" && strings.EqualFold(cfg.RouterOS.Username, "admin") {
		return Runtime{}, errors.New("FOXOS_ROUTEROS_USERNAME must use a dedicated least-privilege account")
	}
	if production || cfg.RouterOS.URL != "" {
		if err := validateSecret("FOXOS_ROUTEROS_PASSWORD", cfg.RouterOS.Password, 32); err != nil {
			return Runtime{}, err
		}
	}
	if cfg.RouterOS.URL != "" && !cfg.Site.EndpointMatches(cfg.RouterOS.URL, cfg.Site.RouterAddress, site.RouterOSPort) {
		return Runtime{}, errors.New("FOXOS_ROUTEROS_URL does not match the site manifest")
	}
	if err := validateOptionalEndpoint(cfg.Mihomo.URL); err != nil {
		return Runtime{}, errors.New("invalid FOXOS_MIHOMO_URL")
	}
	if production || cfg.Mihomo.URL != "" {
		if err := validateSecret("FOXOS_MIHOMO_SECRET", cfg.Mihomo.Secret, 32); err != nil {
			return Runtime{}, err
		}
	}
	if cfg.Mihomo.URL != "" && !cfg.Site.EndpointMatches(cfg.Mihomo.URL, cfg.Site.MihomoAddress, site.MihomoControllerPort) {
		return Runtime{}, errors.New("FOXOS_MIHOMO_URL does not match the site manifest")
	}
	if err := validateOptionalEndpoint(cfg.Mihomo.ProxyURL); err != nil {
		return Runtime{}, errors.New("invalid FOXOS_MIHOMO_PROXY_URL")
	}
	if cfg.Mihomo.ProxyURL != "" && !cfg.Site.EndpointMatches(cfg.Mihomo.ProxyURL, cfg.Site.MihomoAddress, site.MihomoProxyPort) {
		return Runtime{}, errors.New("FOXOS_MIHOMO_PROXY_URL does not match the site manifest")
	}
	if err := validateOptionalEndpoint(cfg.MosDNSURL); err != nil {
		return Runtime{}, errors.New("invalid FOXOS_MOSDNS_URL")
	}
	if cfg.MosDNSURL != "" && !cfg.Site.EndpointMatches(cfg.MosDNSURL, cfg.Site.MosDNSAddress, site.MosDNSPort) {
		return Runtime{}, errors.New("FOXOS_MOSDNS_URL does not match the site manifest")
	}
	if cfg.Mihomo.URL != "" && (cfg.Mihomo.BaseConfigPath == "" || cfg.Mihomo.LocalConfigPath == "" || cfg.Mihomo.RuntimeConfigPath == "" || cfg.Mihomo.BackupDir == "" || cfg.Mihomo.ValidatorBinary == "") {
		return Runtime{}, errors.New("Mihomo base, runtime, validator and backup paths are required when Mihomo is configured")
	}
	if cfg.Production {
		if !cfg.HTTPS.Enabled {
			return Runtime{}, errors.New("FOXOS_HTTPS_ENABLED must be true in production")
		}
		for name, path := range map[string]string{"FOXOS_BACKUP_DIR": cfg.BackupDir, "FOXOS_TLS_DIR": cfg.HTTPS.CertDir} {
			if !filepath.IsAbs(path) {
				return Runtime{}, errors.New(name + " must be an absolute persistent path in production")
			}
		}
		if cfg.UpgradeStatePath != "" && !filepath.IsAbs(cfg.UpgradeStatePath) {
			return Runtime{}, errors.New("FOXOS_UPGRADE_STATE_PATH must be absolute in production")
		}
	}
	return cfg, nil
}

func validateSecret(name, value string, minimum int) error {
	if len(value) < minimum || len(value) > 4096 || strings.TrimSpace(value) != value {
		return errors.New(name + " must contain " + strconv.Itoa(minimum) + " to 4096 characters without surrounding whitespace")
	}
	unique := make(map[byte]struct{}, 16)
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return errors.New(name + " contains non-printable, non-ASCII, or whitespace characters")
		}
		unique[value[index]] = struct{}{}
	}
	if len(unique) < 8 || repeatedSecret(value) {
		return errors.New(name + " is a low-entropy or repeated value")
	}
	normalized := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(value))
	for _, unsafe := range []string{"changeme", "password", "defaultsecret", "defaulttoken", "exampletoken", "insecure"} {
		if strings.Contains(normalized, unsafe) {
			return errors.New(name + " contains a known unsafe placeholder")
		}
	}
	return nil
}

func repeatedSecret(value string) bool {
	for period := 1; period <= 16 && period*2 <= len(value); period++ {
		if len(value)%period == 0 && strings.Repeat(value[:period], len(value)/period) == value {
			return true
		}
	}
	return false
}

func parsePrivateCIDRs(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 32 {
		return nil, errors.New("FOXOS_SUBSCRIPTION_PRIVATE_CIDRS exceeds 32 entries")
	}
	result := make([]netip.Prefix, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != part || part == "" {
			return nil, errors.New("FOXOS_SUBSCRIPTION_PRIVATE_CIDRS contains an invalid entry")
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil || prefix != prefix.Masked() || !prefix.Addr().IsPrivate() {
			return nil, errors.New("FOXOS_SUBSCRIPTION_PRIVATE_CIDRS accepts only canonical RFC1918 or IPv6 ULA prefixes")
		}
		key := prefix.String()
		if _, duplicate := seen[key]; duplicate {
			return nil, errors.New("FOXOS_SUBSCRIPTION_PRIVATE_CIDRS contains a duplicate prefix")
		}
		seen[key] = struct{}{}
		result = append(result, prefix)
	}
	return result, nil
}

func loadHTTPS() (HTTPS, error) {
	rawEnabled := strings.TrimSpace(os.Getenv("FOXOS_HTTPS_ENABLED"))
	if rawEnabled == "" {
		return HTTPS{}, nil
	}
	enabled, err := strconv.ParseBool(rawEnabled)
	if err != nil {
		return HTTPS{}, errors.New("FOXOS_HTTPS_ENABLED must be true or false")
	}
	if !enabled {
		return HTTPS{}, nil
	}
	config := HTTPS{
		Enabled:            true,
		CertDir:            strings.TrimSpace(os.Getenv("FOXOS_TLS_DIR")),
		InternalListen:     strings.TrimSpace(os.Getenv("FOXOS_INTERNAL_LISTEN")),
		PublicListen:       strings.TrimSpace(os.Getenv("FOXOS_HTTPS_LISTEN")),
		HTTPRedirectListen: strings.TrimSpace(os.Getenv("FOXOS_HTTP_REDIRECT_LISTEN")),
	}
	if config.CertDir == "" {
		config.CertDir = "/data/tls"
	}
	if config.InternalListen == "" {
		config.InternalListen = "127.0.0.1:8090"
	}
	if config.PublicListen == "" {
		config.PublicListen = ":443"
	}
	if config.HTTPRedirectListen == "" {
		config.HTTPRedirectListen = ":80"
	}
	if strings.ContainsRune(config.CertDir, '\x00') {
		return HTTPS{}, errors.New("FOXOS_TLS_DIR is invalid")
	}
	if err := validateListen(config.InternalListen, 8090, true); err != nil {
		return HTTPS{}, errors.New("FOXOS_INTERNAL_LISTEN must be a loopback address on port 8090")
	}
	if err := validateListen(config.PublicListen, 443, false); err != nil {
		return HTTPS{}, errors.New("FOXOS_HTTPS_LISTEN must use port 443")
	}
	if err := validateListen(config.HTTPRedirectListen, 80, false); err != nil {
		return HTTPS{}, errors.New("FOXOS_HTTP_REDIRECT_LISTEN must use port 80")
	}
	return config, nil
}

func validateListen(value string, expectedPort int, requireLoopback bool) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || port != strconv.Itoa(expectedPort) {
		return errors.New("invalid listen address")
	}
	if !requireLoopback {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("listen address is not loopback")
	}
	return nil
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
