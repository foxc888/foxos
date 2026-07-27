package routeros

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientAppliesOnlyOwnedBindingAndVerifies(t *testing.T) {
	var applied bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, _ := r.BasicAuth()
		if user != "foxos" || password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/rest/ip/dhcp-server/lease/*1":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["comment"] != "foxos:device:phone" {
				t.Errorf("body=%+v", body)
			}
			applied = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/ip/dhcp-server/lease":
			_ = json.NewEncoder(w).Encode([]Lease{{
				ID:         "*1",
				Address:    "10.0.0.20",
				MACAddress: "AA:BB:CC:DD:EE:FF",
				Dynamic:    "false",
				Comment:    "foxos:device:phone",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "foxos", "secret")
	if err != nil {
		t.Fatal(err)
	}
	operation := Operation{
		Method: http.MethodPatch,
		Path:   "/rest/ip/dhcp-server/lease/*1",
		Body: map[string]string{
			"address":     "10.0.0.20",
			"mac-address": "AA:BB:CC:DD:EE:FF",
			"server":      "dhcp-lan",
			"comment":     "foxos:device:phone",
		},
		OwnedComment: "foxos:device:phone",
	}
	if err := client.Apply(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("operation not applied")
	}
	if err := client.Verify(context.Background(), Plan{Operations: []Operation{operation}}); err != nil {
		t.Fatal(err)
	}
}

func TestClientRechecksEgressOwnershipBeforeMutation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		comment   string
		wantError bool
	}{
		{name: "owned resource", comment: "foxos:egress:phone"},
		{name: "ownership changed", comment: "user-owned", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			patched := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/rest/ip/firewall/filter":
					_ = json.NewEncoder(w).Encode([]map[string]string{{".id": "*A", "comment": test.comment}})
				case r.Method == http.MethodPatch && r.URL.Path == "/rest/ip/firewall/filter/*A":
					patched = true
					w.WriteHeader(http.StatusOK)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "foxos", "secret")
			if err != nil {
				t.Fatal(err)
			}
			operation := EgressOperation{Method: http.MethodPatch, Path: "/rest/ip/firewall/filter/*A", OwnedComment: "foxos:egress:phone", Body: map[string]string{"chain": egressFilterChain, "src-address": "192.168.1.20", "action": "reject", "comment": "foxos:egress:phone"}}
			err = client.ApplyEgress(context.Background(), operation)
			if test.wantError {
				if !errors.Is(err, ErrUnsafeOperation) || patched {
					t.Fatalf("err=%v patched=%t", err, patched)
				}
				return
			}
			if err != nil || !patched {
				t.Fatalf("err=%v patched=%t", err, patched)
			}
		})
	}
}

func TestClientBlocksArbitraryWritePath(t *testing.T) {
	client, err := NewClient("http://127.0.0.1", "foxos", "secret")
	if err != nil {
		t.Fatal(err)
	}
	err = client.Apply(context.Background(), Operation{
		Method:       http.MethodPost,
		Path:         "/rest/system/reboot",
		OwnedComment: "foxos:device:phone",
		Body:         map[string]string{"comment": "foxos:device:phone"},
	})
	if err == nil {
		t.Fatal("expected unsafe operation error")
	}
}

func TestWriteErrorDoesNotExposeRouterOSBody(t *testing.T) {
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
	err = client.Apply(context.Background(), Operation{
		Method:       http.MethodPut,
		Path:         "/rest/ip/dhcp-server/lease",
		OwnedComment: "foxos:device:phone",
		Body: map[string]string{
			"address":     "10.0.0.20",
			"mac-address": "AA:BB:CC:DD:EE:FF",
			"server":      "dhcp-lan",
			"comment":     "foxos:device:phone",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "status 500") || strings.Contains(err.Error(), "device-secret") {
		t.Fatalf("Apply() error = %v", err)
	}
}
