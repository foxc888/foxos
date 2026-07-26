package domain

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

var ErrInvalidDevicePolicy=errors.New("invalid device policy")

type EgressType string
const(
	EgressDirect EgressType="direct"
	EgressMihomoNode EgressType="mihomo-node"
	EgressProxyChain EgressType="proxy-chain"
	EgressL2TP EgressType="l2tp"
	EgressBlocked EgressType="blocked"
)

type DevicePolicy struct{
	ID string
	Name string
	MACAddress string
	StaticIP string
	DHCPServer string
	Egress EgressType
	TargetID string
}

func(p DevicePolicy)Validate()error{
	if strings.TrimSpace(p.ID)==""||strings.TrimSpace(p.MACAddress)==""{return fmt.Errorf("%w: id and MAC are required",ErrInvalidDevicePolicy)}
	if _,err:=net.ParseMAC(p.MACAddress);err!=nil{return fmt.Errorf("%w: invalid MAC",ErrInvalidDevicePolicy)}
	ip:=net.ParseIP(p.StaticIP);if ip==nil||ip.To4()==nil{return fmt.Errorf("%w: static IPv4 is required",ErrInvalidDevicePolicy)}
	if p.DHCPServer==""{return fmt.Errorf("%w: DHCP server is required",ErrInvalidDevicePolicy)}
	switch p.Egress{
	case EgressDirect,EgressBlocked:
		if p.TargetID!=""{return fmt.Errorf("%w: target is not allowed for %s",ErrInvalidDevicePolicy,p.Egress)}
	case EgressMihomoNode,EgressProxyChain,EgressL2TP:
		if p.TargetID==""{return fmt.Errorf("%w: target is required for %s",ErrInvalidDevicePolicy,p.Egress)}
	default:return fmt.Errorf("%w: unsupported egress",ErrInvalidDevicePolicy)
	}
	return nil
}
