package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/mihomo"
	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
)

type AtomicNodeStore interface {
	SaveNodes(context.Context, []domain.Node) error
}

type nodeImportInput struct {
	Links string `json:"links"`
}

func (s *Server) registerNodeExtras(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/nodes/import", s.auth(http.HandlerFunc(s.importNodes)))
	mux.Handle("POST /api/v1/nodes/{id}/probe", s.auth(http.HandlerFunc(s.probeNode)))
}

func (s *Server) importNodes(w http.ResponseWriter, r *http.Request) {
	var input nodeImportInput
	if err := decode(r, &input); err != nil {
		problem(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	nodes, err := mihomo.ParseShareLinks(input.Links)
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "invalid_share_links", err)
		return
	}
	for index := range nodes {
		nodes[index].ID = randomID()
	}
	atomic, ok := s.nodes.(AtomicNodeStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "atomic_import_unavailable", errors.New("node store does not support atomic import"))
		return
	}
	if err := atomic.SaveNodes(r.Context(), nodes); err != nil {
		problem(w, http.StatusConflict, "import_failed", err)
		return
	}
	outputs := make([]nodeOutput, 0, len(nodes))
	for _, node := range nodes {
		outputs = append(outputs, output(node))
	}
	writeJSON(w, http.StatusCreated, map[string]any{"imported": len(outputs), "nodes": outputs})
}

func (s *Server) probeNode(w http.ResponseWriter, r *http.Request) {
	node, err := s.nodes.Node(r.Context(), r.PathValue("id"))
	if errors.Is(err, storepkg.ErrNotFound) {
		problem(w, http.StatusNotFound, "not_found", err)
		return
	}
	if err != nil {
		problem(w, http.StatusInternalServerError, "read_failed", err)
		return
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(node.Server, strconv.Itoa(node.Port)))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"reachable": false, "latencyMs": time.Since(started).Milliseconds(), "error": "tcp_connect_failed"})
		return
	}
	_ = connection.Close()
	writeJSON(w, http.StatusOK, map[string]any{"reachable": true, "latencyMs": time.Since(started).Milliseconds()})
}
