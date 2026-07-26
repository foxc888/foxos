package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
)

type memoryNodes struct{ nodes map[string]domain.Node }

func (m *memoryNodes) SaveNode(_ context.Context, n domain.Node) error { m.nodes[n.ID] = n; return nil }
func (m *memoryNodes) Node(_ context.Context, id string) (domain.Node, error) {
	n, ok := m.nodes[id]
	if !ok {
		return domain.Node{}, storepkg.ErrNotFound
	}
	return n, nil
}
func (m *memoryNodes) Nodes(context.Context) ([]domain.Node, error) {
	out := make([]domain.Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, n)
	}
	return out, nil
}
func (m *memoryNodes) DeleteNode(_ context.Context, id string) error {
	if _, ok := m.nodes[id]; !ok {
		return storepkg.ErrNotFound
	}
	delete(m.nodes, id)
	return nil
}

func TestNodeAPIRequiresAuthAndRedactsSecrets(t *testing.T) {
	const token = "01234567890123456789012345678901"
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.Register(mux)
	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest("GET", "/api/v1/nodes", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", unauthorized.Code)
	}
	body := `{"name":"HK-01","type":"vless","server":"example.com","port":443,"uuid":"secret-uuid","password":"secret-password"}`
	request := httptest.NewRequest("POST", "/api/v1/nodes", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "secret-uuid") || strings.Contains(response.Body.String(), "secret-password") {
		t.Fatalf("secret leaked: %s", response.Body.String())
	}
	var out nodeOutput
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.HasCredential {
		t.Fatal("expected redacted credential marker")
	}
}
