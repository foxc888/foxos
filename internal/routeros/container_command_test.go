package routeros

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestContainerCommandsRequireExactOwnershipAndUseCommandEndpoint(t *testing.T) {
	t.Parallel()
	status := "stopped"
	commands := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/container":
			_ = json.NewEncoder(w).Encode([]Container{{ID: "*1", Name: "foxos-active", Comment: "foxos:active", Status: status, StartOnBoot: "true"}})
		case r.Method == http.MethodPost && (r.URL.Path == "/rest/container/start" || r.URL.Path == "/rest/container/stop"):
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || body[".id"] != "*1" {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			commands = append(commands, r.URL.Path)
			if r.URL.Path == "/rest/container/start" {
				status = "running"
			} else {
				status = "stopped"
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "foxos", "password")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.StartContainer(t.Context(), "*1", "foxos:active"); err != nil {
		t.Fatal(err)
	}
	if err := client.StartContainer(t.Context(), "*1", "foxos:active"); err != nil {
		t.Fatal(err)
	}
	if err := client.StopContainer(t.Context(), "*1", "foxos:active"); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 || commands[0] != "/rest/container/start" || commands[1] != "/rest/container/stop" {
		t.Fatalf("commands=%v", commands)
	}
	if _, err := client.ManagedContainer(t.Context(), "*1", "foxos:user"); !errors.Is(err, ErrContainerNotManaged) {
		t.Fatalf("ownership err=%v", err)
	}
}
