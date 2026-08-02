package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/site"
)

func TestSiteAPIUsesRuntimeManifestWithoutAuthentication(t *testing.T) {
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	config := site.Default()
	config.Network = "192.168.50.0/24"
	config.RouterAddress = "192.168.50.1"
	config.MihomoAddress = "192.168.50.2"
	config.MosDNSAddress = "192.168.50.3"
	config.FoxOSAddress = "192.168.50.4"
	mux := http.NewServeMux()
	app.RegisterSite(mux, config, HTTPSAccess{Enabled: true, CAFingerprint: strings.Repeat("a", 64), CACertificate: []byte("CA")})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/site", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var output siteOutput
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.PublicURL != "https://foxos.home.arpa" || output.Services["mihomo"].Address != "192.168.50.2" || len(output.ProtectedAddresses) != 4 {
		t.Fatalf("unexpected site output: %+v", output)
	}
	ca := httptest.NewRecorder()
	mux.ServeHTTP(ca, httptest.NewRequest(http.MethodGet, "/api/v1/site/ca", nil))
	if ca.Code != http.StatusOK || ca.Body.String() != "CA" {
		t.Fatalf("CA status=%d body=%q", ca.Code, ca.Body.String())
	}
}

func TestSiteCAIsUnavailableWhenHTTPSIsDisabled(t *testing.T) {
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.RegisterSite(mux, site.Default(), HTTPSAccess{})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/site/ca", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}
}
