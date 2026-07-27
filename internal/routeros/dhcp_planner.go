package routeros

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
)

var (
	ErrDHCPPlan              = errors.New("invalid DHCP address plan")
	ErrDHCPExpansionStale    = errors.New("DHCP expansion plan is stale")
	ErrDHCPExpansionRollback = errors.New("DHCP expansion was rolled back")
)

const (
	maxDHCPPlanningResources = 8192
	maxDHCPPlanningServers   = 128
	maxDHCPPlanningRanges    = 256
	maxDHCPPlanningHosts     = 65534
)

type DHCPRange struct {
	Start    string `json:"start"`
	End      string `json:"end"`
	Capacity int    `json:"capacity"`
}

type DHCPAddressClaim struct {
	Address string `json:"address"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail"`
}

type DHCPConflict struct {
	Address string `json:"address,omitempty"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail"`
}

type DHCPServerCapacity struct {
	ServerName               string             `json:"serverName"`
	Interface                string             `json:"interface"`
	Running                  bool               `json:"running"`
	PoolNames                []string           `json:"poolNames"`
	Network                  string             `json:"network,omitempty"`
	Gateway                  string             `json:"gateway,omitempty"`
	Ranges                   []DHCPRange        `json:"ranges"`
	ConfiguredCapacity       int                `json:"configuredCapacity"`
	ExcludedWithinPool       int                `json:"excludedWithinPool"`
	DynamicCapacity          int                `json:"dynamicCapacity"`
	DynamicOccupied          int                `json:"dynamicOccupied"`
	Remaining                int                `json:"remaining"`
	UtilizationPercent       float64            `json:"utilizationPercent"`
	ReservationSpaceCapacity int                `json:"reservationSpaceCapacity"`
	ReservationUsed          int                `json:"reservationUsed"`
	ReservationRemaining     int                `json:"reservationRemaining"`
	Risk                     string             `json:"risk"`
	Claims                   []DHCPAddressClaim `json:"claims"`
	Conflicts                []DHCPConflict     `json:"conflicts"`
	Ready                    bool               `json:"ready"`
	Error                    string             `json:"error,omitempty"`
}

type DHCPAddressPlan struct {
	StateDigest string               `json:"stateDigest"`
	Servers     []DHCPServerCapacity `json:"servers"`
	Ready       bool                 `json:"ready"`
}

type DHCPExpansionAlternative struct {
	Strategy   string   `json:"strategy"`
	Executable bool     `json:"executable"`
	Capacity   int      `json:"capacity"`
	Impact     []string `json:"impact"`
}

type DHCPExpansionOperation struct {
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Body     map[string]string `json:"body"`
	Rollback map[string]string `json:"rollback"`
	Summary  string            `json:"summary"`
}

type DHCPExpansionPlan struct {
	ServerName           string                     `json:"serverName"`
	PoolID               string                     `json:"poolId,omitempty"`
	PoolName             string                     `json:"poolName,omitempty"`
	Network              string                     `json:"network,omitempty"`
	CurrentRanges        string                     `json:"currentRanges,omitempty"`
	ProposedRanges       string                     `json:"proposedRanges,omitempty"`
	RequestedCapacity    int                        `json:"requestedCapacity"`
	SuggestedRanges      []DHCPRange                `json:"suggestedRanges"`
	Before               DHCPServerCapacity         `json:"before"`
	After                *DHCPServerCapacity        `json:"after,omitempty"`
	Alternatives         []DHCPExpansionAlternative `json:"alternatives"`
	StateDigest          string                     `json:"stateDigest"`
	Operation            *DHCPExpansionOperation    `json:"operation,omitempty"`
	Warnings             []string                   `json:"warnings"`
	Executable           bool                       `json:"executable"`
	RequiresConfirmation bool                       `json:"requiresConfirmation"`
}

type DHCPExpansionRequest struct {
	ServerName        string `json:"serverName"`
	ProposedRanges    string `json:"proposedRanges,omitempty"`
	RequestedCapacity int    `json:"requestedCapacity"`
}

