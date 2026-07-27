package routeros

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
)

var ErrPlanConflict = errors.New("RouterOS plan conflict")
var ErrBindingPlanStale = errors.New("RouterOS binding plan is stale")

const maxBindingOperations = 4

type LeaseState struct {
	ID         string `json:"id"`
	Address    string `json:"address"`
	MACAddress string `json:"macAddress"`
	Server     string `json:"server"`
	Dynamic    string `json:"dynamic"`
	Comment    string `json:"comment"`
	Disabled   string `json:"disabled"`
}

type Operation struct {
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	Body         map[string]string `json:"body,omitempty"`
	Summary      string            `json:"summary"`
	OwnedComment string            `json:"ownedComment"`
	Before       *LeaseState       `json:"before,omitempty"`
	After        *LeaseState       `json:"after,omitempty"`
	Rollback     *Operation        `json:"rollback,omitempty"`
}

type BindingPreState struct {
	Digest      string      `json:"digest"`
	Lease       *LeaseState `json:"lease,omitempty"`
	Server      DHCPServer  `json:"server"`
	Network     DHCPNetwork `json:"network"`
	AddressPool string      `json:"addressPool"`
}

type Plan struct {
	PolicyID             string          `json:"policyId"`
	ProtectedAddresses   []string        `json:"protectedAddresses"`
	Operations           []Operation     `json:"operations"`
	Warnings             []string        `json:"warnings"`
	RequiresConfirmation bool            `json:"requiresConfirmation"`
	PreState             BindingPreState `json:"preState"`
	FinalState           LeaseState      `json:"finalState"`
}

