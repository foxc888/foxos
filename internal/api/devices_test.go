package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/routeros"
	"github.com/foxc888/foxos/internal/store/sqlite"
)

type inventoryReader struct {
	devices []routeros.Device
	err     error
}

func (f *inventoryReader) Resource(context.Context) (routeros.Resource, error) {
	return routeros.Resource{}, f.err
}
func (f *inventoryReader) Interfaces(context.Context) ([]routeros.Interface, error) {
	return nil, f.err
}
func (f *inventoryReader) Devices(context.Context) ([]routeros.Device, error) {
	return f.devices, f.err
}

func TestDeviceInventoryUsesUnavailableStateInsteadOfStaleOnline(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const token = "01234567890123456789012345678901"
	app, err := New(store, token)
	if err != nil {
		t.Fatal(err)
	}
	reader := &inventoryReader{devices: []routeros.Device{{MACAddress: "AA:BB:CC:DD:EE:FF", Address: "10.0.0.20", HostName: "laptop", Interface: "bridge-lan", DHCPServer: "dhcp-lan", Status: "bound"}}}
	mux := http.NewServeMux()
	app.RegisterDevices(mux, reader, store, store)

	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		return response
	}

	response := request(http.MethodGet, "/api/v1/devices", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var live deviceInventoryOutput
	if err := json.Unmarshal(response.Body.Bytes(), &live); err != nil {
		t.Fatal(err)
	}
	if !live.SourceAvailable || len(live.Devices) != 1 || !live.Devices[0].Online || live.Devices[0].Status != "online" {
		t.Fatalf("inventory=%+v", live)
	}

	response = request(http.MethodPut, "/api/v1/devices/AA:BB:CC:DD:EE:FF", `{"alias":"Office laptop","vendor":"Framework","tags":["work"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	reader.err = errors.New("router unavailable")
	response = request(http.MethodGet, "/api/v1/devices", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var unavailable deviceInventoryOutput
	if err := json.Unmarshal(response.Body.Bytes(), &unavailable); err != nil {
		t.Fatal(err)
	}
	if unavailable.SourceAvailable || len(unavailable.Devices) != 1 || unavailable.Devices[0].Online || !unavailable.Devices[0].LastKnownOnline || unavailable.Devices[0].Status != "unavailable" {
		t.Fatalf("inventory=%+v", unavailable)
	}
	if unavailable.Devices[0].Alias != "Office laptop" || unavailable.Devices[0].Vendor != "Framework" {
		t.Fatalf("metadata=%+v", unavailable.Devices[0])
	}
}

func TestDeviceInventoryRecordsOnlineHistoryTransitions(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const token = "01234567890123456789012345678901"
	app, err := New(store, token)
	if err != nil {
		t.Fatal(err)
	}
	reader := &inventoryReader{devices: []routeros.Device{{MACAddress: "AA:BB:CC:DD:EE:FF", Status: "bound"}}}
	mux := http.NewServeMux()
	app.RegisterDevices(mux, reader, store, store)
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		return response
	}
	if response := get("/api/v1/devices"); response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	reader.devices = nil
	if response := get("/api/v1/devices"); response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	response := get("/api/v1/devices/AA:BB:CC:DD:EE:FF/history?limit=10")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var history []struct {
		Online bool `json:"online"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Online || !history[1].Online {
		t.Fatalf("history=%+v", history)
	}
}
