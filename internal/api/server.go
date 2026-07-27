package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
)

type NodeStore interface {
	SaveNode(context.Context, domain.Node) error
	Node(context.Context, string) (domain.Node, error)
	Nodes(context.Context) ([]domain.Node, error)
	DeleteNode(context.Context, string) error
}

type Server struct {
	nodes              NodeStore
	token              [sha256.Size]byte
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
	return &Server{nodes: nodes, token: sha256.Sum256([]byte(token))}, nil
}

func (s *Server) Register(mux *http.ServeMux) {
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

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secure := !s.requireSecure || s.secureTransport(r)
		if s.trustedProxyHeader != "" {
			r.Header.Del(s.trustedProxyHeader)
		}
		if !secure {
			problem(w, http.StatusUpgradeRequired, "https_required", errors.New("bearer authentication requires HTTPS"))
			return
		}
		const prefix = "Bearer "
		value := r.Header.Get("Authorization")
		candidate := sha256.Sum256([]byte(strings.TrimPrefix(value, prefix)))
		if !strings.HasPrefix(value, prefix) || subtle.ConstantTimeCompare(candidate[:], s.token[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			problem(w, http.StatusUnauthorized, "unauthorized", errors.New("valid bearer token required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) secureTransport(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	candidate := sha256.Sum256([]byte(r.Header.Get(s.trustedProxyHeader)))
	return subtle.ConstantTimeCompare(candidate[:], s.trustedProxyToken[:]) == 1
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
	if in.ID == "" {
		in.ID = randomID()
	}
	node := in.domain()
	if err := s.nodes.SaveNode(r.Context(), node); err != nil {
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
	var in nodeInput
	if err := decode(r, &in); err != nil {
		problem(w, 400, "invalid_json", err)
		return
	}
	in.ID = r.PathValue("id")
	node := in.domain()
	if err := s.nodes.SaveNode(r.Context(), node); err != nil {
		problem(w, 422, "invalid_node", err)
		return
	}
	writeJSON(w, 200, output(node))
}
func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	if references, ok := s.nodes.(interface {
		NodeReferences(context.Context, string) ([]string, error)
	}); ok {
		items, err := references.NodeReferences(r.Context(), r.PathValue("id"))
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "reference_check_failed")
			return
		}
		if len(items) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "node_in_use", "message": "node is referenced", "references": items})
			return
		}
	}
	err := s.nodes.DeleteNode(r.Context(), r.PathValue("id"))
	if errors.Is(err, storepkg.ErrNotFound) {
		problem(w, 404, "not_found", err)
		return
	}
	if err != nil {
		problem(w, 500, "delete_failed", err)
		return
	}
	w.WriteHeader(204)
}
func decode(r *http.Request, dst any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(body) > 1<<20 {
		return errors.New("request body exceeds 1 MiB")
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
