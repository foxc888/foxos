package routeros

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/site"
)

var (
	ErrEgressPlan         = errors.New("invalid RouterOS egress plan")
	ErrEgressPlanStale    = errors.New("RouterOS egress plan is stale")
	ErrEgressPrerequisite = errors.New("RouterOS egress prerequisite is not satisfied")
	ErrEgressRolledBack   = errors.New("RouterOS egress changes were rolled back")
)

var egressExecutionMu sync.Mutex

const (
	egressMangleChain   = "foxos-prerouting"
	egressFilterChain   = "foxos-forward"
	mihomoTable         = "foxos-mihomo"
	managementList      = "foxos-management-plane"
	MaxEgressOperations = 32
	maxEgressResources  = 4096
)

// EgressState is a read-only snapshot of the RouterOS resources used to build
// an egress plan. Maps are deliberately string-valued because RouterOS REST
// serializes even booleans and numbers as strings.
type EgressState struct {
	MangleRules        []map[string]string `json:"mangleRules"`
	FilterRules        []map[string]string `json:"filterRules"`
	AddressLists       []map[string]string `json:"addressLists"`
	Routes             []map[string]string `json:"routes"`
	RoutingTables      []map[string]string `json:"routingTables"`
	L2TPClients        []map[string]string `json:"l2tpClients"`
	ProtectedAddresses []string            `json:"protectedAddresses,omitempty"`
	MihomoAddress      string              `json:"mihomoAddress,omitempty"`
}

type EgressOperation struct {
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	Body         map[string]string `json:"body,omitempty"`
	Summary      string            `json:"summary"`
	OwnedComment string            `json:"ownedComment"`
	Rollback     *EgressOperation  `json:"rollback,omitempty"`
}

type EgressPlan struct {
	PolicyID             string               `json:"policyId"`
	ProtectedAddresses   []string             `json:"protectedAddresses"`
	StaticIP             string               `json:"staticIp"`
	Egress               domain.EgressType    `json:"egress"`
	TargetID             string               `json:"targetId,omitempty"`
	Policy               domain.DevicePolicy  `json:"policy"`
	PreviousPolicy       *domain.DevicePolicy `json:"previousPolicy,omitempty"`
	StateDigest          string               `json:"stateDigest"`
	Operations           []EgressOperation    `json:"operations"`
	Warnings             []string             `json:"warnings"`
	RequiresConfirmation bool                 `json:"requiresConfirmation"`
}

type EgressWriter interface {
	ApplyEgress(context.Context, EgressOperation) error
	VerifyEgress(context.Context, EgressPlan) error
}

type EgressCompensator interface {
	CompensateEgress(context.Context, []EgressOperation) error
}

type EgressStateReader interface {
	EgressState(context.Context) (EgressState, error)
}

type EgressExecutor struct {
	Writer EgressWriter
}

func (e EgressExecutor) Execute(ctx context.Context, plan EgressPlan) error {
	if e.Writer == nil || !plan.RequiresConfirmation || len(plan.Operations) == 0 || len(plan.Operations) > MaxEgressOperations || !validEgressDigest(plan.StateDigest) {
		return ErrUnsafeOperation
	}
	for _, operation := range plan.Operations {
		if err := ValidateEgressOperation(operation, plan.ProtectedAddresses); err != nil {
			return err
		}
	}
	egressExecutionMu.Lock()
	defer egressExecutionMu.Unlock()
	reader, ok := e.Writer.(EgressStateReader)
	if !ok {
		return fmt.Errorf("%w: state reader is required", ErrUnsafeOperation)
	}
	state, err := reader.EgressState(ctx)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(plan.StateDigest), []byte(EgressStateDigest(state))) != 1 {
		return ErrEgressPlanStale
	}
	applied := make([]EgressOperation, 0, len(plan.Operations))
	for _, operation := range plan.Operations {
		if err := e.Writer.ApplyEgress(ctx, operation); err != nil {
			if len(applied) == 0 {
				return err
			}
			if compensator, ok := e.Writer.(EgressCompensator); ok {
				if compensationErr := compensator.CompensateEgress(ctx, applied); compensationErr != nil {
					return fmt.Errorf("%w: %v", ErrCompensationFailed, compensationErr)
				}
				return fmt.Errorf("%w: %v", ErrEgressRolledBack, err)
			}
			return err
		}
		applied = append(applied, operation)
	}
	if err := e.Writer.VerifyEgress(ctx, plan); err != nil {
		if compensator, ok := e.Writer.(EgressCompensator); ok {
			if compensationErr := compensator.CompensateEgress(ctx, applied); compensationErr != nil {
				return fmt.Errorf("%w: %v", ErrCompensationFailed, compensationErr)
			}
			return fmt.Errorf("%w: %v", ErrEgressRolledBack, err)
		}
		return err
	}
	return nil
}

