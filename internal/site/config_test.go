package site

import (
	"testing"
)

func TestLoadUsesOneCompleteSiteManifest(t *testing.T) {
	defaults, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if defaults != Default() || defaults.PublicURL() != "https://foxos.home.arpa" {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}
	values := map[string]string{
		"FOXOS_SITE_MANAGEMENT_BRIDGE": "lan",
		"FOXOS_SITE_STORAGE_ROOT":      "usb1",
		"FOXOS_SITE_NETWORK":           "192.168.40.0/24",
		"FOXOS_SITE_ROUTER_ADDRESS":    "192.168.40.1",
		"FOXOS_SITE_MIHOMO_ADDRESS":    "192.168.40.2",
		"FOXOS_SITE_MOSDNS_ADDRESS":    "192.168.40.3",
		"FOXOS_SITE_FOXOS_ADDRESS":     "192.168.40.4",
		"FOXOS_SITE_PUBLIC_HOSTNAME":   "foxos.home.arpa",
	}
	configured, err := Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if configured.ManagementBridge != "lan" || configured.MihomoURL() != "http://192.168.40.2:9090" {
		t.Fatalf("unexpected configured site: %+v", configured)
	}
	delete(values, "FOXOS_SITE_NETWORK")
	if _, err := Load(func(key string) string { return values[key] }); err == nil {
		t.Fatal("expected partial manifest rejection")
	}
}

func TestValidateRejectsUnsafeTopology(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "host outside network", mutate: func(c *Config) { c.FoxOSAddress = "192.168.1.4" }},
		{name: "duplicate host", mutate: func(c *Config) { c.MosDNSAddress = c.MihomoAddress }},
		{name: "broadcast", mutate: func(c *Config) { c.FoxOSAddress = "10.0.0.255" }},
		{name: "non canonical network", mutate: func(c *Config) { c.Network = "10.0.0.1/24" }},
		{name: "public suffix", mutate: func(c *Config) { c.PublicHostname = "foxos.example.com" }},
		{name: "unsafe storage", mutate: func(c *Config) { c.StorageRoot = "../disk1" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := Default()
			test.mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
