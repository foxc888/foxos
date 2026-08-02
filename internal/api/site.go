package api

import (
	"net/http"
	"strconv"

	"github.com/foxc888/foxos/internal/site"
)

type HTTPSAccess struct {
	Enabled       bool
	CAFingerprint string
	CACertificate []byte
}

type siteServiceOutput struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	URL     string `json:"url,omitempty"`
}

type siteOutput struct {
	ManagementBridge   string                       `json:"managementBridge"`
	StorageRoot        string                       `json:"storageRoot"`
	Network            string                       `json:"network"`
	PublicHostname     string                       `json:"publicHostname"`
	PublicURL          string                       `json:"publicUrl,omitempty"`
	HTTPRedirectURL    string                       `json:"httpRedirectUrl,omitempty"`
	ProtectedAddresses []string                     `json:"protectedAddresses"`
	Services           map[string]siteServiceOutput `json:"services"`
	HTTPS              siteHTTPSOutput              `json:"https"`
}

type siteHTTPSOutput struct {
	Enabled        bool   `json:"enabled"`
	CAFingerprint  string `json:"caSha256,omitempty"`
	CADownloadPath string `json:"caDownloadPath,omitempty"`
	TrustRequired  bool   `json:"trustRequired"`
}

func (s *Server) RegisterSite(mux *http.ServeMux, config site.Config, access HTTPSAccess) {
	output := siteOutput{
		ManagementBridge:   config.ManagementBridge,
		StorageRoot:        config.StorageRoot,
		Network:            config.Network,
		PublicHostname:     config.PublicHostname,
		ProtectedAddresses: config.ProtectedAddresses(),
		Services: map[string]siteServiceOutput{
			"routeros": {Address: config.RouterAddress, Port: site.RouterOSPort, URL: config.RouterOSURL()},
			"mihomo":   {Address: config.MihomoAddress, Port: site.MihomoControllerPort, URL: config.MihomoURL()},
			"mosdns":   {Address: config.MosDNSAddress, Port: site.MosDNSPort, URL: config.MosDNSURL()},
			"foxos":    {Address: config.FoxOSAddress, Port: site.FoxOSInternalPort, URL: config.InternalURL()},
		},
		HTTPS: siteHTTPSOutput{Enabled: access.Enabled, TrustRequired: access.Enabled},
	}
	if access.Enabled {
		output.PublicURL = config.PublicURL()
		output.HTTPRedirectURL = config.HTTPRedirectURL()
		output.Services["foxos"] = siteServiceOutput{Address: config.FoxOSAddress, Port: 443, URL: config.PublicURL()}
		output.HTTPS.CAFingerprint = access.CAFingerprint
		output.HTTPS.CADownloadPath = "/api/v1/site/ca"
	}
	mux.HandleFunc("GET /api/v1/site", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, output)
	})
	mux.HandleFunc("GET /api/v1/site/ca", func(w http.ResponseWriter, _ *http.Request) {
		if !access.Enabled || len(access.CACertificate) == 0 {
			problemCode(w, http.StatusNotFound, "site_ca_unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", `attachment; filename="foxos-local-ca.pem"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(access.CACertificate)))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(access.CACertificate)
	})
}
