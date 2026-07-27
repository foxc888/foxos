package routeros

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
)

var ErrPlanConflict = errors.New("RouterOS plan conflict")

type Operation struct {
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	Body         map[string]string `json:"body,omitempty"`
	Summary      string            `json:"summary"`
	OwnedComment string            `json:"ownedComment"`
	Rollback     *Operation        `json:"rollback,omitempty"`
}
type Plan struct {
	PolicyID             string      `json:"policyId"`
	Operations           []Operation `json:"operations"`
	Warnings             []string    `json:"warnings"`
	RequiresConfirmation bool        `json:"requiresConfirmation"`
}

func PlanDeviceBinding(policy domain.DevicePolicy, leases []Lease) (Plan, error) {
	if isManagementAddress(policy.StaticIP) {
		return Plan{}, fmt.Errorf("%w: management address is protected", ErrPlanConflict)
	}
	if err := policy.Validate(); err != nil {
		return Plan{}, err
	}
	mac, err := CanonicalMAC(policy.MACAddress)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: invalid MAC", ErrPlanConflict)
	}
	comment := "foxos:device:" + policy.ID
	var current *Lease
	for index := range leases {
		lease := leases[index]
		leaseMAC := normalizeMAC(lease.MACAddress)
		if lease.Address == policy.StaticIP && leaseMAC != "" && leaseMAC != mac {
			return Plan{}, fmt.Errorf("%w: IP %s is already used by %s", ErrPlanConflict, policy.StaticIP, leaseMAC)
		}
		if leaseMAC == mac {
			if lease.Comment != comment {
				return Plan{}, fmt.Errorf("%w: existing lease is not owned by this FoxOS policy", ErrPlanConflict)
			}
			copy := lease
			current = &copy
		}
	}
	body := map[string]string{"address": policy.StaticIP, "mac-address": mac, "server": policy.DHCPServer, "comment": comment, "disabled": "false"}
	plan := Plan{PolicyID: policy.ID, RequiresConfirmation: true}
	if current == nil {
		plan.Operations = []Operation{{Method: http.MethodPut, Path: "/rest/ip/dhcp-server/lease", Body: body, Summary: "创建 FoxOS 管理的静态 DHCP 租约", OwnedComment: comment}}
		plan.Warnings = []string{"设备当前没有 DHCP 租约；应用后设备需要重新获取地址"}
		return plan, nil
	}
	if current.Address == policy.StaticIP && current.Dynamic != "true" && current.Comment == comment {
		plan.RequiresConfirmation = false
		return plan, nil
	}
	if strings.TrimSpace(current.ID) == "" {
		return Plan{}, fmt.Errorf("%w: existing lease has no RouterOS ID", ErrPlanConflict)
	}
	if !safeRouterOSID(current.ID) {
		return Plan{}, fmt.Errorf("%w: invalid RouterOS lease id", ErrPlanConflict)
	}
	rollbackBody := map[string]string{"address": current.Address, "mac-address": mac, "server": current.Server, "comment": current.Comment, "disabled": "false"}
	rollback := &Operation{Method: http.MethodPatch, Path: "/rest/ip/dhcp-server/lease/" + current.ID, Body: rollbackBody, Summary: "恢复 FoxOS 租约原值", OwnedComment: comment}
	plan.Operations = []Operation{{Method: http.MethodPatch, Path: "/rest/ip/dhcp-server/lease/" + current.ID, Body: body, Summary: "更新已有 FoxOS 静态租约", OwnedComment: comment, Rollback: rollback}}
	if current.Address != policy.StaticIP {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("设备地址将从 %s 改为 %s", current.Address, policy.StaticIP))
	}
	return plan, nil
}

func CanonicalMAC(value string) (string, error) {
	mac, err := net.ParseMAC(value)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(mac.String()), nil
}

func isManagementAddress(value string) bool {
	return value == "10.0.0.1" || value == "10.0.0.2" || value == "10.0.0.3" || value == "10.0.0.4"
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
