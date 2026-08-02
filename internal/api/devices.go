package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

type DeviceInventoryStore interface {
	RecordDeviceSnapshot(context.Context, []domain.DeviceObservation, time.Time) error
	DeviceProfiles(context.Context) ([]domain.DeviceProfile, error)
	DeviceProfile(context.Context, string) (domain.DeviceProfile, error)
	UpdateDeviceMetadata(context.Context, string, string, string, []string) (domain.DeviceProfile, error)
	DevicePresenceHistory(context.Context, string, int) ([]domain.DevicePresenceEvent, error)
}

type deviceProfileOutput struct {
	MACAddress      string   `json:"macAddress"`
	Alias           string   `json:"alias,omitempty"`
	Tags            []string `json:"tags"`
	Vendor          string   `json:"vendor,omitempty"`
	HostName        string   `json:"hostName,omitempty"`
	IPAddress       string   `json:"ipAddress,omitempty"`
	Interface       string   `json:"interface,omitempty"`
	DHCPServer      string   `json:"dhcpServer,omitempty"`
	Online          bool     `json:"online"`
	LastKnownOnline bool     `json:"lastKnownOnline"`
	Status          string   `json:"status"`
	FirstSeen       string   `json:"firstSeen"`
	LastSeen        string   `json:"lastSeen,omitempty"`
	UpdatedAt       string   `json:"updatedAt"`
}

type deviceInventoryOutput struct {
	SourceAvailable bool                  `json:"sourceAvailable"`
	ObservedAt      string                `json:"observedAt,omitempty"`
	Error           string                `json:"error,omitempty"`
	Devices         []deviceProfileOutput `json:"devices"`
}

type deviceMetadataInput struct {
	Alias  string   `json:"alias"`
	Vendor string   `json:"vendor"`
	Tags   []string `json:"tags"`
}

func renderDeviceProfile(item domain.DeviceProfile, sourceAvailable bool) deviceProfileOutput {
	status := "unavailable"
	if sourceAvailable {
		status = "offline"
		if item.Online {
			status = "online"
		}
	}
	return deviceProfileOutput{
		MACAddress: item.MACAddress, Alias: item.Alias, Tags: item.Tags, Vendor: item.Vendor,
		HostName: item.HostName, IPAddress: item.IPAddress, Interface: item.Interface, DHCPServer: item.DHCPServer,
		Online: sourceAvailable && item.Online, LastKnownOnline: item.Online, Status: status,
		FirstSeen: formatTime(item.FirstSeen), LastSeen: formatTime(item.LastSeen), UpdatedAt: formatTime(item.UpdatedAt),
	}
}

func (s *Server) RegisterDevices(mux *http.ServeMux, reader RouterOSReader, store DeviceInventoryStore, audit AuditStore) {
	if store == nil {
		return
	}
	mux.Handle("GET /api/v1/devices", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceAvailable := false
		observedAt := time.Time{}
		errorCode := "routeros_not_configured"
		if reader != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			devices, err := reader.Devices(ctx)
			cancel()
			if err == nil {
				observedAt = time.Now().UTC()
				observations := make([]domain.DeviceObservation, 0, len(devices))
				for _, device := range devices {
					observations = append(observations, domain.DeviceObservation{
						MACAddress: device.MACAddress, IPAddress: device.Address, HostName: device.HostName,
						Interface: device.Interface, DHCPServer: device.DHCPServer, Online: strings.EqualFold(device.Status, "bound"),
					})
				}
				if err := store.RecordDeviceSnapshot(r.Context(), observations, observedAt); err != nil {
					problemCode(w, http.StatusInternalServerError, "device_snapshot_failed")
					return
				}
				sourceAvailable = true
				errorCode = ""
			} else {
				errorCode = "routeros_devices_unavailable"
			}
		}
		profiles, err := store.DeviceProfiles(r.Context())
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "device_profiles_failed")
			return
		}
		out := make([]deviceProfileOutput, 0, len(profiles))
		for _, profile := range profiles {
			out = append(out, renderDeviceProfile(profile, sourceAvailable))
		}
		writeJSON(w, http.StatusOK, deviceInventoryOutput{SourceAvailable: sourceAvailable, ObservedAt: formatTime(observedAt), Error: errorCode, Devices: out})
	})))

	mux.Handle("PUT /api/v1/devices/{mac}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input deviceMetadataInput
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		before, err := store.DeviceProfile(r.Context(), r.PathValue("mac"))
		if errors.Is(err, domain.ErrNotFound) {
			problemCode(w, http.StatusNotFound, "device_not_found")
			return
		}
		if err != nil {
			problemCode(w, http.StatusUnprocessableEntity, "invalid_device")
			return
		}
		if audit == nil {
			problemCode(w, http.StatusServiceUnavailable, "audit_unavailable")
			return
		}
		event := domain.AuditEvent{ID: randomID(), Action: "device.metadata", TargetID: before.MACAddress, Outcome: domain.AuditStarted, Details: requestAuditDetails(r, map[string]any{
			"before": map[string]any{"alias": before.Alias, "vendor": before.Vendor, "tags": before.Tags},
			"after":  map[string]any{"alias": input.Alias, "vendor": input.Vendor, "tags": input.Tags},
		})}
		if err := audit.SaveAudit(r.Context(), event); err != nil {
			problemCode(w, http.StatusInternalServerError, "audit_write_failed")
			return
		}
		profile, err := store.UpdateDeviceMetadata(r.Context(), before.MACAddress, input.Alias, input.Vendor, input.Tags)
		if err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "device_metadata_invalid"
			_ = audit.SaveAudit(r.Context(), event)
			if errors.Is(err, domain.ErrInvalidDeviceProfile) {
				problem(w, http.StatusUnprocessableEntity, "invalid_device_metadata", err)
				return
			}
			problemCode(w, http.StatusInternalServerError, "device_metadata_failed")
			return
		}
		event.Outcome = domain.AuditSucceeded
		if err := audit.SaveAudit(r.Context(), event); err != nil {
			problemCode(w, http.StatusInternalServerError, "audit_finalize_failed")
			return
		}
		writeJSON(w, http.StatusOK, renderDeviceProfile(profile, false))
	})))

	mux.Handle("GET /api/v1/devices/{mac}/history", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := store.DeviceProfile(r.Context(), r.PathValue("mac")); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				problemCode(w, http.StatusNotFound, "device_not_found")
				return
			}
			problemCode(w, http.StatusUnprocessableEntity, "invalid_device")
			return
		}
		limit := 100
		if value := r.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 500 {
				problemCode(w, http.StatusBadRequest, "invalid_limit")
				return
			}
			limit = parsed
		}
		items, err := store.DevicePresenceHistory(r.Context(), r.PathValue("mac"), limit)
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "device_history_failed")
			return
		}
		type historyOutput struct {
			ID         int64  `json:"id"`
			Online     bool   `json:"online"`
			ObservedAt string `json:"observedAt"`
		}
		out := make([]historyOutput, 0, len(items))
		for _, item := range items {
			out = append(out, historyOutput{ID: item.ID, Online: item.Online, ObservedAt: formatTime(item.ObservedAt)})
		}
		writeJSON(w, http.StatusOK, out)
	})))
}
