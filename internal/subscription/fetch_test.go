package subscription

import (
	"context"
	"errors"
	"net"
	"net/http"
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
	check := checkSubscriptionRedirect(resolver)
	if err := check(request, via[:3]); err != nil {
		t.Fatalf("third redirect rejected: %v", err)
	}
	if err := check(request, via); !errors.Is(err, ErrRedirects) {
		t.Fatalf("fourth redirect err=%v", err)
	}
}
