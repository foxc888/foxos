package subscription

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInvalidURL = errors.New("subscription URL is invalid")
	ErrBlockedURL = errors.New("subscription URL resolves to a private or local address")
	ErrTooLarge   = errors.New("subscription response is too large")
	ErrRedirects  = errors.New("subscription response exceeded 3 redirects")
)

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type netResolver struct{}

func (netResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

type Fetcher struct {
	Resolver       Resolver
	AllowedPrivate []netip.Prefix
	MaxBytes       int64
	Timeout        time.Duration
	UserAgent      string
}

type Result struct {
	URL         string
	Body        []byte
	Digest      string
	ContentType string
}

func ValidateURL(raw string) (*url.URL, error) {
	return validateURL(raw, nil)
}

func (f Fetcher) ValidateURL(raw string) (*url.URL, error) {
	return validateURL(raw, f.AllowedPrivate)
}

func validateURL(raw string, allowedPrivate []netip.Prefix) (*url.URL, error) {
	if strings.TrimSpace(raw) != raw || raw == "" || len(raw) > 4096 {
		return nil, ErrInvalidURL
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
		return nil, ErrInvalidURL
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") {
		return nil, ErrBlockedURL
	}
	if ip := net.ParseIP(host); ip != nil && !isAllowedSubscriptionIP(ip, allowedPrivate) {
		return nil, ErrBlockedURL
	}
	if parsed.Scheme != "https" || (parsed.Port() != "" && parsed.Port() != "443") {
		return nil, ErrInvalidURL
	}
	return parsed, nil
}

func (f Fetcher) Fetch(ctx context.Context, raw string) (Result, error) {
	parsed, err := f.ValidateURL(raw)
	if err != nil {
		return Result{}, err
	}
	resolver := f.Resolver
	if resolver == nil {
		resolver = netResolver{}
	}
	if _, err := resolveAllowedHost(ctx, resolver, parsed.Hostname(), f.AllowedPrivate); err != nil {
		return Result{}, err
	}
	maxBytes := f.MaxBytes
	if maxBytes <= 0 || maxBytes > 8<<20 {
		maxBytes = 2 << 20
	}
	timeout := f.Timeout
	if timeout <= 0 || timeout > 60*time.Second {
		timeout = 15 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	client.CheckRedirect = checkSubscriptionRedirect(resolver, f.AllowedPrivate)
	client.Transport = &http.Transport{Proxy: nil, DialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
		host, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil {
			return nil, splitErr
		}
		ips, lookupErr := resolveAllowedHost(dialCtx, resolver, host, f.AllowedPrivate)
		if lookupErr != nil {
			return nil, lookupErr
		}
		for _, item := range ips {
			connection, dialErr := (&net.Dialer{Timeout: timeout}).DialContext(dialCtx, network, net.JoinHostPort(item.IP.String(), port))
			if dialErr == nil {
				return connection, nil
			}
		}
		return nil, ErrBlockedURL
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Accept", "text/plain, text/yaml, application/json;q=0.8, */*;q=0.1")
	if f.UserAgent != "" {
		request.Header.Set("User-Agent", f.UserAgent)
	}
	// #nosec G704 -- ValidateURL rejects local targets and the custom transport
	// re-resolves every connection, then dials only a validated public IP.
	response, err := client.Do(request)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, fmt.Errorf("subscription HTTP status %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return Result{}, err
	}
	if int64(len(body)) > maxBytes {
		return Result{}, ErrTooLarge
	}
	sum := sha256.Sum256(body)
	return Result{URL: response.Request.URL.String(), Body: body, Digest: fmt.Sprintf("%x", sum[:]), ContentType: response.Header.Get("Content-Type")}, nil
}

func checkSubscriptionRedirect(resolver Resolver, allowedPrivate []netip.Prefix) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return ErrRedirects
		}
		redirect, err := validateURL(request.URL.String(), allowedPrivate)
		if err != nil {
			return err
		}
		if _, err := resolveAllowedHost(request.Context(), resolver, redirect.Hostname(), allowedPrivate); err != nil {
			return err
		}
		return nil
	}
}

func validateResolvedHost(ctx context.Context, resolver Resolver, host string) error {
	_, err := resolveAllowedHost(ctx, resolver, host, nil)
	return err
}

func resolveAllowedHost(ctx context.Context, resolver Resolver, host string, allowedPrivate []netip.Prefix) ([]net.IPAddr, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !isAllowedSubscriptionIP(ip, allowedPrivate) {
			return nil, ErrBlockedURL
		}
		return []net.IPAddr{{IP: ip}}, nil
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, ErrBlockedURL
	}
	for _, address := range addresses {
		if !isAllowedSubscriptionIP(address.IP, allowedPrivate) {
			return nil, ErrBlockedURL
		}
	}
	return addresses, nil
}

func isAllowedSubscriptionIP(ip net.IP, allowedPrivate []netip.Prefix) bool {
	if isPublicIP(ip) {
		return true
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range allowedPrivate {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	for _, prefix := range blockedPublicPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

var blockedPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}
