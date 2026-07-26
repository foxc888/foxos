package api

import (
	"context"
	"net/http"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/routeros"
)

type LeaseReader interface{Leases(context.Context)([]routeros.Lease,error)}

type bindingInput struct{
	ID string `json:"id"`
	Name string `json:"name"`
	MACAddress string `json:"macAddress"`
	StaticIP string `json:"staticIp"`
	DHCPServer string `json:"dhcpServer"`
	Egress domain.EgressType `json:"egress"`
	TargetID string `json:"targetId,omitempty"`
}

func(s *Server)RegisterBindingPlan(mux *http.ServeMux,reader LeaseReader){
	mux.Handle("POST /api/v1/routeros/plans/device-binding",s.auth(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if reader==nil{writeJSON(w,http.StatusServiceUnavailable,map[string]any{"configured":false,"error":"routeros_not_configured"});return}
		var input bindingInput;if err:=decode(r,&input);err!=nil{problem(w,http.StatusBadRequest,"invalid_json",err);return}
		ctx,cancel:=context.WithTimeout(r.Context(),10*time.Second);defer cancel()
		leases,err:=reader.Leases(ctx);if err!=nil{problem(w,http.StatusServiceUnavailable,"routeros_leases",err);return}
		plan,err:=routeros.PlanDeviceBinding(domain.DevicePolicy{ID:input.ID,Name:input.Name,MACAddress:input.MACAddress,StaticIP:input.StaticIP,DHCPServer:input.DHCPServer,Egress:input.Egress,TargetID:input.TargetID},leases)
		if err!=nil{problem(w,http.StatusConflict,"binding_conflict",err);return}
		writeJSON(w,http.StatusOK,plan)
	})))
}
