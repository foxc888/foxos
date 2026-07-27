package domain

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

var ErrInvalidDevicePolicy = errors.New("invalid device policy")

type EgressType string

const (
	EgressDirect     EgressType = "direct"
	EgressMihomoNode EgressType = "mihomo-node"
	EgressProxyChain EgressType = "proxy-chain"
	EgressL2TP       EgressType = "l2tp"
	EgressBlocked    EgressType = "blocked"
)

type DevicePolicy struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	MACAddress string     `json:"macAddress"`
	StaticIP   string     `json:"staticIp"`
	DHCPServer string     `json:"dhcpServer"`
	Egress     EgressType `json:"egress"`
	TargetID   string     `json:"targetId,omitempty"`
}

func (p DevicePolicy) Validate() error {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.MACAddress) == "" {
		return fmt.Errorf("%w: id and MAC are required", ErrInvalidDevicePolicy)
	}
	if _, err := net.ParseMAC(p.MACAddress); err != nil {
		return fmt.Errorf("%w: invalid MAC", ErrInvalidDevicePolicy)
	}
	ip := net.ParseIP(p.StaticIP)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("%w: static IPv4 is required", ErrInvalidDevicePolicy)
	}
	switch ip.To4().String() {
	case "10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4":
		return fmt.Errorf("%w: management address is protected", ErrInvalidDevicePolicy)
	}
	if p.DHCPServer == "" {
		return fmt.Errorf("%w: DHCP server is required", ErrInvalidDevicePolicy)
	}
	switch p.Egress {
	case EgressDirect, EgressBlocked:
		if p.TargetID != "" {
			return fmt.Errorf("%w: target is not allowed for %s", ErrInvalidDevicePolicy, p.Egress)
		}
	case EgressMihomoNode, EgressProxyChain, EgressL2TP:
		if p.TargetID == "" {
			return fmt.Errorf("%w: target is required for %s", ErrInvalidDevicePolicy, p.Egress)
		}
	default:
		return fmt.Errorf("%w: unsupported egress", ErrInvalidDevicePolicy)
	}
	return nil
}

func EqualDevicePolicies(left, right DevicePolicy) bool {
	leftMAC, leftErr := net.ParseMAC(left.MACAddress)
	rightMAC, rightErr := net.ParseMAC(right.MACAddress)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return strings.TrimSpace(left.ID) == strings.TrimSpace(right.ID) &&
		strings.TrimSpace(left.Name) == strings.TrimSpace(right.Name) &&
		strings.EqualFold(leftMAC.String(), rightMAC.String()) &&
		net.ParseIP(left.StaticIP).Equal(net.ParseIP(right.StaticIP)) &&
		strings.TrimSpace(left.DHCPServer) == strings.TrimSpace(right.DHCPServer) &&
		left.Egress == right.Egress &&
		strings.TrimSpace(left.TargetID) == strings.TrimSpace(right.TargetID)
}
