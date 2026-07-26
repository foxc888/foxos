package routeros

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
