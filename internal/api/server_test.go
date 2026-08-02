package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
)

type memoryNodes struct {
	nodes     map[string]domain.Node
	deleteErr error
}

func (m *memoryNodes) SaveNode(_ context.Context, n domain.Node) error { m.nodes[n.ID] = n; return nil }
func (m *memoryNodes) CreateNode(_ context.Context, n domain.Node) error {
	if _, exists := m.nodes[n.ID]; exists {
		return errors.New("node already exists")
	}
	m.nodes[n.ID] = n
	return nil
}
func (m *memoryNodes) UpdateNode(_ context.Context, n domain.Node) error {
	if _, exists := m.nodes[n.ID]; !exists {
		return storepkg.ErrNotFound
	}
	m.nodes[n.ID] = n
	return nil
}
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
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if _, ok := m.nodes[id]; !ok {
		return storepkg.ErrNotFound
	}
	delete(m.nodes, id)
	return nil
}

func TestDeleteNodeReturnsAtomicReferenceConflict(t *testing.T) {
	t.Parallel()
	const token = "01234567890123456789012345678901"
	store := &memoryNodes{
		nodes: map[string]domain.Node{"node-1": {ID: "node-1", Name: "Node", Type: "http", Server: "example.com", Port: 8080}},
		deleteErr: &storepkg.ReferenceError{
			Resource:   "node",
			References: []string{"proxy-group:group-1"},
		},
	}
	app, err := New(store, token)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.Register(mux)
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/nodes/node-1", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "proxy-group:group-1") || !strings.Contains(response.Body.String(), "node_in_use") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

type securityMemoryNodes struct {
	*memoryNodes
	mu     sync.Mutex
	audits []domain.AuditEvent
}

func (m *securityMemoryNodes) SaveAudit(_ context.Context, event domain.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audits = append(m.audits, event)
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

func TestNodeAPIEnforcesJSONBodyLimit(t *testing.T) {
	t.Parallel()
	const (
		token     = "01234567890123456789012345678901"
		bodyLimit = 1 << 20
	)
	base := `{"name":"HK-01","type":"vless","server":"example.com","port":443}`
	tests := []struct {
		name       string
		size       int
		wantStatus int
		wantSaved  int
	}{
		{name: "body at limit", size: bodyLimit, wantStatus: http.StatusCreated, wantSaved: 1},
		{name: "body over limit", size: bodyLimit + 1, wantStatus: http.StatusBadRequest, wantSaved: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := &memoryNodes{nodes: map[string]domain.Node{}}
			app, err := New(store, token)
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			app.Register(mux)
			body := base + strings.Repeat(" ", test.size-len(base))
			request := httptest.NewRequest(http.MethodPost, "/api/v1/nodes", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if len(store.nodes) != test.wantSaved {
				t.Fatalf("saved nodes=%d, want %d", len(store.nodes), test.wantSaved)
			}
		})
	}
}

func TestNodeUpdatePreservesOmittedFieldsAndCannotCreate(t *testing.T) {
	t.Parallel()
	const token = "01234567890123456789012345678901"
	original := domain.Node{
		ID: "node-1", Name: "Original", Type: "vless", Server: "node.example", Port: 443,
		Username: "operator", Password: "secret-password", UUID: "secret-uuid", Cipher: "auto",
		Network: "ws", SNI: "sni.example", Path: "/ws", Host: "host.example", UDP: true, TLS: true,
		SkipCertVerify: true, Extra: map[string]any{"client-fingerprint": "chrome"},
	}
	store := &memoryNodes{nodes: map[string]domain.Node{original.ID: original}}
	app, err := New(store, token)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.Register(mux)

	missing := httptest.NewRequest(http.MethodPut, "/api/v1/nodes/missing", strings.NewReader(`{"name":"Missing"}`))
	missing.Header.Set("Authorization", "Bearer "+token)
	missingResponse := httptest.NewRecorder()
	mux.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing update status=%d body=%s", missingResponse.Code, missingResponse.Body.String())
	}
	if _, created := store.nodes["missing"]; created {
		t.Fatal("PUT created a missing node")
	}

	update := httptest.NewRequest(http.MethodPut, "/api/v1/nodes/node-1", strings.NewReader(`{"name":"Renamed"}`))
	update.Header.Set("Authorization", "Bearer "+token)
	updateResponse := httptest.NewRecorder()
	mux.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updateResponse.Code, updateResponse.Body.String())
	}
	got := store.nodes[original.ID]
	if got.Name != "Renamed" || got.Password != original.Password || got.UUID != original.UUID || got.SubscriptionID != "" || got.Path != original.Path || got.Host != original.Host || got.SkipCertVerify != original.SkipCertVerify || got.Extra["client-fingerprint"] != "chrome" {
		t.Fatalf("partial update lost fields: %+v", got)
	}
}

func TestNodeUpdateRejectsSubscriptionOwnedNode(t *testing.T) {
	t.Parallel()
	const token = "01234567890123456789012345678901"
	original := domain.Node{ID: "source-node", Name: "Source node", Type: "http", Server: "node.example", Port: 8080, SubscriptionID: "source-1"}
	store := &memoryNodes{nodes: map[string]domain.Node{original.ID: original}}
	app, _ := New(store, token)
	mux := http.NewServeMux()
	app.Register(mux)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/nodes/source-node", strings.NewReader(`{"name":"Taken over"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || store.nodes[original.ID].Name != original.Name {
		t.Fatalf("status=%d node=%+v", response.Code, store.nodes[original.ID])
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/nodes/source-node", nil)
	deleteRequest.Header.Set("Authorization", "Bearer "+token)
	deleteResponse := httptest.NewRecorder()
	mux.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusConflict || store.nodes[original.ID].SubscriptionID != original.SubscriptionID {
		t.Fatalf("delete status=%d node=%+v", deleteResponse.Code, store.nodes[original.ID])
	}
}

