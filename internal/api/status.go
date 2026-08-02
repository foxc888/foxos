package api

import (
	"context"
	"net/http"
	"time"

	"github.com/foxc888/foxos/internal/mihomo"
	"github.com/foxc888/foxos/internal/mosdns"
	"github.com/foxc888/foxos/internal/routeros"
)

type RouterOSReader interface {
	Resource(context.Context) (routeros.Resource, error)
	Interfaces(context.Context) ([]routeros.Interface, error)
	Devices(context.Context) ([]routeros.Device, error)
}
type RouterOSExtendedReader interface {
	RouterOSReader
	Routes(context.Context) ([]routeros.Route, error)
	DHCPServers(context.Context) ([]routeros.DHCPServer, error)
	Containers(context.Context) ([]routeros.Container, error)
}
type MihomoReader interface{ Healthy(context.Context) error }
type MihomoStatusReader interface {
	Status(context.Context) (mihomo.RuntimeStatus, error)
}
type MosDNSReader interface {
	Status(context.Context) mosdns.Status
}

func (s *Server) RegisterStatus(mux *http.ServeMux, ros RouterOSReader, mihomo MihomoReader) {
	mux.Handle("GET /api/v1/routeros/overview", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ros == nil {
			writeJSON(w, http.StatusOK, map[string]any{"configured": false, "online": false})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		resource, err := ros.Resource(ctx)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"configured": true, "online": false, "error": "routeros_unavailable"})
			return
		}
		interfaces, interfacesErr := ros.Interfaces(ctx)
		devices, devicesErr := ros.Devices(ctx)
		partialErrors := map[string]string{}
		if interfacesErr != nil {
			partialErrors["interfaces"] = "unavailable"
		}
		if devicesErr != nil {
			partialErrors["devices"] = "unavailable"
		}
		response := map[string]any{"configured": true, "online": true, "resource": resource, "interfaces": interfaces, "devices": devices}
		if len(partialErrors) > 0 {
			response["partialErrors"] = partialErrors
		}
		writeJSON(w, http.StatusOK, response)
	})))
	mux.Handle("GET /api/v1/mihomo/overview", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mihomo == nil {
			writeJSON(w, http.StatusOK, map[string]any{"configured": false, "online": false})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := mihomo.Healthy(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"configured": true, "online": false, "error": "mihomo_unavailable"})
			return
		}
		response := map[string]any{"configured": true, "online": true}
		if statusReader, ok := mihomo.(MihomoStatusReader); ok {
			if runtimeStatus, statusErr := statusReader.Status(ctx); statusErr == nil {
				response["version"] = runtimeStatus.Version
				response["proxies"] = runtimeStatus.Proxies
				response["connections"] = runtimeStatus.Connections
				response["traffic"] = runtimeStatus.Traffic
				response["partial"] = runtimeStatus.Partial
			}
		}
		writeJSON(w, http.StatusOK, response)
	})))
	if extended, ok := ros.(RouterOSExtendedReader); ok {
		registerRouterOSCollection(mux, s, "routes", func(ctx context.Context) (any, error) { return extended.Routes(ctx) })
		registerRouterOSCollection(mux, s, "dhcp-servers", func(ctx context.Context) (any, error) { return extended.DHCPServers(ctx) })
		registerRouterOSCollection(mux, s, "containers", func(ctx context.Context) (any, error) { return extended.Containers(ctx) })
	}
}

func registerRouterOSCollection(mux *http.ServeMux, server *Server, name string, read func(context.Context) (any, error)) {
	mux.Handle("GET /api/v1/routeros/"+name, server.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		value, err := read(ctx)
		if err != nil {
			problemCode(w, http.StatusServiceUnavailable, "routeros_"+name+"_unavailable")
			return
		}
		writeJSON(w, http.StatusOK, value)
	})))
}

func (s *Server) RegisterMosDNS(mux *http.ServeMux, reader MosDNSReader) {
	mux.Handle("GET /api/v1/mosdns/overview", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reader == nil {
			writeJSON(w, http.StatusOK, mosdns.Status{Configured: false, Online: false})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		status := reader.Status(ctx)
		if !status.Online && status.Configured {
			writeJSON(w, http.StatusServiceUnavailable, status)
			return
		}
		writeJSON(w, http.StatusOK, status)
	})))
}
