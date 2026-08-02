package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
)

type NodeStore interface {
	SaveNode(context.Context, domain.Node) error
	CreateNode(context.Context, domain.Node) error
	UpdateNode(context.Context, domain.Node) error
	Node(context.Context, string) (domain.Node, error)
	Nodes(context.Context) ([]domain.Node, error)
	DeleteNode(context.Context, string) error
}

type Server struct {
	nodes              NodeStore
	token              [sha256.Size]byte
	sessions           *sessionStore
	limiter            *fixedWindowLimiter
	audit              securityAuditStore
	requireSecure      bool
	trustedProxyHeader string
	trustedProxyToken  [sha256.Size]byte
}

func (s *Server) RequireSecureTransport(proxyHeader, proxyToken string) error {
	if strings.TrimSpace(proxyHeader) == "" || len(proxyToken) < 32 {
		return errors.New("trusted proxy header and token of at least 32 characters are required")
	}
	s.requireSecure = true
	s.trustedProxyHeader = proxyHeader
	s.trustedProxyToken = sha256.Sum256([]byte(proxyToken))
	return nil
}

func New(nodes NodeStore, token string) (*Server, error) {
	if nodes == nil || len(token) < 32 {
		return nil, errors.New("node store and API token of at least 32 characters are required")
	}
	server := &Server{nodes: nodes, token: sha256.Sum256([]byte(token)), sessions: newSessionStore(), limiter: newFixedWindowLimiter()}
	server.audit, _ = nodes.(securityAuditStore)
	return server, nil
}

func (s *Server) Register(mux *http.ServeMux) {
	s.registerSession(mux)
	mux.Handle("GET /api/v1/nodes", s.auth(http.HandlerFunc(s.listNodes)))
	mux.Handle("POST /api/v1/nodes", s.auth(http.HandlerFunc(s.createNode)))
	mux.Handle("GET /api/v1/nodes/{id}", s.auth(http.HandlerFunc(s.getNode)))
	mux.Handle("PUT /api/v1/nodes/{id}", s.auth(http.HandlerFunc(s.updateNode)))
	mux.Handle("DELETE /api/v1/nodes/{id}", s.auth(http.HandlerFunc(s.deleteNode)))
	s.registerNodeExtras(mux)
}

type nodeInput struct {
	ID             string         `json:"id,omitempty"`
	Name           string         `json:"name"`
	Type           string         `json:"type"`
	Server         string         `json:"server"`
	Port           int            `json:"port"`
	Username       string         `json:"username,omitempty"`
	Password       string         `json:"password,omitempty"` // #nosec G117 -- write-only API input; nodeOutput never includes the value.
	UUID           string         `json:"uuid,omitempty"`
	Cipher         string         `json:"cipher,omitempty"`
	Network        string         `json:"network,omitempty"`
	SNI            string         `json:"sni,omitempty"`
	Path           string         `json:"path,omitempty"`
	Host           string         `json:"host,omitempty"`
	UDP            bool           `json:"udp,omitempty"`
	TLS            bool           `json:"tls,omitempty"`
	SkipCertVerify bool           `json:"skipCertVerify,omitempty"`
	Extra          map[string]any `json:"extra,omitempty"`
}

type nodeUpdateInput struct {
	Name           *string         `json:"name,omitempty"`
	Type           *string         `json:"type,omitempty"`
	Server         *string         `json:"server,omitempty"`
	Port           *int            `json:"port,omitempty"`
	Username       *string         `json:"username,omitempty"`
	Password       *string         `json:"password,omitempty"` // #nosec G117 -- write-only API input; nodeOutput never includes the value.
	UUID           *string         `json:"uuid,omitempty"`
	Cipher         *string         `json:"cipher,omitempty"`
	Network        *string         `json:"network,omitempty"`
	SNI            *string         `json:"sni,omitempty"`
	Path           *string         `json:"path,omitempty"`
	Host           *string         `json:"host,omitempty"`
	UDP            *bool           `json:"udp,omitempty"`
	TLS            *bool           `json:"tls,omitempty"`
	SkipCertVerify *bool           `json:"skipCertVerify,omitempty"`
	Extra          *map[string]any `json:"extra,omitempty"`
}
type nodeOutput struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Server        string `json:"server"`
	Port          int    `json:"port"`
	Network       string `json:"network,omitempty"`
	SNI           string `json:"sni,omitempty"`
	UDP           bool   `json:"udp,omitempty"`
	TLS           bool   `json:"tls,omitempty"`
	HasCredential bool   `json:"hasCredential"`
}