func PlanDeviceBinding(policy domain.DevicePolicy, state BindingState) (Plan, error) {
	if isProtectedAddress(policy.StaticIP, state.ProtectedAddresses) {
		return Plan{}, fmt.Errorf("%w: management address is protected", ErrPlanConflict)
	}
	if err := policy.Validate(); err != nil {
		return Plan{}, err
	}
	mac, err := CanonicalMAC(policy.MACAddress)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: invalid MAC", ErrPlanConflict)
	}
	server, network, poolRanges, err := validateBindingTopology(policy, state)
	if err != nil {
		return Plan{}, err
	}
	comment := "foxos:device:" + policy.ID
	var current *Lease
	for index := range state.Leases {
		lease := state.Leases[index]
		leaseMAC := normalizeMAC(lease.MACAddress)
		if lease.Address == policy.StaticIP && leaseMAC != mac {
			return Plan{}, fmt.Errorf("%w: IP %s already has a DHCP lease", ErrPlanConflict, policy.StaticIP)
		}
		if leaseMAC != mac {
			continue
		}
		if current != nil {
			return Plan{}, fmt.Errorf("%w: multiple leases exist for device MAC", ErrPlanConflict)
		}
		copy := lease
		current = &copy
	}
	for _, item := range state.ARP {
		if item.Address == policy.StaticIP && normalizeMAC(item.MACAddress) != "" && normalizeMAC(item.MACAddress) != mac {
			return Plan{}, fmt.Errorf("%w: IP %s is present in ARP for another MAC", ErrPlanConflict, policy.StaticIP)
		}
	}
	for _, item := range state.Addresses {
		address, _, parseErr := net.ParseCIDR(item.Address)
		if parseErr == nil && address.String() == policy.StaticIP {
			return Plan{}, fmt.Errorf("%w: IP %s is assigned to RouterOS", ErrPlanConflict, policy.StaticIP)
		}
	}
	if current == nil && addressInRanges(policy.StaticIP, poolRanges) {
		return Plan{}, fmt.Errorf("%w: new static address must be in the reserved range outside the dynamic pool", ErrPlanConflict)
	}
	if current != nil {
		if current.Dynamic == "true" {
			if current.Server != policy.DHCPServer || strings.TrimSpace(current.Comment) != "" {
				return Plan{}, fmt.Errorf("%w: dynamic lease is not an ordinary lease on the selected DHCP server", ErrPlanConflict)
			}
			if current.Address != policy.StaticIP && addressInRanges(policy.StaticIP, poolRanges) {
				return Plan{}, fmt.Errorf("%w: changed static address must be in the reserved range outside the dynamic pool", ErrPlanConflict)
			}
		} else if current.Comment != comment {
			return Plan{}, fmt.Errorf("%w: existing static lease is not owned by this FoxOS policy", ErrPlanConflict)
		}
	}

	digest := BindingStateDigest(state)
	preState := BindingPreState{Digest: digest, Server: server, Network: network, AddressPool: server.AddressPool}
	if current != nil {
		leaseState := normalizedLeaseState(*current)
		preState.Lease = &leaseState
	}
	desired := LeaseState{Address: policy.StaticIP, MACAddress: mac, Server: policy.DHCPServer, Dynamic: "false", Comment: comment, Disabled: "false"}
	body := leaseStateBody(desired)
	plan := Plan{PolicyID: policy.ID, ProtectedAddresses: normalizedProtectedAddresses(state.ProtectedAddresses), RequiresConfirmation: true, PreState: preState, FinalState: desired}
	if current == nil {
		plan.Operations = []Operation{{Method: http.MethodPut, Path: "/rest/ip/dhcp-server/lease", Body: body, Summary: "创建 FoxOS 管理的静态 DHCP 租约", OwnedComment: comment, After: statePointer(desired)}}
		plan.Warnings = []string{"设备当前没有 DHCP 租约；应用后设备需要重新获取地址"}
		return plan, nil
	}
	if strings.TrimSpace(current.ID) == "" || !safeRouterOSID(current.ID) {
		return Plan{}, fmt.Errorf("%w: existing lease has an invalid RouterOS ID", ErrPlanConflict)
	}
	desired.ID = current.ID
	plan.FinalState = desired
	before := normalizedLeaseState(*current)
	if before.Address == desired.Address && before.Dynamic != "true" && before.Comment == comment && before.Server == desired.Server && before.Disabled == desired.Disabled {
		plan.RequiresConfirmation = false
		return plan, nil
	}
	if current.Dynamic == "true" {
		madeStatic := before
		madeStatic.Dynamic = "false"
		makeStaticRollback := &Operation{Method: http.MethodDelete, Path: "/rest/ip/dhcp-server/lease/" + current.ID, Summary: "删除采用失败的临时静态租约", OwnedComment: comment}
		makeStatic := Operation{
			Method: http.MethodPost, Path: "/rest/ip/dhcp-server/lease/make-static", Body: map[string]string{".id": current.ID},
			Summary: "采用当前动态租约并转为静态", OwnedComment: comment, Before: statePointer(before), After: statePointer(madeStatic), Rollback: makeStaticRollback,
		}
		patchRollback := &Operation{Method: http.MethodPatch, Path: "/rest/ip/dhcp-server/lease/" + current.ID, Body: leaseStateBody(madeStatic), Summary: "恢复采用后的静态租约原值", OwnedComment: comment}
		patch := Operation{Method: http.MethodPatch, Path: "/rest/ip/dhcp-server/lease/" + current.ID, Body: body, Summary: "写入 FoxOS 所有权和固定地址", OwnedComment: comment, Before: statePointer(madeStatic), After: statePointer(desired), Rollback: patchRollback}
		plan.Operations = []Operation{makeStatic, patch}
		plan.Warnings = append(plan.Warnings, "将采用当前动态租约并转换为 FoxOS 管理的静态租约")
	} else {
		rollback := &Operation{Method: http.MethodPatch, Path: "/rest/ip/dhcp-server/lease/" + current.ID, Body: leaseStateBody(before), Summary: "恢复 FoxOS 租约原值", OwnedComment: comment}
		plan.Operations = []Operation{{Method: http.MethodPatch, Path: "/rest/ip/dhcp-server/lease/" + current.ID, Body: body, Summary: "更新已有 FoxOS 静态租约", OwnedComment: comment, Before: statePointer(before), After: statePointer(desired), Rollback: rollback}}
	}
	if current.Address != policy.StaticIP {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("设备地址将从 %s 改为 %s", current.Address, policy.StaticIP))
	}
	if len(plan.Operations) > maxBindingOperations {
		return Plan{}, fmt.Errorf("%w: too many operations", ErrPlanConflict)
	}
	return plan, nil
}

