package routeros

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/site"
)

type Client struct {
	base     *url.URL
	username string
	password string
	http     *http.Client
	site     site.Config
}

func NewClient(endpoint, username, password string, siteConfigs ...site.Config) (*Client, error) {
	base, err := url.Parse(endpoint)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil {
		return nil, errors.New("invalid RouterOS REST endpoint")
	}
	if !privateEndpointHost(base.Hostname()) {
		return nil, errors.New("RouterOS endpoint must use a private or loopback address")
	}
	if username == "" || password == "" {
		return nil, errors.New("RouterOS credentials are required")
	}
	if len(siteConfigs) > 1 {
		return nil, errors.New("at most one site configuration is allowed")
	}
	siteConfig := site.Default()
	if len(siteConfigs) == 1 {
		siteConfig = siteConfigs[0]
	}
	if err := siteConfig.Validate(); err != nil {
		return nil, err
	}
	return &Client{base: base, username: username, password: password, site: siteConfig, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func privateEndpointHost(host string) bool {
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".local") {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

type Resource struct {
	Version      string `json:"version"`
	Architecture string `json:"architecture-name"`
	BoardName    string `json:"board-name"`
	CPU          string `json:"cpu"`
	CPUCount     string `json:"cpu-count"`
	CPULoad      string `json:"cpu-load"`
	FreeMemory   string `json:"free-memory"`
	TotalMemory  string `json:"total-memory"`
	FreeHDD      string `json:"free-hdd-space"`
	TotalHDD     string `json:"total-hdd-space"`
	Uptime       string `json:"uptime"`
}

type Interface struct {
	ID         string `json:".id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	MACAddress string `json:"mac-address"`
	Running    string `json:"running"`
	Disabled   string `json:"disabled"`
	RXByte     string `json:"rx-byte"`
	TXByte     string `json:"tx-byte"`
}

type Lease struct {
	ID         string `json:".id"`
	Address    string `json:"address"`
	MACAddress string `json:"mac-address"`
	HostName   string `json:"host-name"`
	Status     string `json:"status"`
	Dynamic    string `json:"dynamic"`
	Server     string `json:"server"`
	LastSeen   string `json:"last-seen"`
	Comment    string `json:"comment"`
	Disabled   string `json:"disabled"`
}

type L2TPClient struct {
	ID                   string `json:".id"`
	Name                 string `json:"name"`
	ConnectTo            string `json:"connect-to"`
	User                 string `json:"user"`
	Running              string `json:"running"`
	Disabled             string `json:"disabled"`
	Comment              string `json:"comment"`
	AddDefaultRoute      string `json:"add-default-route"`
	DefaultRouteDistance string `json:"default-route-distance"`
	UsePeerDNS           string `json:"use-peer-dns"`
	Profile              string `json:"profile"`
}

type ARP struct {
	ID         string `json:".id"`
	Address    string `json:"address"`
	MACAddress string `json:"mac-address"`
	Interface  string `json:"interface"`
	Complete   string `json:"complete"`
}

type Route struct {
	ID       string `json:".id"`
	Dst      string `json:"dst-address"`
	Gateway  string `json:"gateway"`
	Distance string `json:"distance"`
	Active   string `json:"active"`
	Disabled string `json:"disabled"`
	Comment  string `json:"comment"`
}

type DHCPServer struct {
	ID          string `json:".id"`
	Name        string `json:"name"`
	Interface   string `json:"interface"`
	AddressPool string `json:"address-pool"`
	Disabled    string `json:"disabled"`
	Running     string `json:"running"`
}

type IPPool struct {
	ID       string `json:".id"`
	Name     string `json:"name"`
	Ranges   string `json:"ranges"`
	NextPool string `json:"next-pool"`
	Comment  string `json:"comment"`
}

type DHCPNetwork struct {
	ID      string `json:".id"`
	Address string `json:"address"`
	Gateway string `json:"gateway"`
	Comment string `json:"comment"`
}

type IPAddress struct {
	ID        string `json:".id"`
	Address   string `json:"address"`
	Network   string `json:"network"`
	Interface string `json:"interface"`
	Dynamic   string `json:"dynamic"`
	Disabled  string `json:"disabled"`
	Comment   string `json:"comment"`
}

type BindingState struct {
	Leases             []Lease       `json:"leases"`
	Pools              []IPPool      `json:"pools"`
	Networks           []DHCPNetwork `json:"networks"`
	Addresses          []IPAddress   `json:"addresses"`
	ARP                []ARP         `json:"arp"`
	DHCPServers        []DHCPServer  `json:"dhcpServers"`
	ProtectedAddresses []string      `json:"protectedAddresses,omitempty"`
}

type Container struct {
	ID          string `json:".id"`
	Name        string `json:"name"`
	Comment     string `json:"comment"`
	Status      string `json:"status"`
	RootDir     string `json:"root-dir"`
	Interface   string `json:"interface"`
	StartOnBoot string `json:"start-on-boot"`
}

func (c *Client) Resource(ctx context.Context) (Resource, error) {
	var records []Resource
	if err := c.get(ctx, "/rest/system/resource", &records); err != nil {
		return Resource{}, err
	}
	if len(records) != 1 {
		return Resource{}, fmt.Errorf("RouterOS resource response count %d", len(records))
	}
	return records[0], nil
}
func (c *Client) Interfaces(ctx context.Context) ([]Interface, error) {
	var out []Interface
	err := c.get(ctx, "/rest/interface", &out)
	return out, err
}
func (c *Client) Leases(ctx context.Context) ([]Lease, error) {
	var out []Lease
	err := c.get(ctx, "/rest/ip/dhcp-server/lease", &out)
	return out, err
}
func (c *Client) ARP(ctx context.Context) ([]ARP, error) {
	var out []ARP
	err := c.get(ctx, "/rest/ip/arp", &out)
	return out, err
}
func (c *Client) L2TPClients(ctx context.Context) ([]L2TPClient, error) {
	var out []L2TPClient
	err := c.get(ctx, "/rest/interface/l2tp-client", &out)
	return out, err
}
func (c *Client) Routes(ctx context.Context) ([]Route, error) {
	var out []Route
	err := c.get(ctx, "/rest/ip/route", &out)
	return out, err
}
func (c *Client) DHCPServers(ctx context.Context) ([]DHCPServer, error) {
	var out []DHCPServer
	err := c.get(ctx, "/rest/ip/dhcp-server", &out)
	return out, err
}
func (c *Client) IPPools(ctx context.Context) ([]IPPool, error) {
	var out []IPPool
	err := c.get(ctx, "/rest/ip/pool", &out)
	return out, err
}
func (c *Client) DHCPNetworks(ctx context.Context) ([]DHCPNetwork, error) {
	var out []DHCPNetwork
	err := c.get(ctx, "/rest/ip/dhcp-server/network", &out)
	return out, err
}
func (c *Client) IPAddresses(ctx context.Context) ([]IPAddress, error) {
	var out []IPAddress
	err := c.get(ctx, "/rest/ip/address", &out)
	return out, err
}
func (c *Client) BindingState(ctx context.Context) (BindingState, error) {
	state := BindingState{ProtectedAddresses: c.site.ProtectedAddresses()}
	var err error
	if state.Leases, err = c.Leases(ctx); err != nil {
		return BindingState{}, err
	}
	if state.Pools, err = c.IPPools(ctx); err != nil {
		return BindingState{}, err
	}
	if state.Networks, err = c.DHCPNetworks(ctx); err != nil {
		return BindingState{}, err
	}
	if state.Addresses, err = c.IPAddresses(ctx); err != nil {
		return BindingState{}, err
	}
	if state.ARP, err = c.ARP(ctx); err != nil {
		return BindingState{}, err
	}
	if state.DHCPServers, err = c.DHCPServers(ctx); err != nil {
		return BindingState{}, err
	}
	return state, nil
}
func (c *Client) Containers(ctx context.Context) ([]Container, error) {
	var out []Container
	err := c.get(ctx, "/rest/container", &out)
	return out, err
}

func (c *Client) get(ctx context.Context, path string, destination any) error {
	if !allowedReadPath(path) {
		return errors.New("RouterOS path is not allowed")
	}
	target := *c.base
	target.Path = strings.TrimRight(c.base.Path, "/") + path
	target.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth(c.username, c.password)
	request.Header.Set("Accept", "application/json")
	// #nosec G704 -- the base URL is a validated private IP literal, the REST
	// path is selected from allowedReadPath, and redirects are disabled.
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return errors.New("RouterOS authentication failed")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("RouterOS status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode RouterOS response: %w", err)
	}
	return nil
}
func allowedReadPath(path string) bool {
	switch path {
	case "/rest/system/resource", "/rest/interface", "/rest/ip/dhcp-server/lease", "/rest/ip/arp", "/rest/interface/l2tp-client", "/rest/ip/route", "/rest/ip/dhcp-server", "/rest/ip/dhcp-server/network", "/rest/ip/pool", "/rest/ip/address", "/rest/container", "/rest/routing/table":
		return true
	case "/rest/ip/firewall/address-list", "/rest/ip/firewall/mangle", "/rest/ip/firewall/filter":
		return true
	}
	return false
}
