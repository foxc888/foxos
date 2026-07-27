package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/mihomo"
)

type fakeNodeHTTPProbe struct {
	name string
}

func (f *fakeNodeHTTPProbe) ProbeNodeHTTP(_ context.Context, name string) (time.Duration, error) {
	f.name = name
	return 42 * time.Millisecond, nil
}

type fakeExitProbe struct{}

func (fakeExitProbe) Probe(context.Context) (mihomo.ExitResult, error) {
	return mihomo.ExitResult{IPAddress: "1.1.1.1", Latency: 55 * time.Millisecond}, nil
}

func TestMihomoProbeKeepsNodeHTTPAndCurrentPolicyExitScopesSeparate(t *testing.T) {
	const token = "01234567890123456789012345678901"
	store := &memoryNodes{nodes: map[string]domain.Node{"node-a": {ID: "node-a", Name: "Node A", Type: "vless", Server: "example.com", Port: 443}}}
	app, err := New(store, token)
	if err != nil {
		t.Fatal(err)
	}
	nodeProbe := &fakeNodeHTTPProbe{}
	mux := http.NewServeMux()
	app.RegisterMihomoProbes(mux, nodeProbe, fakeExitProbe{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/mihomo/probes/node-a", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var output struct {
		NodeHTTP mihomoProbeCheck `json:"nodeHttp"`
		Exit     mihomoExitCheck  `json:"exit"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if nodeProbe.name != "Node A" || !output.NodeHTTP.Success || output.NodeHTTP.LatencyMS != 42 {
		t.Fatalf("nodeHTTP=%+v name=%q", output.NodeHTTP, nodeProbe.name)
	}
	if !output.Exit.Success || output.Exit.Scope != "current-policy" || output.Exit.IPAddress != "1.1.1.1" || output.Exit.LatencyMS != 55 {
		t.Fatalf("exit=%+v", output.Exit)
	}
}
