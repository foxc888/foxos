package mosdns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Status struct {
	Configured bool   `json:"configured"`
	Online     bool   `json:"online"`
	Address    string `json:"address,omitempty"`
	Transport  string `json:"transport,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Client struct {
	base *url.URL
	http *http.Client
}

func NewClient(endpoint string) (*Client, error) {
	if strings.TrimSpace(endpoint) != endpoint || endpoint == "" {
		return nil, errors.New("MosDNS endpoint is required")
	}
	base, err := url.Parse(endpoint)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.Fragment != "" {
		return nil, errors.New("invalid MosDNS endpoint")
	}
	host := base.Hostname()
	if host == "" || strings.EqualFold(host, "localhost") || !allowedAddress(net.ParseIP(host)) {
		return nil, errors.New("MosDNS endpoint must use a private or loopback IP literal")
	}
	return &Client{base: base, http: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) Healthy(ctx context.Context) error {
	if c == nil || c.base == nil {
		return errors.New("MosDNS client is not configured")
	}
	host := c.base.Hostname()
	port := c.base.Port()
	if port == "" {
		if c.base.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	ip := net.ParseIP(host)
	if !allowedAddress(ip) {
		return errors.New("MosDNS endpoint must use a private or loopback IP literal")
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
	if err != nil {
		return errors.New("MosDNS TCP check failed")
	}
	_ = connection.Close()
	// MosDNS deployments commonly expose DNS only. A successful TCP check is
	// therefore the authoritative read-only signal; HTTP health is optional.
	return nil
}

func (c *Client) Status(ctx context.Context) Status {
	status := Status{Configured: c != nil && c.base != nil}
	if !status.Configured {
		return status
	}
	status.Address = c.base.Host
	status.Transport = "tcp"
	if err := c.Healthy(ctx); err != nil {
		status.Error = err.Error()
		return status
	}
	status.Online = true
	return status
}

func (c *Client) HTTPHealth(ctx context.Context) error {
	if c == nil || c.base == nil {
		return errors.New("MosDNS client is not configured")
	}
	target := *c.base
	target.Path = strings.TrimRight(c.base.Path, "/") + "/health"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return err
	}
	// #nosec G704 -- NewClient accepts only a private or loopback IP literal and
	// this method appends a fixed health path; redirects are disabled.
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("MosDNS health status %d", response.StatusCode)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	return nil
}

func allowedAddress(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	return false
}
