package mihomo

import (
	"bytes"
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
	"unicode"

	"gopkg.in/yaml.v3"
)

type Controller struct {
	base       *url.URL
	secret     string
	reloadPath string
	http       *http.Client
}

type RuntimeStatus struct {
	Version     string         `json:"version"`
	Proxies     map[string]any `json:"proxies,omitempty"`
	Connections []any          `json:"connections,omitempty"`
	Traffic     map[string]any `json:"traffic,omitempty"`
	Partial     []string       `json:"partial,omitempty"`
}

const nodeHTTPCheckURL = "https://www.gstatic.com/generate_204"

func NewController(endpoint, secret, reloadPath string) (*Controller, error) {
	base, err := url.Parse(endpoint)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil {
		return nil, errors.New("invalid Mihomo controller endpoint")
	}
	if !privateControllerHost(base.Hostname()) {
		return nil, errors.New("Mihomo endpoint must use a private or loopback address")
	}
	if len(secret) < 32 || len(secret) > 4096 || strings.TrimSpace(secret) != secret {
		return nil, errors.New("invalid Mihomo secret")
	}
	if reloadPath == "" {
		return nil, errors.New("Mihomo config path is required")
	}
	return &Controller{base: base, secret: secret, reloadPath: reloadPath, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func privateControllerHost(host string) bool {
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".local") {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func (c *Controller) Validate(_ context.Context, body []byte) error {
	if len(body) == 0 || len(body) > 16<<20 {
		return errors.New("invalid config size")
	}
	var document yaml.Node
	if err := yaml.Unmarshal(body, &document); err != nil {
		return fmt.Errorf("yaml: %w", err)
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("config root must be a mapping")
	}
	return nil
}

func (c *Controller) Reload(ctx context.Context) error {
	body, err := json.Marshal(map[string]string{"path": c.reloadPath})
	if err != nil {
		return err
	}
	response, err := c.do(ctx, http.MethodPut, "/configs?force=true", bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		return statusError(response)
	}
	return nil
}

func (c *Controller) Healthy(ctx context.Context) error {
	response, err := c.do(ctx, http.MethodGet, "/version", nil, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return statusError(response)
	}
	var value struct {
		Version string `json:"version"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&value); err != nil || value.Version == "" {
		return errors.New("invalid Mihomo version response")
	}
	return nil
}

func (c *Controller) Status(ctx context.Context) (RuntimeStatus, error) {
	response, err := c.do(ctx, http.MethodGet, "/version", nil, "")
	if err != nil {
		return RuntimeStatus{}, err
	}
	var status RuntimeStatus
	if err := decodeControllerJSON(response, &status); err != nil {
		return RuntimeStatus{}, err
	}
	partial := make([]string, 0)
	var proxies struct {
		Proxies map[string]any `json:"proxies"`
	}
	if err := c.getJSON(ctx, "/proxies", &proxies); err != nil {
		partial = append(partial, "proxies")
	} else {
		status.Proxies = proxies.Proxies
	}
	var connections struct {
		UploadTotal   int64 `json:"uploadTotal"`
		DownloadTotal int64 `json:"downloadTotal"`
		Connections   []any `json:"connections"`
	}
	if err := c.getJSON(ctx, "/connections", &connections); err != nil {
		partial = append(partial, "connections")
	} else {
		status.Connections = connections.Connections
		status.Traffic = map[string]any{
			"uploadTotal":     connections.UploadTotal,
			"downloadTotal":   connections.DownloadTotal,
			"connectionCount": len(connections.Connections),
		}
	}
	status.Partial = partial
	return status, nil
}

func (c *Controller) ProbeNodeHTTP(ctx context.Context, name string) (time.Duration, error) {
	if strings.TrimSpace(name) != name || name == "" || len(name) > 256 || strings.ContainsFunc(name, unicode.IsControl) {
		return 0, errors.New("invalid Mihomo proxy name")
	}
	target := *c.base
	basePath := strings.TrimRight(c.base.Path, "/")
	escapedBase := strings.TrimRight(c.base.EscapedPath(), "/")
	target.Path = basePath + "/proxies/" + name + "/delay"
	target.RawPath = escapedBase + "/proxies/" + url.PathEscape(name) + "/delay"
	query := url.Values{}
	query.Set("timeout", "5000")
	query.Set("url", nodeHTTPCheckURL)
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.secret)
	// #nosec G704 -- the controller base is a validated private IP literal;
	// the escaped proxy name is one path segment and the check URL is fixed.
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	var value struct {
		Delay int64 `json:"delay"`
	}
	if err := decodeControllerJSON(response, &value); err != nil {
		return 0, err
	}
	if value.Delay <= 0 || value.Delay > 120_000 {
		return 0, errors.New("invalid Mihomo delay response")
	}
	return time.Duration(value.Delay) * time.Millisecond, nil
}

func (c *Controller) getJSON(ctx context.Context, path string, destination any) error {
	response, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	return decodeControllerJSON(response, destination)
}

func decodeControllerJSON(response *http.Response, destination any) error {
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return statusError(response)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return nil
}

func (c *Controller) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	target := *c.base
	target.Path = strings.TrimRight(c.base.Path, "/") + strings.Split(path, "?")[0]
	if index := strings.Index(path, "?"); index >= 0 {
		target.RawQuery = path[index+1:]
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Authorization", "Bearer "+c.secret)
	// #nosec G704 -- the base URL is a validated private IP literal and path is
	// selected only by internal controller methods; redirects are disabled.
	return c.http.Do(request)
}
func statusError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	return fmt.Errorf("Mihomo controller status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
}