func (e EgressExecutor) Compensate(ctx context.Context, plan EgressPlan) error {
	if e.Writer == nil || len(plan.Operations) == 0 || len(plan.Operations) > MaxEgressOperations {
		if len(plan.Operations) == 0 {
			return nil
		}
		return ErrUnsafeOperation
	}
	for _, operation := range plan.Operations {
		if err := ValidateEgressOperation(operation); err != nil {
			return err
		}
	}
	compensator, ok := e.Writer.(EgressCompensator)
	if !ok {
		return fmt.Errorf("%w: egress compensator is required", ErrUnsafeOperation)
	}
	egressExecutionMu.Lock()
	defer egressExecutionMu.Unlock()
	return compensator.CompensateEgress(ctx, plan.Operations)
}

// PlanDeviceEgress builds a plan only from a previously read RouterOS state.
// The state requirement is intentional: creating a rule without proving that
// the FoxOS jump chains and routing prerequisites exist can silently place a
// rule after a user drop/accept rule and give a false success signal.
func PlanDeviceEgress(policy domain.DevicePolicy, state EgressState) (EgressPlan, error) {
	if !egressStateWithinLimit(state) {
		return EgressPlan{}, fmt.Errorf("%w: RouterOS egress state exceeds the planning limit", ErrEgressPlan)
	}
	if isProtectedAddress(policy.StaticIP, state.ProtectedAddresses) {
		return EgressPlan{}, fmt.Errorf("%w: management address is protected", ErrEgressPlan)
	}
	if err := policy.Validate(); err != nil {
		return EgressPlan{}, err
	}
	if !safeResourceName(policy.ID) {
		return EgressPlan{}, fmt.Errorf("%w: invalid policy id", ErrEgressPlan)
	}
	owner := "foxos:egress:" + policy.ID
	plan := EgressPlan{PolicyID: policy.ID, ProtectedAddresses: normalizedProtectedAddresses(state.ProtectedAddresses), StaticIP: policy.StaticIP, Egress: policy.Egress, TargetID: policy.TargetID, Policy: policy, StateDigest: EgressStateDigest(state)}

	// Remove resources left by a previous mode before installing the desired
	// shape. Every deletion carries a complete, validated PUT rollback.
	remove := func(path string, resources []map[string]string) error {
		operations, err := removeOwned(path, resources, owner)
		if err != nil {
			return err
		}
		plan.Operations = append(plan.Operations, operations...)
		return nil
	}

	switch policy.Egress {
	case domain.EgressDirect:
		if err := remove("/rest/ip/firewall/mangle", state.MangleRules); err != nil {
			return EgressPlan{}, err
		}
		if err := remove("/rest/ip/firewall/filter", state.FilterRules); err != nil {
			return EgressPlan{}, err
		}
		if err := remove("/rest/ip/route", state.Routes); err != nil {
			return EgressPlan{}, err
		}
		plan.Warnings = append(plan.Warnings, "direct 只移除 FoxOS 自有出口规则，保留用户默认路由和防火墙")

	case domain.EgressBlocked:
		if err := requireAnchor(state.FilterRules, "forward", "foxos:anchor:forward", egressFilterChain); err != nil {
			return EgressPlan{}, err
		}
		if hasActiveFastTrack(state.FilterRules) {
			return EgressPlan{}, fmt.Errorf("%w: active FastTrack can bypass the FoxOS forward chain", ErrEgressPrerequisite)
		}
		if err := remove("/rest/ip/firewall/mangle", state.MangleRules); err != nil {
			return EgressPlan{}, err
		}
		if err := remove("/rest/ip/route", state.Routes); err != nil {
			return EgressPlan{}, err
		}
		filterBody := map[string]string{"chain": egressFilterChain, "src-address": policy.StaticIP, "action": "reject", "reject-with": "icmp-network-unreachable", "comment": owner, "disabled": "false"}
		op, err := ensureOwned("/rest/ip/firewall/filter", state.FilterRules, filterBody, owner, "拒绝设备转发流量")
		if err != nil {
			return EgressPlan{}, err
		}
		plan.Operations = append(plan.Operations, op...)
		plan.Warnings = append(plan.Warnings, "设备流量将在 FoxOS forward 链中拒绝；已有 FastTrack 连接必须先结束")

	case domain.EgressMihomoNode, domain.EgressProxyChain:
		mihomoAddress := state.MihomoAddress
		if mihomoAddress == "" {
			mihomoAddress = site.Default().MihomoAddress
		}
		return EgressPlan{}, fmt.Errorf("%w: Mihomo transparent ingress, return path, management bypass and exit-IP evidence are not verified; a marked route to %s is not a data plane", ErrEgressPrerequisite, mihomoAddress)

	case domain.EgressL2TP:
		if err := requireL2TPPrerequisites(state, policy); err != nil {
			return EgressPlan{}, err
		}
		if err := remove("/rest/ip/firewall/filter", state.FilterRules); err != nil {
			return EgressPlan{}, err
		}
		if err := remove("/rest/ip/firewall/mangle", state.MangleRules); err != nil {
			return EgressPlan{}, err
		}
		bypass, err := ensureManagementBypass(state.AddressLists, state.ProtectedAddresses)
		if err != nil {
			return EgressPlan{}, err
		}
		plan.Operations = append(plan.Operations, bypass...)
		table := l2tpTable(policy.ID)
		mangleBody := map[string]string{"chain": egressMangleChain, "src-address": policy.StaticIP, "dst-address-list": "!" + managementList, "action": "mark-routing", "new-routing-mark": table, "passthrough": "no", "comment": owner, "disabled": "false"}
		mangle, err := ensureOwned("/rest/ip/firewall/mangle", state.MangleRules, mangleBody, owner, "设备流量导向 L2TP 路由表")
		if err != nil {
			return EgressPlan{}, err
		}
		plan.Operations = append(plan.Operations, mangle...)
		routeBody := map[string]string{"dst-address": "0.0.0.0/0", "gateway": policy.TargetID, "routing-table": table, "distance": "1", "comment": owner, "disabled": "false"}
		route, err := ensureOwned("/rest/ip/route", state.Routes, routeBody, owner, "为设备出口准备 FoxOS L2TP 路由")
		if err != nil {
			return EgressPlan{}, err
		}
		plan.Operations = append(plan.Operations, route...)
		plan.Warnings = append(plan.Warnings, "只写入 FoxOS 自有 L2TP 路由和 mangle 规则，不修改用户默认路由")
	}

	if len(plan.Operations) > MaxEgressOperations {
		return EgressPlan{}, fmt.Errorf("%w: plan exceeds %d operations", ErrEgressPlan, MaxEgressOperations)
	}
	plan.RequiresConfirmation = len(plan.Operations) > 0
	return plan, nil
}