func validateBindingTopology(policy domain.DevicePolicy, state BindingState) (DHCPServer, DHCPNetwork, []ipRange, error) {
	var server *DHCPServer
	for index := range state.DHCPServers {
		if state.DHCPServers[index].Name == policy.DHCPServer {
			if server != nil {
				return DHCPServer{}, DHCPNetwork{}, nil, fmt.Errorf("%w: DHCP server name is ambiguous", ErrPlanConflict)
			}
			copy := state.DHCPServers[index]
			server = &copy
		}
	}
	if server == nil || server.Disabled == "true" || server.Interface == "" {
		return DHCPServer{}, DHCPNetwork{}, nil, fmt.Errorf("%w: selected DHCP server is unavailable", ErrPlanConflict)
	}
	target, err := netip.ParseAddr(policy.StaticIP)
	if err != nil || !target.Is4() {
		return DHCPServer{}, DHCPNetwork{}, nil, fmt.Errorf("%w: static address is not IPv4", ErrPlanConflict)
	}
	var selected *DHCPNetwork
	var prefix netip.Prefix
	for index := range state.Networks {
		candidate, parseErr := netip.ParsePrefix(state.Networks[index].Address)
		if parseErr != nil || !candidate.Addr().Is4() || !candidate.Contains(target) {
			continue
		}
		candidate = candidate.Masked()
		if selected == nil || candidate.Bits() > prefix.Bits() {
			copy := state.Networks[index]
			selected, prefix = &copy, candidate
		} else if candidate.Bits() == prefix.Bits() {
			return DHCPServer{}, DHCPNetwork{}, nil, fmt.Errorf("%w: DHCP network is ambiguous", ErrPlanConflict)
		}
	}
	if selected == nil || target == prefix.Addr() || target == prefixLast(prefix) {
		return DHCPServer{}, DHCPNetwork{}, nil, fmt.Errorf("%w: address is outside a usable DHCP network host range", ErrPlanConflict)
	}
	interfaceMatch := false
	for _, item := range state.Addresses {
		if item.Interface != server.Interface || item.Disabled == "true" {
			continue
		}
		interfacePrefix, parseErr := netip.ParsePrefix(item.Address)
		if parseErr == nil && interfacePrefix.Addr().Is4() && interfacePrefix.Contains(target) {
			interfaceMatch = true
			break
		}
	}
	if !interfaceMatch {
		return DHCPServer{}, DHCPNetwork{}, nil, fmt.Errorf("%w: address is outside the selected DHCP server interface subnet", ErrPlanConflict)
	}
	ranges, err := poolRanges(*server, state.Pools)
	if err != nil {
		return DHCPServer{}, DHCPNetwork{}, nil, fmt.Errorf("%w: %v", ErrPlanConflict, err)
	}
	return *server, *selected, ranges, nil
}

type ipRange struct{ first, last uint32 }

func poolRanges(server DHCPServer, pools []IPPool) ([]ipRange, error) {
	if server.AddressPool == "static-only" {
		return nil, nil
	}
	byName := make(map[string]IPPool, len(pools))
	for _, pool := range pools {
		if pool.Name == "" {
			continue
		}
		if _, found := byName[pool.Name]; found {
			return nil, errors.New("address pool name is ambiguous")
		}
		byName[pool.Name] = pool
	}
	name := server.AddressPool
	seen := make(map[string]struct{})
	var output []ipRange
	for name != "" && name != "none" && name != "static-only" {
		if _, found := seen[name]; found {
			return nil, errors.New("address pool chain contains a cycle")
		}
		seen[name] = struct{}{}
		pool, found := byName[name]
		if !found {
			return nil, fmt.Errorf("address pool %q is missing", name)
		}
		for _, raw := range strings.Split(pool.Ranges, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			parts := strings.Split(raw, "-")
			if len(parts) > 2 {
				return nil, fmt.Errorf("invalid address range %q", raw)
			}
			first, err := ipv4Number(parts[0])
			if err != nil {
				return nil, fmt.Errorf("invalid address range %q", raw)
			}
			last := first
			if len(parts) == 2 {
				last, err = ipv4Number(parts[1])
				if err != nil || last < first {
					return nil, fmt.Errorf("invalid address range %q", raw)
				}
			}
			output = append(output, ipRange{first: first, last: last})
		}
		name = pool.NextPool
	}
	return output, nil
}