func TestBrowserSessionUsesHttpOnlyCookieAndCSRF(t *testing.T) {
	t.Parallel()
	const token = "01234567890123456789012345678901"
	store := &securityMemoryNodes{memoryNodes: &memoryNodes{nodes: map[string]domain.Node{}}}
	app, err := New(store, token)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.Register(mux)

	login := httptest.NewRequest(http.MethodPost, "http://foxos.home.arpa/api/v1/session", strings.NewReader(`{"token":"`+token+`"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "http://foxos.home.arpa")
	login.Header.Set("Sec-Fetch-Site", "same-origin")
	loginResponse := httptest.NewRecorder()
	mux.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusCreated {
		t.Fatalf("login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	if strings.Contains(loginResponse.Body.String(), token) {
		t.Fatal("API token was reflected in the session response")
	}
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &session); err != nil || len(session.CSRFToken) != 64 {
		t.Fatalf("session response=%s err=%v", loginResponse.Body.String(), err)
	}
	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != localCookieName || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected session cookie: %+v", cookies)
	}

	read := httptest.NewRequest(http.MethodGet, "http://foxos.home.arpa/api/v1/nodes", nil)
	read.AddCookie(cookies[0])
	readResponse := httptest.NewRecorder()
	mux.ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("cookie read status=%d body=%s", readResponse.Code, readResponse.Body.String())
	}

	write := func(csrf, origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "http://foxos.home.arpa/api/v1/nodes", strings.NewReader(`{"name":"Node","type":"http","server":"node.example","port":8080}`))
		request.AddCookie(cookies[0])
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		if csrf != "" {
			request.Header.Set(csrfHeaderName, csrf)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}
	if response := write("", "http://foxos.home.arpa"); response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d body=%s", response.Code, response.Body.String())
	}
	if response := write(session.CSRFToken, "http://attacker.invalid"); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d body=%s", response.Code, response.Body.String())
	}
	if response := write(session.CSRFToken, "http://foxos.home.arpa"); response.Code != http.StatusCreated {
		t.Fatalf("valid CSRF status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSecureBrowserSessionCookieAndLoginRateLimit(t *testing.T) {
	t.Parallel()
	const (
		token       = "01234567890123456789012345678901"
		proxyToken  = "abcdefghijklmnopqrstuvwxyz012345"
		proxyHeader = "X-Foxos-Internal-Gateway"
	)
	store := &securityMemoryNodes{memoryNodes: &memoryNodes{nodes: map[string]domain.Node{}}}
	app, _ := New(store, token)
	if err := app.RequireSecureTransport(proxyHeader, proxyToken); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.Register(mux)
	request := func(value string) *http.Request {
		result := httptest.NewRequest(http.MethodPost, "https://foxos.home.arpa/api/v1/session", strings.NewReader(`{"token":"`+value+`"}`))
		result.TLS = &tls.ConnectionState{}
		result.Header.Set("Content-Type", "application/json")
		result.Header.Set("Origin", "https://foxos.home.arpa")
		result.Header.Set("Sec-Fetch-Site", "same-origin")
		return result
	}
	validResponse := httptest.NewRecorder()
	mux.ServeHTTP(validResponse, request(token))
	if validResponse.Code != http.StatusCreated {
		t.Fatalf("secure login status=%d body=%s", validResponse.Code, validResponse.Body.String())
	}
	cookies := validResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != secureCookieName || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatalf("unexpected secure cookie: %+v", cookies)
	}

	for attempt := 1; attempt <= 5; attempt++ {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request(strings.Repeat("x", 32)))
		want := http.StatusUnauthorized
		if attempt == 5 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("attempt=%d status=%d want=%d body=%s", attempt, response.Code, want, response.Body.String())
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.audits) < 2 {
		t.Fatalf("expected authentication and rate-limit audit events, got %+v", store.audits)
	}
}

func TestBearerAuthenticationRequiresTrustedHTTPSWhenEnabled(t *testing.T) {
	const (
		token       = "01234567890123456789012345678901"
		proxyToken  = "abcdefghijklmnopqrstuvwxyz012345"
		proxyHeader = "X-Foxos-Internal-Gateway"
	)
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RequireSecureTransport(proxyHeader, proxyToken); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.Register(mux)
	request := func() *http.Request {
		result := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
		result.Header.Set("Authorization", "Bearer "+token)
		return result
	}
	tests := []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{name: "plain HTTP bearer is rejected", want: http.StatusUpgradeRequired},
		{name: "forged forwarded proto is rejected", mutate: func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") }, want: http.StatusUpgradeRequired},
		{name: "trusted loopback proxy is accepted", mutate: func(r *http.Request) {
			r.RemoteAddr = "127.0.0.1:54321"
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set(proxyHeader, proxyToken)
		}, want: http.StatusOK},
		{name: "direct TLS is accepted", mutate: func(r *http.Request) { r.TLS = &tls.ConnectionState{} }, want: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := request()
			if test.mutate != nil {
				test.mutate(r)
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, r)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
