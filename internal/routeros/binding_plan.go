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
}
type Plan struct {
	PolicyID             string      `json:"policyId"`
	Operations           []Operation `json:"operations"`
	Warnings             []string    `json:"warnings"`
	RequiresConfirmation bool        `json:"requiresConfirmation"`
}

func PlanDeviceBinding(policy domain.DevicePolicy, leases []Lease) (Plan, error) {
	if err := policy.Validate(); err != nil {
		return Plan{}, err
	}
	mac := normalizeMAC(policy.MACAddress)
	comment := "foxos:device:" + policy.ID
	var current *Lease
	for index := range leases {
		lease := leases[index]
		leaseMAC := normalizeMAC(lease.MACAddress)
		if lease.Address == policy.StaticIP && leaseMAC != "" && leaseMAC != mac {
			return Plan{}, fmt.Errorf("%w: IP %s is already used by %s", ErrPlanConflict, policy.StaticIP, leaseMAC)
		}
		if leaseMAC == mac {
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
	if current.Address == policy.StaticIP && current.Dynamic != "true" {
		plan.RequiresConfirmation = false
		return plan, nil
	}
	if strings.TrimSpace(current.ID) == "" {
		return Plan{}, fmt.Errorf("%w: existing lease has no RouterOS ID", ErrPlanConflict)
	}
	plan.Operations = []Operation{{Method: http.MethodPatch, Path: "/rest/ip/dhcp-server/lease/" + current.ID, Body: body, Summary: "将现有租约转换为 FoxOS 管理的静态租约", OwnedComment: comment}}
	if current.Address != policy.StaticIP {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("设备地址将从 %s 改为 %s", current.Address, policy.StaticIP))
	}
	if current.Dynamic == "true" {
		plan.Warnings = append(plan.Warnings, "现有动态租约将转换为静态租约")
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
