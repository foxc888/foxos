package routeros

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadOverviewAndDevices(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "foxos" || password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/system/resource":
			_ = json.NewEncoder(w).Encode(Resource{Version: "7.15.2", Architecture: "x86_64", CPULoad: "12"})
		case "/rest/interface":
			_ = json.NewEncoder(w).Encode([]Interface{{ID: "*1", Name: "ether1", Running: "true"}})
		case "/rest/ip/dhcp-server/lease":
			_ = json.NewEncoder(w).Encode([]Lease{{ID: "*2", Address: "10.0.0.20", MACAddress: "aa:bb:cc:dd:ee:ff", HostName: "phone", Status: "bound", Dynamic: "true"}})
		case "/rest/ip/arp":
			_ = json.NewEncoder(w).Encode([]ARP{{ID: "*3", Address: "10.0.0.20", MACAddress: "AA:BB:CC:DD:EE:FF", Interface: "bridge", Complete: "true"}})
		case "/rest/interface/l2tp-client":
			_, _ = w.Write([]byte(`[{"name":"JP","connect-to":"vpn.example.com","user":"fox","password":"must-not-leak","running":"true"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "foxos", "secret")
	if err != nil {
		t.Fatal(err)
	}
	client.http = server.Client()
	resource, err := client.Resource(context.Background())
	if err != nil || resource.Version != "7.15.2" {
		t.Fatalf("resource=%+v err=%v", resource, err)
	}
	devices, err := client.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].HostName != "phone" || devices[0].Interface != "bridge" || devices[0].MACAddress != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("devices=%+v", devices)
	}
	l2tp, err := client.L2TPClients(context.Background())
	if err != nil || len(l2tp) != 1 || l2tp[0].Name != "JP" {
		t.Fatalf("l2tp=%+v err=%v", l2tp, err)
	}
}

func TestResourceRejectsInvalidObject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		body      string
		wantError string
	}{
		{name: "missing version", body: `{}`, wantError: "missing version"},
		{name: "array response", body: `[{"version":"7.23.2"}]`, wantError: "decode RouterOS response"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "foxos", "secret")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Resource(t.Context()); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Resource() error = %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestRejectsRedirect(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com", http.StatusFound)
	}))
	defer redirect.Close()
	client, err := NewClient(redirect.URL, "foxos", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Resource(context.Background()); err == nil {
		t.Fatal("expected redirect rejection")
	}
}

func TestReadErrorDoesNotExposeRouterOSBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("device-secret-must-not-leak"))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "foxos", "secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Resource(context.Background())
	if err == nil || !strings.Contains(err.Error(), "status 500") || strings.Contains(err.Error(), "device-secret") {
		t.Fatalf("Resource() error = %v", err)
	}
}
