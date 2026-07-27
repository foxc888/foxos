package mihomo

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const exitCheckURL = "https://api.ipify.org?format=json"

type ExitResult struct {
	IPAddress string
	Latency   time.Duration
}

type ExitProbe struct {
	http   *http.Client
	target string
}

func NewExitProbe(proxyEndpoint string) (*ExitProbe, error) {
	proxyURL, err := url.Parse(proxyEndpoint)
	if err != nil || proxyURL.Host == "" || (proxyURL.Scheme != "http" && proxyURL.Scheme != "https") || proxyURL.User != nil || proxyURL.Path != "" || proxyURL.RawQuery != "" || proxyURL.Fragment != "" {
		return nil, errors.New("invalid Mihomo proxy endpoint")
	}
	if !privateControllerHost(proxyURL.Hostname()) {
		return nil, errors.New("Mihomo proxy endpoint must use a private or loopback address")
	}
	transport := &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives:   true,
	}
	return &ExitProbe{http: &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, target: exitCheckURL}, nil
}

func (p *ExitProbe) Probe(ctx context.Context) (ExitResult, error) {
	if p == nil || p.http == nil || p.target == "" {
		return ExitResult{}, errors.New("Mihomo exit probe is not configured")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.target, nil)
	if err != nil {
		return ExitResult{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "FoxOS exit verifier/1")
	started := time.Now()
	response, err := p.http.Do(request)
	if err != nil {
		return ExitResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ExitResult{}, errors.New("exit verification returned a non-success status")
	}
	var value struct {
		IP string `json:"ip"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&value); err != nil {
		return ExitResult{}, errors.New("invalid exit verification response")
	}
	ip := net.ParseIP(strings.TrimSpace(value.IP))
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return ExitResult{}, errors.New("exit verification did not return a public IP address")
	}
	return ExitResult{IPAddress: ip.String(), Latency: time.Since(started)}, nil
}