func addressInRanges(value string, ranges []ipRange) bool {
	number, err := ipv4Number(value)
	if err != nil {
		return false
	}
	for _, candidate := range ranges {
		if number >= candidate.first && number <= candidate.last {
			return true
		}
	}
	return false
}

func ipv4Number(value string) (uint32, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || !address.Is4() {
		return 0, errors.New("not IPv4")
	}
	bytes := address.As4()
	return binary.BigEndian.Uint32(bytes[:]), nil
}

func prefixLast(prefix netip.Prefix) netip.Addr {
	bytes := prefix.Masked().Addr().As4()
	number := binary.BigEndian.Uint32(bytes[:])
	hostBits := 32 - prefix.Bits()
	if hostBits > 0 {
		number |= uint32(1<<hostBits) - 1
	}
	var output [4]byte
	binary.BigEndian.PutUint32(output[:], number)
	return netip.AddrFrom4(output)
}

func BindingStateDigest(state BindingState) string {
	normalized := struct {
		Leases      []LeaseState
		Pools       []IPPool
		Networks    []DHCPNetwork
		Addresses   []IPAddress
		ARP         []ARP
		DHCPServers []DHCPServer
	}{Pools: append([]IPPool(nil), state.Pools...), Networks: append([]DHCPNetwork(nil), state.Networks...), Addresses: append([]IPAddress(nil), state.Addresses...), ARP: append([]ARP(nil), state.ARP...), DHCPServers: append([]DHCPServer(nil), state.DHCPServers...)}
	for _, lease := range state.Leases {
		normalized.Leases = append(normalized.Leases, normalizedLeaseState(lease))
	}
	sort.Slice(normalized.Leases, func(i, j int) bool { return leaseStateKey(normalized.Leases[i]) < leaseStateKey(normalized.Leases[j]) })
	sort.Slice(normalized.Pools, func(i, j int) bool { return normalized.Pools[i].Name < normalized.Pools[j].Name })
	sort.Slice(normalized.Networks, func(i, j int) bool { return normalized.Networks[i].Address < normalized.Networks[j].Address })
	sort.Slice(normalized.Addresses, func(i, j int) bool {
		return normalized.Addresses[i].Address+normalized.Addresses[i].Interface < normalized.Addresses[j].Address+normalized.Addresses[j].Interface
	})
	sort.Slice(normalized.ARP, func(i, j int) bool {
		return normalized.ARP[i].Address+normalizeMAC(normalized.ARP[i].MACAddress) < normalized.ARP[j].Address+normalizeMAC(normalized.ARP[j].MACAddress)
	})
	sort.Slice(normalized.DHCPServers, func(i, j int) bool { return normalized.DHCPServers[i].Name < normalized.DHCPServers[j].Name })
	body, _ := json.Marshal(normalized)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func normalizedLeaseState(lease Lease) LeaseState {
	disabled := lease.Disabled
	if disabled == "" {
		disabled = "false"
	}
	dynamic := lease.Dynamic
	if dynamic == "" {
		dynamic = "false"
	}
	return LeaseState{ID: lease.ID, Address: lease.Address, MACAddress: normalizeMAC(lease.MACAddress), Server: lease.Server, Dynamic: dynamic, Comment: lease.Comment, Disabled: disabled}
}

func leaseStateKey(state LeaseState) string {
	return state.ID + "\x00" + state.Address + "\x00" + state.MACAddress + "\x00" + state.Server + "\x00" + state.Dynamic + "\x00" + state.Comment + "\x00" + state.Disabled
}

func leaseStateBody(state LeaseState) map[string]string {
	return map[string]string{"address": state.Address, "mac-address": state.MACAddress, "server": state.Server, "comment": state.Comment, "disabled": state.Disabled}
}

func statePointer(value LeaseState) *LeaseState {
	copy := value
	return &copy
}

func CanonicalMAC(value string) (string, error) {
	mac, err := net.ParseMAC(value)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(mac.String()), nil
}

func safeRouterOSID(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if index == 0 && char != '*' {
			return false
		}
		if index > 0 && !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}
