package domain

import (
	"errors"
	"net"
	"strings"
	"time"
	"unicode"
)

var ErrInvalidDeviceProfile = errors.New("invalid device profile")

type DeviceObservation struct {
	MACAddress string
	IPAddress  string
	HostName   string
	Interface  string
	DHCPServer string
	Online     bool
}

type DeviceProfile struct {
	MACAddress string
	Alias      string
	Tags       []string
	Vendor     string
	HostName   string
	IPAddress  string
	Interface  string
	DHCPServer string
	FirstSeen  time.Time
	LastSeen   time.Time
	Online     bool
	UpdatedAt  time.Time
}

type DevicePresenceEvent struct {
	ID         int64
	MACAddress string
	Online     bool
	ObservedAt time.Time
}

func NormalizeMAC(value string) (string, error) {
	hardware, err := net.ParseMAC(strings.TrimSpace(value))
	if err != nil || len(hardware) != 6 {
		return "", ErrInvalidDeviceProfile
	}
	return strings.ToUpper(hardware.String()), nil
}

func ValidateDeviceMetadata(alias, vendor string, tags []string) error {
	if !validProfileText(alias, 128) || !validProfileText(vendor, 128) || len(tags) > 16 {
		return ErrInvalidDeviceProfile
	}
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		if !validProfileText(tag, 32) || tag == "" {
			return ErrInvalidDeviceProfile
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			return ErrInvalidDeviceProfile
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validProfileText(value string, limit int) bool {
	if strings.TrimSpace(value) != value || len(value) > limit {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}