func PlanDHCPAddresses(state BindingState) (DHCPAddressPlan, error) {
	if err := validateDHCPPlanningBounds(state); err != nil {
		return DHCPAddressPlan{}, err
	}
	plan := DHCPAddressPlan{
		StateDigest: BindingStateDigest(state),
		Servers:     make([]DHCPServerCapacity, 0, len(state.DHCPServers)),
		Ready:       len(state.DHCPServers) > 0,
	}
	servers := append([]DHCPServer(nil), state.DHCPServers...)
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	for _, server := range servers {
		analysis, err := analyzeDHCPServer(server, state)
		if err != nil {
			analysis = DHCPServerCapacity{
				ServerName: server.Name, Interface: server.Interface,
				Running: server.Running == "true" && server.Disabled != "true",
				Ready:   false, Error: err.Error(), Risk: "unknown",
				Claims: []DHCPAddressClaim{}, Conflicts: []DHCPConflict{}, Ranges: []DHCPRange{},
			}
		} else if !analysis.Ready {
			plan.Ready = false
		}
		if err != nil {
			plan.Ready = false
		}
		plan.Servers = append(plan.Servers, analysis)
	}
	return plan, nil
}

func analyzeDHCPServer(server DHCPServer, state BindingState) (DHCPServerCapacity, error) {
	analysis := DHCPServerCapacity{
		ServerName: server.Name, Interface: server.Interface,
		Running: server.Running == "true" && server.Disabled != "true",
		Ready:   true, Claims: []DHCPAddressClaim{}, Conflicts: []DHCPConflict{}, Ranges: []DHCPRange{},
	}
	if strings.TrimSpace(server.Name) == "" || strings.TrimSpace(server.Interface) == "" || server.Disabled == "true" {
		return analysis, fmt.Errorf("%w: DHCP server is disabled or incomplete", ErrDHCPPlan)
	}
	ranges, names, err := serverPoolRanges(server, state.Pools)
	if err != nil {
		return analysis, fmt.Errorf("%w: %v", ErrDHCPPlan, err)
	}
	analysis.PoolNames = names
	prefix, network, err := selectDHCPNetwork(server, ranges, state)
	if err != nil {
		return analysis, err
	}
	analysis.Network, analysis.Gateway = prefix.String(), network.Gateway
	firstHost, lastHost, hostCount, err := usableHostRange(prefix)
	if err != nil {
		return analysis, fmt.Errorf("%w: %v", ErrDHCPPlan, err)
	}
	merged, overlaps, err := normalizePlanningRanges(ranges, firstHost, lastHost)
	if err != nil {
		return analysis, err
	}
	for _, candidate := range ranges {
		analysis.Ranges = append(analysis.Ranges, renderDHCPRange(candidate))
	}
	if overlaps {
		analysis.Conflicts = append(analysis.Conflicts, DHCPConflict{Kind: "overlapping_pool_ranges", Detail: "地址池范围互相重叠，容量已按去重地址计算"})
	}
	analysis.ConfiguredCapacity = intervalCapacity(merged)
	poolContains := func(number uint32) bool { return rangeContains(merged, number) }
	claims := make(map[uint32][]DHCPAddressClaim)
	leaseMACs := make(map[uint32]map[string]struct{})
	dynamic := make(map[uint32]struct{})
	excluded := make(map[uint32]struct{})
	reservedOutside := make(map[uint32]struct{})
	infraOutside := make(map[uint32]struct{})
	addClaim := func(number uint32, claim DHCPAddressClaim, reserve bool) {
		claims[number] = append(claims[number], claim)
		if poolContains(number) {
			if reserve {
				excluded[number] = struct{}{}
			}
		} else if number >= firstHost && number <= lastHost {
			reservedOutside[number] = struct{}{}
		}
	}
	if gateway, parseErr := ipv4Number(network.Gateway); parseErr == nil && gateway >= firstHost && gateway <= lastHost {
		addClaim(gateway, DHCPAddressClaim{Address: network.Gateway, Kind: "gateway", Detail: "DHCP network gateway"}, true)
		if !poolContains(gateway) {
			infraOutside[gateway] = struct{}{}
		}
	}
	for _, item := range state.Addresses {
		if item.Disabled == "true" {
			continue
		}
		addressPrefix, parseErr := netip.ParsePrefix(item.Address)
		if parseErr != nil || !prefix.Contains(addressPrefix.Addr()) {
			continue
		}
		address := addressPrefix.Addr()
		number, _ := ipv4Number(address.String())
		if number < firstHost || number > lastHost {
			continue
		}
		addClaim(number, DHCPAddressClaim{Address: address.String(), Kind: "router_address", Detail: "RouterOS address on " + item.Interface}, true)
		if !poolContains(number) {
			infraOutside[number] = struct{}{}
		}
	}
	for _, lease := range state.Leases {
		number, parseErr := ipv4Number(lease.Address)
		if parseErr != nil || number < firstHost || number > lastHost || lease.Disabled == "true" {
			continue
		}
		mac := normalizeMAC(lease.MACAddress)
		if leaseMACs[number] == nil {
			leaseMACs[number] = make(map[string]struct{})
		}
		leaseMACs[number][mac] = struct{}{}
		if lease.Dynamic == "true" && lease.Server == server.Name && poolContains(number) {
			dynamic[number] = struct{}{}
			addClaim(number, DHCPAddressClaim{Address: lease.Address, Kind: "dynamic_lease", Detail: mac}, false)
		} else {
			kind := "static_lease"
			if lease.Dynamic == "true" {
				kind = "other_server_lease"
			}
			addClaim(number, DHCPAddressClaim{Address: lease.Address, Kind: kind, Detail: lease.Server + " " + mac}, true)
		}
	}
	for number, macs := range leaseMACs {
		if len(macs) > 1 {
			analysis.Conflicts = append(analysis.Conflicts, DHCPConflict{Address: ipv4String(number), Kind: "duplicate_lease_address", Detail: "同一地址关联多个 MAC"})
			if poolContains(number) {
				excluded[number] = struct{}{}
				delete(dynamic, number)
			}
		}
	}
	for _, arp := range state.ARP {
		number, parseErr := ipv4Number(arp.Address)
		if parseErr != nil || number < firstHost || number > lastHost || arp.Complete != "true" {
			continue
		}
		mac := normalizeMAC(arp.MACAddress)
		if known := leaseMACs[number]; len(known) == 1 {
			if _, same := known[mac]; same {
				continue
			}
		}
		addClaim(number, DHCPAddressClaim{Address: arp.Address, Kind: "arp_observation", Detail: arp.Interface + " " + mac}, true)
		if poolContains(number) {
			delete(dynamic, number)
			analysis.Conflicts = append(analysis.Conflicts, DHCPConflict{Address: arp.Address, Kind: "arp_pool_conflict", Detail: "ARP 观察与 DHCP 租约不一致或没有对应租约"})
		}
	}
	for number := range excluded {
		analysis.Conflicts = append(analysis.Conflicts, DHCPConflict{Address: ipv4String(number), Kind: "reserved_address_in_pool", Detail: "静态或基础设施地址位于动态池内"})
	}
	keys := make([]uint32, 0, len(claims))
	for number := range claims {
		keys = append(keys, number)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, number := range keys {
		analysis.Claims = append(analysis.Claims, claims[number]...)
	}
	sort.Slice(analysis.Conflicts, func(i, j int) bool {
		return analysis.Conflicts[i].Address+analysis.Conflicts[i].Kind < analysis.Conflicts[j].Address+analysis.Conflicts[j].Kind
	})
	analysis.ExcludedWithinPool = len(excluded)
	analysis.DynamicCapacity = max(0, analysis.ConfiguredCapacity-analysis.ExcludedWithinPool)
	analysis.DynamicOccupied = min(len(dynamic), analysis.DynamicCapacity)
	analysis.Remaining = analysis.DynamicCapacity - analysis.DynamicOccupied
	if analysis.DynamicCapacity > 0 {
		analysis.UtilizationPercent = float64(analysis.DynamicOccupied) * 100 / float64(analysis.DynamicCapacity)
	}
	analysis.ReservationSpaceCapacity = max(0, hostCount-analysis.ConfiguredCapacity-len(infraOutside))
	for number := range infraOutside {
		delete(reservedOutside, number)
	}
	analysis.ReservationUsed = min(len(reservedOutside), analysis.ReservationSpaceCapacity)
	analysis.ReservationRemaining = analysis.ReservationSpaceCapacity - analysis.ReservationUsed
	analysis.Risk = dhcpRisk(analysis.DynamicCapacity, analysis.DynamicOccupied, analysis.Remaining)
	return analysis, nil
}

func validateDHCPPlanningBounds(state BindingState) error {
	resources := len(state.Leases) + len(state.Pools) + len(state.Networks) + len(state.Addresses) + len(state.ARP) + len(state.DHCPServers)
	if resources > maxDHCPPlanningResources || len(state.DHCPServers) > maxDHCPPlanningServers || len(state.Pools) > maxDHCPPlanningRanges {
		return fmt.Errorf("%w: RouterOS state exceeds the planning boundary", ErrDHCPPlan)
	}
	return nil
}

func serverPoolRanges(server DHCPServer, pools []IPPool) ([]ipRange, []string, error) {
	ranges, err := poolRanges(server, pools)
	if err != nil {
		return nil, nil, err
	}
	if len(ranges) > maxDHCPPlanningRanges {
		return nil, nil, errors.New("address pool has too many ranges")
	}
	if server.AddressPool == "static-only" || server.AddressPool == "none" || server.AddressPool == "" {
		return ranges, []string{}, nil
	}
	byName := make(map[string]IPPool, len(pools))
	for _, pool := range pools {
		if _, duplicate := byName[pool.Name]; duplicate {
			return nil, nil, errors.New("address pool name is ambiguous")
		}
		byName[pool.Name] = pool
	}
	name := server.AddressPool
	seen := make(map[string]struct{})
	var names []string
	for name != "" && name != "none" && name != "static-only" {
		if _, duplicate := seen[name]; duplicate {
			return nil, nil, errors.New("address pool chain contains a cycle")
		}
		seen[name] = struct{}{}
		pool, found := byName[name]
		if !found {
			return nil, nil, fmt.Errorf("address pool %q is missing", name)
		}
		names = append(names, name)
		name = pool.NextPool
	}
	return ranges, names, nil
}

func selectDHCPNetwork(server DHCPServer, ranges []ipRange, state BindingState) (netip.Prefix, DHCPNetwork, error) {
	type candidate struct {
		prefix  netip.Prefix
		network DHCPNetwork
	}
	var candidates []candidate
	for _, network := range state.Networks {
		prefix, err := netip.ParsePrefix(network.Address)
		if err != nil || !prefix.Addr().Is4() {
			continue
		}
		prefix = prefix.Masked()
		interfaceMatch := false
		for _, address := range state.Addresses {
			if address.Interface != server.Interface || address.Disabled == "true" {
				continue
			}
			addressPrefix, parseErr := netip.ParsePrefix(address.Address)
			if parseErr == nil && addressPrefix.Masked() == prefix {
				interfaceMatch = true
				break
			}
		}
		if !interfaceMatch {
			continue
		}
		contains := true
		for _, addressRange := range ranges {
			if !prefix.Contains(netip.AddrFrom4(numberBytes(addressRange.first))) || !prefix.Contains(netip.AddrFrom4(numberBytes(addressRange.last))) {
				contains = false
				break
			}
		}
		if contains {
			candidates = append(candidates, candidate{prefix: prefix, network: network})
		}
	}
	if len(candidates) != 1 {
		return netip.Prefix{}, DHCPNetwork{}, fmt.Errorf("%w: DHCP server network is missing or ambiguous", ErrDHCPPlan)
	}
	return candidates[0].prefix, candidates[0].network, nil
}

func usableHostRange(prefix netip.Prefix) (uint32, uint32, int, error) {
	if !prefix.IsValid() || !prefix.Addr().Is4() || prefix.Bits() > 30 {
		return 0, 0, 0, errors.New("network has no conventional DHCP host range")
	}
	first, _ := ipv4Number(prefix.Masked().Addr().String())
	last, _ := ipv4Number(prefixLast(prefix).String())
	count := uint64(last) - uint64(first) - 1
	if count > maxDHCPPlanningHosts {
		return 0, 0, 0, errors.New("network exceeds DHCP planning limit")
	}
	return first + 1, last - 1, int(count), nil
}

func parsePlanningRanges(value string) ([]ipRange, error) {
	if strings.TrimSpace(value) == "" || len(value) > 8192 {
		return nil, fmt.Errorf("%w: address ranges are required", ErrDHCPPlan)
	}
	parts := strings.Split(value, ",")
	if len(parts) > maxDHCPPlanningRanges {
		return nil, fmt.Errorf("%w: too many address ranges", ErrDHCPPlan)
	}
	ranges := make([]ipRange, 0, len(parts))
	for _, raw := range parts {
		bounds := strings.Split(strings.TrimSpace(raw), "-")
		if len(bounds) < 1 || len(bounds) > 2 {
			return nil, fmt.Errorf("%w: invalid range %q", ErrDHCPPlan, raw)
		}
		first, err := ipv4Number(bounds[0])
		if err != nil {
			return nil, fmt.Errorf("%w: invalid range %q", ErrDHCPPlan, raw)
		}
		last := first
		if len(bounds) == 2 {
			last, err = ipv4Number(bounds[1])
			if err != nil || last < first {
				return nil, fmt.Errorf("%w: invalid range %q", ErrDHCPPlan, raw)
			}
		}
		ranges = append(ranges, ipRange{first: first, last: last})
	}
	return ranges, nil
}

func normalizePlanningRanges(ranges []ipRange, firstHost, lastHost uint32) ([]ipRange, bool, error) {
	if len(ranges) > maxDHCPPlanningRanges {
		return nil, false, fmt.Errorf("%w: too many address ranges", ErrDHCPPlan)
	}
	copyRanges := append([]ipRange(nil), ranges...)
	sort.Slice(copyRanges, func(i, j int) bool {
		return copyRanges[i].first < copyRanges[j].first || copyRanges[i].first == copyRanges[j].first && copyRanges[i].last < copyRanges[j].last
	})
	merged := make([]ipRange, 0, len(copyRanges))
	overlaps := false
	for _, candidate := range copyRanges {
		if candidate.first < firstHost || candidate.last > lastHost || candidate.last < candidate.first {
			return nil, false, fmt.Errorf("%w: pool range is outside the usable network host range", ErrDHCPPlan)
		}
		if len(merged) == 0 || uint64(candidate.first) > uint64(merged[len(merged)-1].last)+1 {
			merged = append(merged, candidate)
			continue
		}
		if candidate.first <= merged[len(merged)-1].last {
			overlaps = true
		}
		if candidate.last > merged[len(merged)-1].last {
			merged[len(merged)-1].last = candidate.last
		}
	}
	return merged, overlaps, nil
}

func intervalCapacity(ranges []ipRange) int {
	capacity := uint64(0)
	for _, candidate := range ranges {
		capacity += uint64(candidate.last) - uint64(candidate.first) + 1
	}
	return min(int(capacity), maxDHCPPlanningHosts)
}

func rangeContains(ranges []ipRange, number uint32) bool {
	index := sort.Search(len(ranges), func(index int) bool { return ranges[index].last >= number })
	return index < len(ranges) && ranges[index].first <= number
}

func rangesContainAll(outer, inner []ipRange) bool {
	for _, candidate := range inner {
		if !rangeContains(outer, candidate.first) || !rangeContains(outer, candidate.last) {
			return false
		}
	}
	return true
}

func renderDHCPRange(candidate ipRange) DHCPRange {
	return DHCPRange{Start: ipv4String(candidate.first), End: ipv4String(candidate.last), Capacity: int(uint64(candidate.last)-uint64(candidate.first)) + 1}
}

func formatPlanningRanges(ranges []ipRange) string {
	parts := make([]string, 0, len(ranges))
	for _, candidate := range ranges {
		if candidate.first == candidate.last {
			parts = append(parts, ipv4String(candidate.first))
		} else {
			parts = append(parts, ipv4String(candidate.first)+"-"+ipv4String(candidate.last))
		}
	}
	return strings.Join(parts, ",")
}

func ipv4String(number uint32) string {
	return netip.AddrFrom4(numberBytes(number)).String()
}

func numberBytes(number uint32) [4]byte {
	var output [4]byte
	binary.BigEndian.PutUint32(output[:], number)
	return output
}

func dhcpRisk(capacity, occupied, remaining int) string {
	if capacity == 0 || remaining == 0 {
		return "exhausted"
	}
	ratio := float64(occupied) / float64(capacity)
	if remaining <= 10 || ratio >= 0.9 {
		return "critical"
	}
	if remaining <= 20 || ratio >= 0.75 {
		return "warning"
	}
	return "normal"
}

func PreviewDHCPExpansion(state BindingState, request DHCPExpansionRequest) (DHCPExpansionPlan, error) {
	if err := validateDHCPPlanningBounds(state); err != nil {
		return DHCPExpansionPlan{}, err
	}
	request.ServerName = strings.TrimSpace(request.ServerName)
	if request.ServerName == "" || request.RequestedCapacity < 1 || request.RequestedCapacity > maxDHCPPlanningHosts {
		return DHCPExpansionPlan{}, fmt.Errorf("%w: server name and bounded requested capacity are required", ErrDHCPPlan)
	}
	server, err := uniqueDHCPServer(state.DHCPServers, request.ServerName)
	if err != nil {
		return DHCPExpansionPlan{}, err
	}
	before, err := analyzeDHCPServer(server, state)
	if err != nil {
		return DHCPExpansionPlan{}, err
	}
	plan := DHCPExpansionPlan{
		ServerName: server.Name, Network: before.Network, RequestedCapacity: request.RequestedCapacity,
		Before: before, StateDigest: BindingStateDigest(state),
		Warnings: []string{"扩容只允许修改带 FoxOS 所有权注释的单一地址池，不修改 DHCP network、网关、DNS、VLAN 或防火墙"},
	}
	plan.SuggestedRanges = suggestExpansionRanges(before, request.RequestedCapacity)
	prefix, _ := netip.ParsePrefix(before.Network)
	_, _, usable, _ := usableHostRange(prefix)
	if request.RequestedCapacity > usable || (prefix.Bits() >= 24 && request.RequestedCapacity > 254) {
		plan.Alternatives = capacityAlternatives(request.RequestedCapacity)
		plan.Warnings = append(plan.Warnings, "目标容量超过当前网段上限，FoxOS 不会自动改掩码或创建 VLAN")
	}
	if strings.TrimSpace(request.ProposedRanges) == "" {
		return plan, nil
	}
	if request.RequestedCapacity > usable {
		return plan, nil
	}
	pool, err := executableDHCPPool(server, state.Pools)
	if err != nil {
		plan.Warnings = append(plan.Warnings, err.Error())
		return plan, nil
	}
	firstHost, lastHost, _, _ := usableHostRange(prefix)
	proposed, err := parsePlanningRanges(request.ProposedRanges)
	if err != nil {
		return DHCPExpansionPlan{}, err
	}
	proposed, _, err = normalizePlanningRanges(proposed, firstHost, lastHost)
	if err != nil {
		return DHCPExpansionPlan{}, err
	}
	current, err := parsePlanningRanges(pool.Ranges)
	if err != nil {
		return DHCPExpansionPlan{}, err
	}
	current, _, err = normalizePlanningRanges(current, firstHost, lastHost)
	if err != nil || !rangesContainAll(proposed, current) {
		return DHCPExpansionPlan{}, fmt.Errorf("%w: proposed ranges must preserve every current pool address", ErrDHCPPlan)
	}
	canonical := formatPlanningRanges(proposed)
	clone := cloneBindingState(state)
	for index := range clone.Pools {
		if clone.Pools[index].ID == pool.ID {
			clone.Pools[index].Ranges = canonical
		}
	}
	after, err := analyzeDHCPServer(server, clone)
	if err != nil {
		return DHCPExpansionPlan{}, err
	}
	if after.DynamicCapacity < request.RequestedCapacity || after.DynamicCapacity <= before.DynamicCapacity || hasBlockingDHCPConflict(after.Conflicts) {
		return DHCPExpansionPlan{}, fmt.Errorf("%w: proposed ranges do not provide the requested conflict-free expansion", ErrDHCPPlan)
	}
	plan.PoolID, plan.PoolName = pool.ID, pool.Name
	plan.CurrentRanges, plan.ProposedRanges = pool.Ranges, canonical
	plan.After = &after
	plan.Operation = &DHCPExpansionOperation{
		Method: http.MethodPatch, Path: "/rest/ip/pool/" + pool.ID,
		Body: map[string]string{"ranges": canonical}, Rollback: map[string]string{"ranges": pool.Ranges},
		Summary: "扩展 FoxOS 自有 DHCP 地址池",
	}
	plan.Executable, plan.RequiresConfirmation = true, true
	return plan, nil
}

func uniqueDHCPServer(servers []DHCPServer, name string) (DHCPServer, error) {
	var found *DHCPServer
	for index := range servers {
		if servers[index].Name != name {
			continue
		}
		if found != nil {
			return DHCPServer{}, fmt.Errorf("%w: DHCP server name is ambiguous", ErrDHCPPlan)
		}
		copy := servers[index]
		found = &copy
	}
	if found == nil {
		return DHCPServer{}, fmt.Errorf("%w: DHCP server is missing", ErrDHCPPlan)
	}
	return *found, nil
}

func executableDHCPPool(server DHCPServer, pools []IPPool) (IPPool, error) {
	if server.AddressPool == "" || server.AddressPool == "none" || server.AddressPool == "static-only" {
		return IPPool{}, errors.New("当前 DHCP server 没有可扩展的动态池")
	}
	var found *IPPool
	for index := range pools {
		if pools[index].Name != server.AddressPool {
			continue
		}
		if found != nil {
			return IPPool{}, errors.New("地址池名称不唯一")
		}
		copy := pools[index]
		found = &copy
	}
	owner := "foxos:dhcp-pool:" + server.Name
	if found == nil || !safeRouterOSID(found.ID) || (found.NextPool != "" && found.NextPool != "none") || found.Comment != owner {
		return IPPool{}, errors.New("地址池不是带精确所有权标记的 FoxOS 单池，扩容仅提供预览")
	}
	return *found, nil
}

func cloneBindingState(state BindingState) BindingState {
	state.Leases = append([]Lease(nil), state.Leases...)
	state.Pools = append([]IPPool(nil), state.Pools...)
	state.Networks = append([]DHCPNetwork(nil), state.Networks...)
	state.Addresses = append([]IPAddress(nil), state.Addresses...)
	state.ARP = append([]ARP(nil), state.ARP...)
	state.DHCPServers = append([]DHCPServer(nil), state.DHCPServers...)
	return state
}

func hasBlockingDHCPConflict(conflicts []DHCPConflict) bool {
	for _, conflict := range conflicts {
		if conflict.Kind != "overlapping_pool_ranges" {
			return true
		}
	}
	return false
}

func suggestExpansionRanges(before DHCPServerCapacity, requested int) []DHCPRange {
	if requested <= before.DynamicCapacity {
		return []DHCPRange{}
	}
	prefix, err := netip.ParsePrefix(before.Network)
	if err != nil {
		return []DHCPRange{}
	}
	first, last, _, err := usableHostRange(prefix)
	if err != nil {
		return []DHCPRange{}
	}
	blocked := make([]ipRange, 0, len(before.Ranges)+len(before.Claims))
	for _, item := range before.Ranges {
		start, startErr := ipv4Number(item.Start)
		end, endErr := ipv4Number(item.End)
		if startErr == nil && endErr == nil {
			blocked = append(blocked, ipRange{first: start, last: end})
		}
	}
	for _, claim := range before.Claims {
		if number, parseErr := ipv4Number(claim.Address); parseErr == nil && number >= first && number <= last {
			blocked = append(blocked, ipRange{first: number, last: number})
		}
	}
	blocked, _, _ = normalizePlanningRanges(blocked, first, last)
	needed := requested - before.DynamicCapacity
	var suggestions []DHCPRange
	cursor := uint64(first)
	for _, item := range blocked {
		if cursor < uint64(item.first) && needed > 0 {
			end := uint64(item.first) - 1
			available := int(end-cursor) + 1
			if available > needed {
				end = cursor + uint64(needed) - 1
				available = needed
			}
			suggestions = append(suggestions, renderDHCPRange(ipRange{first: uint32(cursor), last: uint32(end)}))
			needed -= available
		}
		if uint64(item.last)+1 > cursor {
			cursor = uint64(item.last) + 1
		}
	}
	if cursor <= uint64(last) && needed > 0 {
		end := uint64(last)
		available := int(end-cursor) + 1
		if available > needed {
			end = cursor + uint64(needed) - 1
		}
		suggestions = append(suggestions, renderDHCPRange(ipRange{first: uint32(cursor), last: uint32(end)}))
	}
	return suggestions
}

func capacityAlternatives(requested int) []DHCPExpansionAlternative {
	if requested <= 510 {
		return []DHCPExpansionAlternative{
			{Strategy: "expand-to-/23", Capacity: 510, Executable: false, Impact: []string{"确认相邻 /24 没有重叠路由", "同时修改接口地址前缀和 DHCP network", "复核客户端掩码、静态地址、防火墙与广播域", "FoxOS 不自动执行该多资源变更"}},
			{Strategy: "split-vlan", Capacity: requested, Executable: false, Impact: []string{"新增 VLAN、网关地址、地址池、DHCP network 和 DHCP server", "配置交换机/AP trunk 与 SSID 或端口映射", "新增跨 VLAN 防火墙策略", "FoxOS 不自动接管未知 VLAN 或防火墙资源"}},
		}
	}
	return []DHCPExpansionAlternative{
		{Strategy: "split-vlan", Capacity: requested, Executable: false, Impact: []string{"按设备信任边界拆分多个 VLAN 和 DHCP server", "规划交换机/AP trunk、路由和跨 VLAN 防火墙", "FoxOS 不自动接管未知 VLAN、路由或防火墙资源"}},
	}
}

type DHCPExpansionWriter interface {
	BindingState(context.Context) (BindingState, error)
	WriteDHCPPoolRanges(context.Context, string, string, string) error
}

type DHCPExpansionExecutor struct {
	Writer DHCPExpansionWriter
}

var dhcpExpansionMu sync.Mutex

func (e DHCPExpansionExecutor) Execute(ctx context.Context, plan DHCPExpansionPlan) error {
	if e.Writer == nil || !plan.Executable || !plan.RequiresConfirmation || plan.Operation == nil ||
		plan.Operation.Method != http.MethodPatch || !safeRouterOSID(plan.PoolID) || plan.ProposedRanges == "" {
		return ErrUnsafeOperation
	}
	dhcpExpansionMu.Lock()
	defer dhcpExpansionMu.Unlock()
	state, err := e.Writer.BindingState(ctx)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(BindingStateDigest(state)), []byte(plan.StateDigest)) != 1 {
		return ErrDHCPExpansionStale
	}
	pool, err := executableDHCPPoolFromPlan(state.Pools, plan)
	if err != nil || pool.Ranges != plan.CurrentRanges {
		return ErrDHCPExpansionStale
	}
	if err := e.Writer.WriteDHCPPoolRanges(ctx, pool.ID, pool.Comment, plan.ProposedRanges); err != nil {
		return err
	}
	state, readErr := e.Writer.BindingState(ctx)
	if readErr == nil {
		pool, err = executableDHCPPoolFromPlan(state.Pools, plan)
		if err == nil && pool.Ranges == plan.ProposedRanges {
			return nil
		}
	}
	if readErr != nil {
		return readErr
	}
	if err != nil || pool.Ranges != plan.ProposedRanges {
		return fmt.Errorf("%w: pool changed before rollback", ErrCompensationStateChanged)
	}
	if rollbackErr := e.Writer.WriteDHCPPoolRanges(ctx, plan.PoolID, "foxos:dhcp-pool:"+plan.ServerName, plan.CurrentRanges); rollbackErr != nil {
		return fmt.Errorf("%w: rollback: %v", ErrCompensationFailed, rollbackErr)
	}
	return fmt.Errorf("%w: expansion readback failed", ErrDHCPExpansionRollback)
}

func executableDHCPPoolFromPlan(pools []IPPool, plan DHCPExpansionPlan) (IPPool, error) {
	for _, pool := range pools {
		if pool.ID == plan.PoolID && pool.Name == plan.PoolName && pool.Comment == "foxos:dhcp-pool:"+plan.ServerName {
			return pool, nil
		}
	}
	return IPPool{}, ErrDHCPExpansionStale
}

func (c *Client) WriteDHCPPoolRanges(ctx context.Context, id, owner, ranges string) error {
	if !safeRouterOSID(id) || !strings.HasPrefix(owner, "foxos:dhcp-pool:") {
		return ErrUnsafeOperation
	}
	if _, err := parsePlanningRanges(ranges); err != nil {
		return err
	}
	pools, err := c.IPPools(ctx)
	if err != nil {
		return err
	}
	for _, pool := range pools {
		if pool.ID == id && pool.Comment == owner {
			return c.writeBindingOperation(ctx, http.MethodPatch, "/rest/ip/pool/"+id, map[string]string{"ranges": ranges})
		}
	}
	return ErrDHCPExpansionStale
}
