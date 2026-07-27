package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/foxc888/foxos/internal/domain"
	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
)

type DevicePolicyStore interface {
	SaveDevicePolicy(context.Context, domain.DevicePolicy) error
	DevicePolicy(context.Context, string) (domain.DevicePolicy, error)
	DevicePolicies(context.Context) ([]domain.DevicePolicy, error)
	DeleteDevicePolicy(context.Context, string) error
}
type devicePolicyPayload struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	MACAddress string            `json:"macAddress"`
	StaticIP   string            `json:"staticIp"`
	DHCPServer string            `json:"dhcpServer"`
	Egress     domain.EgressType `json:"egress"`
	TargetID   string            `json:"targetId,omitempty"`
}

func (p devicePolicyPayload) domain() domain.DevicePolicy {
	return domain.DevicePolicy{ID: p.ID, Name: p.Name, MACAddress: p.MACAddress, StaticIP: p.StaticIP, DHCPServer: p.DHCPServer, Egress: p.Egress, TargetID: p.TargetID}
}
func policyPayload(p domain.DevicePolicy) devicePolicyPayload {
	return devicePolicyPayload{ID: p.ID, Name: p.Name, MACAddress: p.MACAddress, StaticIP: p.StaticIP, DHCPServer: p.DHCPServer, Egress: p.Egress, TargetID: p.TargetID}
}

func (s *Server) RegisterDevicePolicies(mux *http.ServeMux, store DevicePolicyStore) {
	mux.Handle("GET /api/v1/device-policies", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := store.DevicePolicies(r.Context())
		if err != nil {
			problem(w, 500, "list_failed", err)
			return
		}
		out := make([]devicePolicyPayload, 0, len(items))
		for _, item := range items {
			out = append(out, policyPayload(item))
		}
		writeJSON(w, 200, out)
	})))
	mux.Handle("POST /api/v1/device-policies", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", http.MethodGet)
		problemCode(w, http.StatusMethodNotAllowed, "egress_workflow_required")
	})))
	mux.Handle("GET /api/v1/device-policies/{id}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy, err := store.DevicePolicy(r.Context(), r.PathValue("id"))
		if errors.Is(err, storepkg.ErrNotFound) {
			problem(w, 404, "not_found", err)
			return
		}
		if err != nil {
			problem(w, 500, "read_failed", err)
			return
		}
		writeJSON(w, 200, policyPayload(policy))
	})))
	mux.Handle("PUT /api/v1/device-policies/{id}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", http.MethodGet)
		problemCode(w, http.StatusMethodNotAllowed, "egress_workflow_required")
	})))
	mux.Handle("DELETE /api/v1/device-policies/{id}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", http.MethodGet)
		problemCode(w, http.StatusMethodNotAllowed, "egress_workflow_required")
	})))
}
