package mihomo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExitProbeValidatesPublicAddressResponse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		body    string
		wantIP  string
		wantErr bool
	}{
		{name: "public IPv4", body: `{"ip":"1.1.1.1"}`, wantIP: "1.1.1.1"},
		{name: "reject private", body: `{"ip":"10.0.0.2"}`, wantErr: true},
		{name: "reject malformed", body: `{"ip":"not-an-ip"}`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			probe := &ExitProbe{http: server.Client(), target: server.URL}
			result, err := probe.Probe(context.Background())
			if (err != nil) != test.wantErr || result.IPAddress != test.wantIP {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestNewExitProbeRejectsPublicOrCredentialedProxyEndpoints(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{"https://example.com:7890", "http://user:password@10.0.0.2:7890", "socks5://10.0.0.2:7890"} {
		if _, err := NewExitProbe(endpoint); err == nil {
			t.Fatalf("endpoint %q accepted", endpoint)
		}
	}
	if _, err := NewExitProbe("http://10.0.0.2:7890"); err != nil {
		t.Fatal(err)
	}
}