func egressStateWithinLimit(state EgressState) bool {
	count := 0
	for _, size := range []int{len(state.MangleRules), len(state.FilterRules), len(state.AddressLists), len(state.Routes), len(state.RoutingTables), len(state.L2TPClients)} {
		if size > maxEgressResources-count {
			return false
		}
		count += size
	}
	return true
}

func EgressStateDigest(state EgressState) string {
	body, err := json.Marshal(state)
	if err != nil {
		panic("RouterOS egress state contains unsupported data")
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func EqualEgressPlans(left, right EgressPlan) bool {
	leftBody, leftErr := json.Marshal(left)
	rightBody, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil || len(leftBody) != len(rightBody) {
		return false
	}
	return subtle.ConstantTimeCompare(leftBody, rightBody) == 1
}

func validEgressDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (c *Client) PlanDeviceEgress(ctx context.Context, policy domain.DevicePolicy) (EgressPlan, error) {
	state, err := c.EgressState(ctx)
	if err != nil {
		return EgressPlan{}, err
	}
	return PlanDeviceEgress(policy, state)
}

func (c *Client) EgressState(ctx context.Context) (EgressState, error) {
	state := EgressState{ProtectedAddresses: c.site.ProtectedAddresses(), MihomoAddress: c.site.MihomoAddress}
	queries := []struct {
		path string
		out  *[]map[string]string
	}{
		{path: "/rest/ip/firewall/mangle", out: &state.MangleRules},
		{path: "/rest/ip/firewall/filter", out: &state.FilterRules},
		{path: "/rest/ip/firewall/address-list", out: &state.AddressLists},
		{path: "/rest/ip/route", out: &state.Routes},
		{path: "/rest/routing/table", out: &state.RoutingTables},
		{path: "/rest/interface/l2tp-client", out: &state.L2TPClients},
	}
	for _, query := range queries {
		if err := c.get(ctx, query.path, query.out); err != nil {
			return EgressState{}, err
		}
	}
	return state, nil
}

func requireManglePrerequisites(state EgressState, policy domain.DevicePolicy) error {
	if err := requireAnchor(state.MangleRules, "prerouting", "foxos:anchor:mangle", egressMangleChain); err != nil {
		return err
	}
	if hasActiveFastTrack(state.FilterRules) {
		return fmt.Errorf("%w: active FastTrack can bypass policy routing", ErrEgressPrerequisite)
	}
	if !hasFibTable(state.RoutingTables, mihomoTable) {
		return fmt.Errorf("%w: routing table %s with fib=yes is required", ErrEgressPrerequisite, mihomoTable)
	}
	if !hasMihomoGateway(state.Routes, state.MihomoAddress) {
		return fmt.Errorf("%w: active foxos Mihomo transparent gateway route is required", ErrEgressPrerequisite)
	}
	_ = policy
	return nil
}

func requireL2TPPrerequisites(state EgressState, policy domain.DevicePolicy) error {
	if err := requireManglePrerequisitesWithoutMihomo(state); err != nil {
		return err
	}
	table := l2tpTable(policy.ID)
	if !hasFibTable(state.RoutingTables, table) {
		return fmt.Errorf("%w: routing table %s with fib=yes is required", ErrEgressPrerequisite, table)
	}
	for _, item := range state.L2TPClients {
		if item["name"] == policy.TargetID && !disabled(item) && strings.EqualFold(item["running"], "true") {
			return nil
		}
	}
	return fmt.Errorf("%w: running L2TP client %q is required", ErrEgressPrerequisite, policy.TargetID)
}

func requireManglePrerequisitesWithoutMihomo(state EgressState) error {
	if err := requireAnchor(state.MangleRules, "prerouting", "foxos:anchor:mangle", egressMangleChain); err != nil {
		return err
	}
	if hasActiveFastTrack(state.FilterRules) {
		return fmt.Errorf("%w: active FastTrack can bypass policy routing", ErrEgressPrerequisite)
	}
	return nil
}

func requireAnchor(rules []map[string]string, chain, comment, jumpTarget string) error {
	first := -1
	anchor := -1
	for index, rule := range rules {
		if rule["chain"] != chain || disabled(rule) {
			continue
		}
		if first < 0 {
			first = index
		}
		if rule["comment"] == comment && rule["action"] == "jump" && rule["jump-target"] == jumpTarget {
			anchor = index
		}
	}
	if anchor < 0 {
		return fmt.Errorf("%w: active %s jump anchor %q is required", ErrEgressPrerequisite, chain, comment)
	}
	if anchor != first {
		return fmt.Errorf("%w: FoxOS %s jump anchor must be the first active rule", ErrEgressPrerequisite, chain)
	}
	if !safeRouterOSID(rules[anchor][".id"]) {
		return fmt.Errorf("%w: anchor has invalid RouterOS id", ErrEgressPrerequisite)
	}
	return nil
}

func hasActiveFastTrack(rules []map[string]string) bool {
	for _, rule := range rules {
		if !disabled(rule) && rule["chain"] == "forward" && rule["action"] == "fasttrack-connection" {
			return true
		}
	}
	return false
}

func hasFibTable(tables []map[string]string, name string) bool {
	for _, table := range tables {
		if table["name"] == name && !disabled(table) && (strings.EqualFold(table["fib"], "true") || strings.EqualFold(table["fib"], "yes")) {
			return true
		}
	}
	return false
}

func hasMihomoGateway(routes []map[string]string, addresses ...string) bool {
	mihomoAddress := site.Default().MihomoAddress
	if len(addresses) > 0 && net.ParseIP(addresses[0]) != nil {
		mihomoAddress = addresses[0]
	}
	for _, route := range routes {
		if route["dst-address"] == "0.0.0.0/0" && route["routing-table"] == mihomoTable && !disabled(route) && (route["gateway"] == mihomoAddress || route["gateway"] == mihomoAddress+"@main") && route["comment"] == "foxos:prerequisite:mihomo-transparent" {
			return true
		}
	}
	return false
}

func ensureManagementBypass(resources []map[string]string, protected []string) ([]EgressOperation, error) {
	protected = normalizedProtectedAddresses(protected)
	operations := make([]EgressOperation, 0, len(protected))
	for _, ip := range protected {
		owner := "foxos:bypass:" + ip
		body := map[string]string{"list": managementList, "address": ip, "comment": owner, "disabled": "false"}
		items, err := ensureOwned("/rest/ip/firewall/address-list", resources, body, owner, "保护 FoxOS 管理面地址 "+ip)
		if err != nil {
			return nil, err
		}
		operations = append(operations, items...)
	}
	return operations, nil
}

func isProtectedAddress(value string, protected []string) bool {
	for _, address := range normalizedProtectedAddresses(protected) {
		if value == address {
			return true
		}
	}
	return false
}

func normalizedProtectedAddresses(protected []string) []string {
	if len(protected) == 0 {
		return site.Default().ProtectedAddresses()
	}
	result := make([]string, 0, len(protected))
	seen := make(map[string]struct{}, len(protected))
	for _, value := range protected {
		ip := net.ParseIP(strings.TrimSpace(value))
		if ip == nil || ip.To4() == nil {
			continue
		}
		canonical := ip.String()
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	if len(result) == 0 {
		return site.Default().ProtectedAddresses()
	}
	return result
}

func removeOwned(path string, resources []map[string]string, owner string) ([]EgressOperation, error) {
	operations := make([]EgressOperation, 0)
	for _, resource := range resources {
		if resource["comment"] != owner {
			continue
		}
		id := resource[".id"]
		if !safeRouterOSID(id) {
			return nil, fmt.Errorf("%w: owned resource %q has invalid RouterOS id", ErrEgressPlan, owner)
		}
		body, err := managedBody(path, resource)
		if err != nil {
			return nil, err
		}
		rollback := &EgressOperation{Method: http.MethodPut, Path: path, Body: body, Summary: "恢复 FoxOS 自有出口资源", OwnedComment: owner}
		operations = append(operations, EgressOperation{Method: http.MethodDelete, Path: path + "/" + id, Body: map[string]string{"comment": owner}, Summary: "移除旧 FoxOS 出口资源", OwnedComment: owner, Rollback: rollback})
	}
	return operations, nil
}

func ensureOwned(path string, resources []map[string]string, desired map[string]string, owner, summary string) ([]EgressOperation, error) {
	var found map[string]string
	for _, resource := range resources {
		if resource["comment"] != owner {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%w: duplicate FoxOS resource %q", ErrEgressPlan, owner)
		}
		found = resource
	}
	if found == nil {
		return []EgressOperation{{Method: http.MethodPut, Path: path, Body: cloneMap(desired), Summary: summary, OwnedComment: owner}}, nil
	}
	id := found[".id"]
	if !safeRouterOSID(id) {
		return nil, fmt.Errorf("%w: owned resource %q has invalid RouterOS id", ErrEgressPlan, owner)
	}
	if sameManagedFields(path, found, desired) {
		return nil, nil
	}
	previous, err := managedBody(path, found)
	if err != nil {
		return nil, err
	}
	rollback := &EgressOperation{Method: http.MethodPatch, Path: path + "/" + id, Body: previous, Summary: "恢复 FoxOS 自有出口资源", OwnedComment: owner}
	return []EgressOperation{{Method: http.MethodPatch, Path: path + "/" + id, Body: cloneMap(desired), Summary: summary, OwnedComment: owner, Rollback: rollback}}, nil
}

func managedBody(path string, item map[string]string) (map[string]string, error) {
	owner := item["comment"]
	if !strings.HasPrefix(owner, "foxos:") {
		return nil, fmt.Errorf("%w: resource is not FoxOS-owned", ErrEgressPlan)
	}
	var keys []string
	switch path {
	case "/rest/ip/firewall/address-list":
		keys = []string{"list", "address", "comment", "disabled"}
	case "/rest/ip/firewall/mangle":
		keys = []string{"chain", "src-address", "dst-address-list", "action", "new-routing-mark", "passthrough", "comment", "disabled"}
	case "/rest/ip/firewall/filter":
		keys = []string{"chain", "src-address", "action", "reject-with", "comment", "disabled"}
	case "/rest/ip/route":
		keys = []string{"dst-address", "gateway", "routing-table", "distance", "comment", "disabled"}
	default:
		return nil, fmt.Errorf("%w: unsupported resource path", ErrEgressPlan)
	}
	body := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := item[key]; ok && value != "" {
			body[key] = value
		}
	}
	if body["comment"] != owner {
		return nil, fmt.Errorf("%w: owned resource has no stable comment", ErrEgressPlan)
	}
	return body, nil
}

func sameManagedFields(path string, current, desired map[string]string) bool {
	body, err := managedBody(path, current)
	if err != nil {
		return false
	}
	for key, value := range desired {
		if body[key] != value {
			return false
		}
	}
	return true
}

func cloneMap(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func disabled(item map[string]string) bool {
	return strings.EqualFold(item["disabled"], "true") || strings.EqualFold(item["disabled"], "yes")
}

func l2tpTable(policyID string) string { return "foxos-l2tp-" + policyID }

func safeResourceName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == ':') {
			return false
		}
	}
	return true
}

