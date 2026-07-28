package subscription

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
)

type staticResolver []net.IPAddr

func (s staticResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return s, nil
}

func TestValidateURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		url     string
		wantErr error
	}{
		{name: "public HTTPS", url: "https://example.com/nodes.txt"},
		{name: "reject plain HTTP", url: "http://example.com/nodes", wantErr: ErrInvalidURL},
		{name: "reject nonstandard port", url: "https://example.com:8443/nodes", wantErr: ErrInvalidURL},
		{name: "reject credentials", url: "https://token@example.com/nodes", wantErr: ErrInvalidURL},
		{name: "reject file scheme", url: "file:///etc/passwd", wantErr: ErrInvalidURL},
		{name: "reject loopback", url: "http://127.0.0.1/nodes", wantErr: ErrBlockedURL},
		{name: "reject private", url: "http://10.0.0.1/nodes", wantErr: ErrBlockedURL},
		{name: "reject local hostname", url: "http://router.local/nodes", wantErr: ErrBlockedURL},
		{name: "reject surrounding whitespace", url: " https://example.com", wantErr: ErrInvalidURL},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ValidateURL(test.url)
			if test.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("err=%v want=%v", err, test.wantErr)
			}
		})
	}
}

func TestIsPublicIPRejectsSpecialUseRanges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		value  string
		public bool
	}{
		{name: "public", value: "1.1.1.1", public: true},
		{name: "carrier NAT", value: "100.64.0.1"},
		{name: "benchmark", value: "198.18.0.1"},
		{name: "documentation IPv4", value: "203.0.113.10"},
		{name: "documentation IPv6", value: "2001:db8::1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := isPublicIP(net.ParseIP(test.value)); got != test.public {
				t.Fatalf("isPublicIP(%s)=%t want=%t", test.value, got, test.public)
			}
		})
	}
}

func TestValidateResolvedHostRejectsMixedPublicAndPrivateAnswers(t *testing.T) {
	t.Parallel()
	resolver := staticResolver{{IP: net.ParseIP("203.0.113.10")}, {IP: net.ParseIP("10.0.0.2")}}
	if err := validateResolvedHost(context.Background(), resolver, "example.com"); !errors.Is(err, ErrBlockedURL) {
		t.Fatalf("err=%v", err)
	}
}

func TestFetcherRedirectPolicyLimitsHopsBeforeFollowing(t *testing.T) {
	t.Parallel()
	resolver := staticResolver{{IP: net.ParseIP("1.1.1.1")}}
	request := &http.Request{URL: &url.URL{Scheme: "https", Host: "example.com", Path: "/nodes"}}
	via := make([]*http.Request, 4)
	for index := range via {
		via[index] = request.Clone(context.Background())
	}
	check := checkSubscriptionRedirect(resolver, nil)
	if err := check(request, via[:3]); err != nil {
		t.Fatalf("third redirect rejected: %v", err)
	}
	if err := check(request, via); !errors.Is(err, ErrRedirects) {
		t.Fatalf("fourth redirect err=%v", err)
	}
}

func TestFetcherPrivateAllowlistAppliesToLiteralDNSRedirectAndRebinding(t *testing.T) {
	t.Parallel()
	allowed := []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")}
	fetcher := Fetcher{AllowedPrivate: allowed}
	if _, err := fetcher.ValidateURL("https://10.20.1.2/nodes"); err != nil {
		t.Fatalf("allowlisted literal rejected: %v", err)
	}
	for _, raw := range []string{"https://10.21.1.2/nodes", "https://127.0.0.1/nodes", "https://169.254.169.254/latest/meta-data"} {
		if _, err := fetcher.ValidateURL(raw); !errors.Is(err, ErrBlockedURL) {
			t.Fatalf("blocked literal %q err=%v", raw, err)
		}
	}
	if _, err := resolveAllowedHost(context.Background(), staticResolver{{IP: net.ParseIP("10.20.1.2")}}, "feed.example", allowed); err != nil {
		t.Fatalf("allowlisted DNS result rejected: %v", err)
	}
	mixed := staticResolver{{IP: net.ParseIP("10.20.1.2")}, {IP: net.ParseIP("10.21.1.2")}}
	if _, err := resolveAllowedHost(context.Background(), mixed, "feed.example", allowed); !errors.Is(err, ErrBlockedURL) {
		t.Fatalf("mixed allowed and blocked DNS answers err=%v", err)
	}
	redirect := &http.Request{URL: &url.URL{Scheme: "https", Host: "10.21.1.2", Path: "/nodes"}}
	if err := checkSubscriptionRedirect(staticResolver(nil), allowed)(redirect, nil); !errors.Is(err, ErrBlockedURL) {
		t.Fatalf("redirect outside allowlist err=%v", err)
	}
	resolver := &changingResolver{answers: [][]net.IPAddr{
		{{IP: net.ParseIP("10.20.1.2")}},
		{{IP: net.ParseIP("169.254.169.254")}},
	}}
	if _, err := resolveAllowedHost(context.Background(), resolver, "feed.example", allowed); err != nil {
		t.Fatalf("first resolution rejected: %v", err)
	}
	if _, err := resolveAllowedHost(context.Background(), resolver, "feed.example", allowed); !errors.Is(err, ErrBlockedURL) {
		t.Fatalf("rebinding resolution err=%v", err)
	}
}

type changingResolver struct {
	answers [][]net.IPAddr
	calls   int
}

func (r *changingResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	index := r.calls
	if index >= len(r.answers) {
		index = len(r.answers) - 1
	}
	r.calls++
	return r.answers[index], nil
}
