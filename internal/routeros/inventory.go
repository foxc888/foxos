package routeros

import (
	"context"
	"sort"
	"strings"
)

type Device struct {
	MACAddress string `json:"macAddress"`
	Address    string `json:"address"`
	HostName   string `json:"hostName,omitempty"`
	Interface  string `json:"interface,omitempty"`
	DHCPServer string `json:"dhcpServer,omitempty"`
	Status     string `json:"status"`
	Dynamic    bool   `json:"dynamic"`
	LastSeen   string `json:"lastSeen,omitempty"`
}

func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	leases, err := c.Leases(ctx)
	if err != nil {
		return nil, err
	}
	arps, err := c.ARP(ctx)
	if err != nil {
		return nil, err
	}
	byMAC := make(map[string]Device, len(leases)+len(arps))
	for _, lease := range leases {
		key := normalizeMAC(lease.MACAddress)
		if key == "" {
			continue
		}
		byMAC[key] = Device{MACAddress: key, Address: lease.Address, HostName: lease.HostName, DHCPServer: lease.Server, Status: lease.Status, Dynamic: lease.Dynamic == "true", LastSeen: lease.LastSeen}
	}
	for _, arp := range arps {
		key := normalizeMAC(arp.MACAddress)
		if key == "" {
			continue
		}
		device := byMAC[key]
		device.MACAddress = key
		if device.Address == "" {
			device.Address = arp.Address
		}
		device.Interface = arp.Interface
		if device.Status == "" {
			if arp.Complete == "true" {
				device.Status = "bound"
			} else {
				device.Status = "unknown"
			}
		}
		byMAC[key] = device
	}
	devices := make([]Device, 0, len(byMAC))
	for _, device := range byMAC {
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].Address == devices[j].Address {
			return devices[i].MACAddress < devices[j].MACAddress
		}
		return devices[i].Address < devices[j].Address
	})
	return devices, nil
}
func normalizeMAC(value string) string { return strings.ToUpper(strings.TrimSpace(value)) }
