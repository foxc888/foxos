package mihomo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testControllerSecret = "0123456789abcdef0123456789abcdef"

func TestControllerHealthAndReload(t *testing.T) {
	var reloadPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testControllerSecret {
			t.Errorf("missing auth")
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"1.19.0"}`))
		case r.Method == "PUT" && r.URL.Path == "/configs":
			if r.URL.Query().Get("force") != "true" {
				t.Errorf("force query missing")
			}
			var body struct {
				Path string `json:"path"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			reloadPath = body.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	controller, err := NewController(server.URL, testControllerSecret, "/root/.config/mihomo/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Healthy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reloadPath != "/root/.config/mihomo/config.yaml" {
		t.Fatalf("path=%q", reloadPath)
	}
}

func TestControllerValidatesYAMLBeforeReload(t *testing.T) {
	controller, err := NewController("http://127.0.0.1:9090", testControllerSecret, "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Validate(context.Background(), []byte("proxies: [")); err == nil {
		t.Fatal("expected YAML error")
	}
}

func TestControllerStatusCollectsRuntimeProxiesConnectionsAndTraffic(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/version":
			_, _ = w.Write([]byte(`{"version":"1.19.0"}`))
		case "/proxies":
			_, _ = w.Write([]byte(`{"proxies":{"Node A":{"name":"Node A","type":"VLESS","history":[{"time":"2026-07-27T00:00:00Z","delay":42}]},"GLOBAL":{"name":"GLOBAL","type":"Selector","now":"Node A","all":["Node A"]}}}`))
		case "/connections":
			_, _ = w.Write([]byte(`{"uploadTotal":128,"downloadTotal":256,"connections":[{"id":"connection-a","upload":12,"download":34}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	controller, err := NewController(server.URL, testControllerSecret, "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	status, err := controller.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Version != "1.19.0" || len(status.Proxies) != 2 || len(status.Connections) != 1 || len(status.Partial) != 0 {
		t.Fatalf("status=%+v", status)
	}
	if status.Traffic["uploadTotal"] != int64(128) || status.Traffic["downloadTotal"] != int64(256) || status.Traffic["connectionCount"] != 1 {
		t.Fatalf("traffic=%+v", status.Traffic)
	}
}

func TestControllerProbeNodeHTTPUsesEscapedNameAndFixedTarget(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxies/Node / A/delay" || r.URL.Query().Get("url") != nodeHTTPCheckURL || r.URL.Query().Get("timeout") != "5000" {
			t.Errorf("path=%q raw=%q query=%v", r.URL.Path, r.URL.RawPath, r.URL.Query())
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+testControllerSecret {
			t.Error("missing controller authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"delay":73}`))
	}))
	defer server.Close()
	controller, err := NewController(server.URL, testControllerSecret, "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	delay, err := controller.ProbeNodeHTTP(context.Background(), "Node / A")
	if err != nil || delay != 73*time.Millisecond {
		t.Fatalf("delay=%s err=%v", delay, err)
	}
}

func TestNewControllerRejectsWeakSecrets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		secret string
	}{
		{name: "missing"},
		{name: "too short", secret: strings.Repeat("x", 31)},
		{name: "surrounding whitespace", secret: " " + strings.Repeat("x", 32)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewController("http://127.0.0.1:9090", test.secret, "/config.yaml"); err == nil {
				t.Fatal("expected Mihomo secret validation error")
			}
		})
	}
}
