package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/mihomo"
)

type MihomoNodeProber interface {
	ProbeNodeHTTP(context.Context, string) (time.Duration, error)
}

type MihomoExitProber interface {
	Probe(context.Context) (mihomo.ExitResult, error)
}

type mihomoProbeCheck struct {
	Available bool   `json:"available"`
	Success   bool   `json:"success"`
	LatencyMS int64  `json:"latencyMs,omitempty"`
	Error     string `json:"error,omitempty"`
}

type mihomoExitCheck struct {
	mihomoProbeCheck
	Scope     string `json:"scope"`
	IPAddress string `json:"ipAddress,omitempty"`
}

func (s *Server) RegisterMihomoProbes(mux *http.ServeMux, nodeProbe MihomoNodeProber, exitProbe MihomoExitProber) {
	mux.Handle("POST /api/v1/mihomo/probes/{id}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nodeProbe == nil && exitProbe == nil {
			problemCode(w, http.StatusServiceUnavailable, "mihomo_probe_not_configured")
			return
		}
		node, err := s.nodes.Node(r.Context(), r.PathValue("id"))
		if errors.Is(err, domain.ErrNotFound) {
			problemCode(w, http.StatusNotFound, "node_not_found")
			return
		}
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "node_read_failed")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		httpCheck := mihomoProbeCheck{Available: nodeProbe != nil}
		exitCheck := mihomoExitCheck{mihomoProbeCheck: mihomoProbeCheck{Available: exitProbe != nil}, Scope: "current-policy"}
		var wait sync.WaitGroup
		if nodeProbe != nil {
			wait.Add(1)
			go func() {
				defer wait.Done()
				delay, err := nodeProbe.ProbeNodeHTTP(ctx, node.Name)
				if err != nil {
					httpCheck.Error = "mihomo_node_http_failed"
					return
				}
				httpCheck.Success = true
				httpCheck.LatencyMS = delay.Milliseconds()
			}()
		}
		if exitProbe != nil {
			wait.Add(1)
			go func() {
				defer wait.Done()
				result, err := exitProbe.Probe(ctx)
				if err != nil {
					exitCheck.Error = "mihomo_exit_failed"
					return
				}
				exitCheck.Success = true
				exitCheck.LatencyMS = result.Latency.Milliseconds()
				exitCheck.IPAddress = result.IPAddress
			}()
		}
		wait.Wait()
		writeJSON(w, http.StatusOK, map[string]any{
			"nodeId": node.ID, "nodeName": node.Name, "nodeHttp": httpCheck, "exit": exitCheck,
		})
	})))
}
