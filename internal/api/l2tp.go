package api

import (
	"context"
	"net/http"\n\t"strings"
	"time"

	"github.com/foxc888/foxos/internal/routeros"
)

type L2TPReader interface{L2TPClients(context.Context)([]routeros.L2TPClient,error)}

type l2tpOutput struct{
	ID string `json:"id"`
	Name string `json:"name"`
	ConnectTo string `json:"connectTo"`
	User string `json:"user"`
	Running bool `json:"running"`
	Disabled bool `json:"disabled"`
	OwnedByFoxOS bool `json:"ownedByFoxos"`
	AddDefaultRoute bool `json:"addDefaultRoute"`
	DefaultRouteDistance string `json:"defaultRouteDistance,omitempty"`
	UsePeerDNS bool `json:"usePeerDns"`
	Profile string `json:"profile,omitempty"`
}

func(s *Server)RegisterL2TP(mux *http.ServeMux,reader L2TPReader){
	mux.Handle("GET /api/v1/routeros/l2tp",s.auth(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if reader==nil{writeJSON(w,http.StatusServiceUnavailable,map[string]any{"configured":false,"error":"routeros_not_configured"});return}
		ctx,cancel:=context.WithTimeout(r.Context(),10*time.Second);defer cancel()
		clients,err:=reader.L2TPClients(ctx);if err!=nil{problem(w,http.StatusServiceUnavailable,"routeros_l2tp",err);return}
		out:=make([]l2tpOutput,0,len(clients))
		for _,client:=range clients{out=append(out,l2tpOutput{ID:client.ID,Name:client.Name,ConnectTo:client.ConnectTo,User:client.User,Running:client.Running=="true",Disabled:client.Disabled=="true",OwnedByFoxOS:strings.HasPrefix(client.Comment,"foxos:l2tp:"),AddDefaultRoute:client.AddDefaultRoute=="true",DefaultRouteDistance:client.DefaultRouteDistance,UsePeerDNS:client.UsePeerDNS=="true",Profile:client.Profile})}
		writeJSON(w,http.StatusOK,out)
	})))
}
