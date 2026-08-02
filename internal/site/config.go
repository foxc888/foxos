package site

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	RouterOSPort         = 80
	MihomoControllerPort = 9090
	MihomoProxyPort      = 7890
	MosDNSPort           = 53
	FoxOSInternalPort    = 8090
)

var routerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

type Config struct {
	ManagementBridge string `json:"managementBridge"`
	StorageRoot      string `json:"storageRoot"`
	Network          string `json:"network"`
	RouterAddress    string `json:"routerAddress"`
	MihomoAddress    string `json:"mihomoAddress"`
	MosDNSAddress    string `json:"mosdnsAddress"`
	FoxOSAddress     string `json:"foxosAddress"`
	PublicHostname   string `json:"publicHostname"`
}

func Default() Config {
	return Config{
		ManagementBridge: "bridge-lan",
		StorageRoot:      "disk1",
		Network:          "10.0.0.0/24",
		RouterAddress:    "10.0.0.1",
		MihomoAddress:    "10.0.0.2",
		MosDNSAddress:    "10.0.0.3",
		FoxOSAddress:     "10.0.0.4",
		PublicHostname:   "foxos.home.arpa",
	}
}

func Load(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, errors.New("site environment reader is required")
	}
	keys := []struct {
		name  string
		apply func(*Config, string)
	}{
		{name: "FOXOS_SITE_MANAGEMENT_BRIDGE", apply: func(c *Config, value string) { c.ManagementBridge = value }},
		{name: "FOXOS_SITE_STORAGE_ROOT", apply: func(c *Config, value string) { c.StorageRoot = value }},
		{name: "FOXOS_SITE_NETWORK", apply: func(c *Config, value string) { c.Network = value }},
		{name: "FOXOS_SITE_ROUTER_ADDRESS", apply: func(c *Config, value string) { c.RouterAddress = value }},
		{name: "FOXOS_SITE_MIHOMO_ADDRESS", apply: func(c *Config, value string) { c.MihomoAddress = value }},
		{name: "FOXOS_SITE_MOSDNS_ADDRESS", apply: func(c *Config, value string) { c.MosDNSAddress = value }},
		{name: "FOXOS_SITE_FOXOS_ADDRESS", apply: func(c *Config, value string) { c.FoxOSAddress = value }},
		{name: "FOXOS_SITE_PUBLIC_HOSTNAME", apply: func(c *Config, value string) { c.PublicHostname = value }},
	}
	configured := 0
	config := Config{}
	for _, item := range keys {
		value := strings.TrimSpace(getenv(item.name))
		if value == "" {
			continue
		}
		configured++
		item.apply(&config, value)
	}
	if configured == 0 {
		config = Default()
	} else if configured != len(keys) {
		return Config{}, errors.New("site configuration must set every FOXOS_SITE_* field or none")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c Config) Validate() error {
	if !routerNamePattern.MatchString(c.ManagementBridge) {
		return errors.New("site management bridge is invalid")
	}
	if !routerNamePattern.MatchString(c.StorageRoot) {
		return errors.New("site storage root is invalid")
	}
	networkIP, networkRange, err := net.ParseCIDR(c.Network)
	if err != nil || networkIP.To4() == nil || networkIP.String() != networkRange.IP.String() {
		return errors.New("site network must be a canonical IPv4 CIDR")
	}
	addresses := []struct {
		name  string
		value string
	}{
		{name: "RouterOS", value: c.RouterAddress},
		{name: "Mihomo", value: c.MihomoAddress},
		{name: "MosDNS", value: c.MosDNSAddress},
		{name: "FoxOS", value: c.FoxOSAddress},
	}
	seen := make(map[string]string, len(addresses))
	last := lastAddress(networkRange)
	for _, item := range addresses {
		ip := net.ParseIP(item.value)
		if ip == nil || ip.To4() == nil || ip.String() != item.value || !networkRange.Contains(ip) {
			return fmt.Errorf("site %s address must be a canonical IPv4 inside %s", item.name, c.Network)
		}
		if ip.Equal(networkRange.IP) || ip.Equal(last) {
			return fmt.Errorf("site %s address cannot be the network or broadcast address", item.name)
		}
		if previous, exists := seen[item.value]; exists {
			return fmt.Errorf("site addresses for %s and %s overlap", previous, item.name)
		}
		seen[item.value] = item.name
	}
	hostname := strings.ToLower(strings.TrimSuffix(c.PublicHostname, "."))
	if hostname != c.PublicHostname || !strings.HasSuffix(hostname, ".home.arpa") || len(hostname) > 253 {
		return errors.New("site public hostname must be a lowercase name below home.arpa")
	}
	if parsed := net.ParseIP(hostname); parsed != nil {
		return errors.New("site public hostname cannot be an IP address")
	}
	for _, label := range strings.Split(hostname, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return errors.New("site public hostname contains an invalid DNS label")
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
				return errors.New("site public hostname contains an invalid DNS label")
			}
		}
	}
	return nil
}

func (c Config) ProtectedAddresses() []string {
	return []string{c.RouterAddress, c.MihomoAddress, c.MosDNSAddress, c.FoxOSAddress}
}

func (c Config) RouterOSURL() string {
	return "http://" + net.JoinHostPort(c.RouterAddress, strconv.Itoa(RouterOSPort))
}

func (c Config) MihomoURL() string {
	return "http://" + net.JoinHostPort(c.MihomoAddress, strconv.Itoa(MihomoControllerPort))
}

func (c Config) MihomoProxyURL() string {
	return "http://" + net.JoinHostPort(c.MihomoAddress, strconv.Itoa(MihomoProxyPort))
}

func (c Config) MosDNSURL() string {
	return "http://" + net.JoinHostPort(c.MosDNSAddress, strconv.Itoa(MosDNSPort))
}

func (c Config) InternalURL() string {
	return "http://" + net.JoinHostPort(c.FoxOSAddress, strconv.Itoa(FoxOSInternalPort))
}

func (c Config) PublicURL() string { return "https://" + c.PublicHostname }

func (c Config) HTTPRedirectURL() string { return "http://" + c.FoxOSAddress }

func (c Config) EndpointMatches(endpoint, expectedHost string, expectedPort int) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() != expectedHost {
		return false
	}
	port := parsed.Port()
	if port == "" {
		port = "80"
	}
	return port == strconv.Itoa(expectedPort)
}

func lastAddress(network *net.IPNet) net.IP {
	ip := append(net.IP(nil), network.IP.To4()...)
	mask := network.Mask
	for index := range ip {
		ip[index] |= ^mask[index]
	}
	return ip
}
