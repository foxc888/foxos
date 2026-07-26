package api

import (
	"context"
	"net/http"
	"time"

	"github.com/foxc888/foxos/internal/routeros"
)

type RouterOSReader interface {
	Resource(context.Context)(routeros.Resource,error)
	Interfaces(context.Context)([]routeros.Interface,error)
	Devices(context.Context)([]routeros.Device,error)
}
type MihomoReader interface{Healthy(context.Context)error}

func(s *Server)RegisterStatus(mux *http.ServeMux,ros RouterOSReader,mihomo MihomoReader){
	mux.Handle("GET /api/v1/routeros/overview",s.auth(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if ros==nil{writeJSON(w,http.StatusOK,map[string]any{"configured":false,"online":false});return}
		ctx,cancel:=context.WithTimeout(r.Context(),10*time.Second);defer cancel()
		resource,err:=ros.Resource(ctx);if err!=nil{writeJSON(w,http.StatusServiceUnavailable,map[string]any{"configured":true,"online":false,"error":"routeros_unavailable"});return}
		interfaces,err:=ros.Interfaces(ctx);if err!=nil{problem(w,http.StatusServiceUnavailable,"routeros_interfaces",err);return}
		devices,err:=ros.Devices(ctx);if err!=nil{problem(w,http.StatusServiceUnavailable,"routeros_devices",err);return}
		writeJSON(w,http.StatusOK,map[string]any{"configured":true,"online":true,"resource":resource,"interfaces":interfaces,"devices":devices})
	})))
	mux.Handle("GET /api/v1/mihomo/overview",s.auth(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if mihomo==nil{writeJSON(w,http.StatusOK,map[string]any{"configured":false,"online":false});return}
		ctx,cancel:=context.WithTimeout(r.Context(),10*time.Second);defer cancel()
		if err:=mihomo.Healthy(ctx);err!=nil{writeJSON(w,http.StatusServiceUnavailable,map[string]any{"configured":true,"online":false,"error":"mihomo_unavailable"});return}
		writeJSON(w,http.StatusOK,map[string]any{"configured":true,"online":true})
	})))
}
