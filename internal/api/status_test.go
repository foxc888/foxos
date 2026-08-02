package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/foxc888/foxos/internal/domain"

	"github.com/foxc888/foxos/internal/routeros"
)

type fakeROS struct{}

func (fakeROS) Resource(context.Context) (routeros.Resource, error) {
	return routeros.Resource{Version: "7.15.2"}, nil
}
func (fakeROS) Interfaces(context.Context) ([]routeros.Interface, error) {
	return []routeros.Interface{{Name: "ether1"}}, nil
}
func (fakeROS) Devices(context.Context) ([]routeros.Device, error) {
	return []routeros.Device{{MACAddress: "AA:BB:CC:DD:EE:FF"}}, nil
}

type fakeMihomo struct{}

func (fakeMihomo) Healthy(context.Context) error { return nil }

func TestStatusEndpointsAreAuthenticated(t *testing.T) {
	const token = "01234567890123456789012345678901"
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.RegisterStatus(mux, fakeROS{}, fakeMihomo{})
	request := httptest.NewRequest("GET", "/api/v1/routeros/overview", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