func ValidateEgressOperation(operation EgressOperation, protected ...[]string) error {
	protectedAddresses := normalizedProtectedAddresses(nil)
	if len(protected) > 0 {
		protectedAddresses = normalizedProtectedAddresses(protected[0])
	}
	if operation.Method != http.MethodPut && operation.Method != http.MethodPatch && operation.Method != http.MethodDelete {
		return fmt.Errorf("%w: method", ErrUnsafeOperation)
	}
	base, id, ok := egressPath(operation.Path)
	if !ok || (operation.Method == http.MethodPut && id != "") || (operation.Method != http.MethodPut && id == "") {
		return fmt.Errorf("%w: path", ErrUnsafeOperation)
	}
	if !strings.HasPrefix(operation.OwnedComment, "foxos:") {
		return fmt.Errorf("%w: owner", ErrUnsafeOperation)
	}
	if operation.Method == http.MethodDelete {
		if operation.Body["comment"] != operation.OwnedComment || operation.Rollback == nil {
			return fmt.Errorf("%w: delete ownership", ErrUnsafeOperation)
		}
		return nil
	}
	if operation.Body["comment"] != operation.OwnedComment {
		return fmt.Errorf("%w: owner", ErrUnsafeOperation)
	}
	if operation.Body["place-before"] != "" && !safeRouterOSID(operation.Body["place-before"]) {
		return fmt.Errorf("%w: placement", ErrUnsafeOperation)
	}
	if isProtectedAddress(operation.Body["src-address"], protectedAddresses) {
		return fmt.Errorf("%w: management address", ErrUnsafeOperation)
	}
	if base == "/rest/ip/firewall/address-list" {
		if operation.Body["list"] != managementList || !strings.HasPrefix(operation.OwnedComment, "foxos:bypass:") || operation.Body["address"] != strings.TrimPrefix(operation.OwnedComment, "foxos:bypass:") {
			return fmt.Errorf("%w: management bypass resource", ErrUnsafeOperation)
		}
	} else if isProtectedAddress(operation.Body["address"], protectedAddresses) {
		return fmt.Errorf("%w: management address", ErrUnsafeOperation)
	}
	switch base {
	case "/rest/ip/firewall/mangle":
		if operation.Body["chain"] != egressMangleChain || operation.Body["action"] != "mark-routing" || operation.Body["passthrough"] != "no" || !strings.HasPrefix(operation.Body["new-routing-mark"], "foxos-") {
			return fmt.Errorf("%w: mangle boundary", ErrUnsafeOperation)
		}
	case "/rest/ip/firewall/filter":
		if operation.Body["chain"] != egressFilterChain || operation.Body["action"] != "reject" {
			return fmt.Errorf("%w: filter boundary", ErrUnsafeOperation)
		}
	case "/rest/ip/route":
		if operation.Body["dst-address"] != "0.0.0.0/0" || !strings.HasPrefix(operation.Body["routing-table"], "foxos-l2tp-") || !safeResourceName(operation.Body["gateway"]) {
			return fmt.Errorf("%w: route boundary", ErrUnsafeOperation)
		}
	}
	return nil
}

func egressPath(path string) (string, string, bool) {
	for _, base := range []string{"/rest/ip/firewall/address-list", "/rest/ip/firewall/mangle", "/rest/ip/firewall/filter", "/rest/ip/route"} {
		if path == base {
			return base, "", true
		}
		if strings.HasPrefix(path, base+"/") {
			id := strings.TrimPrefix(path, base+"/")
			if safeRouterOSID(id) {
				return base, id, true
			}
		}
	}
	return "", "", false
}

func allowedEgressPath(path, method string) bool {
	_, id, ok := egressPath(path)
	if !ok {
		return false
	}
	if method == http.MethodGet {
		return id == ""
	}
	if method == http.MethodPut {
		return id == ""
	}
	return id != "" && (method == http.MethodPatch || method == http.MethodDelete)
}