func (p nodeInput) domain() domain.Node {
	return domain.Node{ID: p.ID, Name: p.Name, Type: strings.ToLower(p.Type), Server: p.Server, Port: p.Port, Username: p.Username, Password: p.Password, UUID: p.UUID, Cipher: p.Cipher, Network: p.Network, SNI: p.SNI, Path: p.Path, Host: p.Host, UDP: p.UDP, TLS: p.TLS, SkipCertVerify: p.SkipCertVerify, Extra: p.Extra}
}
func output(n domain.Node) nodeOutput {
	return nodeOutput{ID: n.ID, Name: n.Name, Type: n.Type, Server: n.Server, Port: n.Port, Network: n.Network, SNI: n.SNI, UDP: n.UDP, TLS: n.TLS, HasCredential: n.Password != "" || n.UUID != ""}
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.nodes.Nodes(r.Context())
	if err != nil {
		problem(w, 500, "list_failed", err)
		return
	}
	out := make([]nodeOutput, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, output(n))
	}
	writeJSON(w, 200, out)
}
func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	var in nodeInput
	if err := decode(r, &in); err != nil {
		problem(w, 400, "invalid_json", err)
		return
	}
	if in.ID != "" {
		problemCode(w, http.StatusUnprocessableEntity, "node_id_server_generated")
		return
	}
	in.ID = randomID()
	node := in.domain()
	if err := s.nodes.CreateNode(r.Context(), node); err != nil {
		problem(w, 422, "invalid_node", err)
		return
	}
	writeJSON(w, 201, output(node))
}
func (s *Server) getNode(w http.ResponseWriter, r *http.Request) {
	node, err := s.nodes.Node(r.Context(), r.PathValue("id"))
	if errors.Is(err, storepkg.ErrNotFound) {
		problem(w, 404, "not_found", err)
		return
	}
	if err != nil {
		problem(w, 500, "read_failed", err)
		return
	}
	writeJSON(w, 200, output(node))
}
func (s *Server) updateNode(w http.ResponseWriter, r *http.Request) {
	current, err := s.nodes.Node(r.Context(), r.PathValue("id"))
	if errors.Is(err, storepkg.ErrNotFound) {
		problemCode(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		problemCode(w, http.StatusInternalServerError, "read_failed")
		return
	}
	if current.SubscriptionID != "" {
		problemCode(w, http.StatusConflict, "subscription_node_read_only")
		return
	}
	var in nodeUpdateInput
	if err := decode(r, &in); err != nil {
		problem(w, 400, "invalid_json", err)
		return
	}
	node := mergeNodeUpdate(current, in)
	if err := s.nodes.UpdateNode(r.Context(), node); err != nil {
		if errors.Is(err, storepkg.ErrNotFound) {
			problemCode(w, http.StatusNotFound, "not_found")
			return
		}
		problem(w, 422, "invalid_node", err)
		return
	}
	writeJSON(w, 200, output(node))
}

func mergeNodeUpdate(current domain.Node, update nodeUpdateInput) domain.Node {
	if update.Name != nil {
		current.Name = strings.TrimSpace(*update.Name)
	}
	if update.Type != nil {
		current.Type = strings.ToLower(strings.TrimSpace(*update.Type))
	}
	if update.Server != nil {
		current.Server = strings.TrimSpace(*update.Server)
	}
	if update.Port != nil {
		current.Port = *update.Port
	}
	if update.Username != nil {
		current.Username = *update.Username
	}
	if update.Password != nil {
		current.Password = *update.Password
	}
	if update.UUID != nil {
		current.UUID = *update.UUID
	}
	if update.Cipher != nil {
		current.Cipher = *update.Cipher
	}
	if update.Network != nil {
		current.Network = *update.Network
	}
	if update.SNI != nil {
		current.SNI = *update.SNI
	}
	if update.Path != nil {
		current.Path = *update.Path
	}
	if update.Host != nil {
		current.Host = *update.Host
	}
	if update.UDP != nil {
		current.UDP = *update.UDP
	}
	if update.TLS != nil {
		current.TLS = *update.TLS
	}
	if update.SkipCertVerify != nil {
		current.SkipCertVerify = *update.SkipCertVerify
	}
	if update.Extra != nil {
		current.Extra = make(map[string]any, len(*update.Extra))
		for key, value := range *update.Extra {
			current.Extra[key] = value
		}
	}
	return current
}
func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	current, err := s.nodes.Node(r.Context(), r.PathValue("id"))
	if errors.Is(err, storepkg.ErrNotFound) {
		problem(w, http.StatusNotFound, "not_found", err)
		return
	}
	if err != nil {
		problemCode(w, http.StatusInternalServerError, "read_failed")
		return
	}
	if current.SubscriptionID != "" {
		problemCode(w, http.StatusConflict, "subscription_node_read_only")
		return
	}
	err = s.nodes.DeleteNode(r.Context(), r.PathValue("id"))
	if errors.Is(err, storepkg.ErrNotFound) {
		problem(w, 404, "not_found", err)
		return
	}
	var referenceErr *storepkg.ReferenceError
	if errors.As(err, &referenceErr) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "node_in_use", "message": referenceErr.Error(), "references": referenceErr.References})
		return
	}
	if err != nil {
		problem(w, 500, "delete_failed", err)
		return
	}
	w.WriteHeader(204)
}
func decode(r *http.Request, dst any) error {
	return decodeLimit(r, dst, 1<<20)
}

func decodeLimit(r *http.Request, dst any, limit int64) error {
	if limit < 1 {
		return errors.New("request body limit is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return errors.New("request body exceeds the allowed size")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}
func randomID() string {
	var body [16]byte
	if _, err := rand.Read(body[:]); err != nil {
		panic("secure random unavailable")
	}
	return hex.EncodeToString(body[:])
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, status int, code string, err error) {
	message := code
	if status < 500 && err != nil {
		message = err.Error()
	}
	writeJSON(w, status, map[string]any{"error": code, "message": message})
}

func problemCode(w http.ResponseWriter, status int, code string) {
	problem(w, status, code, nil)
}
