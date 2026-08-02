package routeros

import (
	"sort"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
)

type EgressCapability struct {
	Mode         domain.EgressType `json:"mode"`
	Available    bool              `json:"available"`
	Experimental bool              `json:"experimental"`
	Missing      []string          `json:"missing"`
	Evidence     []string          `json:"evidence,omitempty"`
}

func EgressCapabilities(state *EgressState) []EgressCapability {
	modes := []domain.EgressType{domain.EgressDirect, domain.EgressBlocked, domain.EgressMihomoNode, domain.EgressProxyChain, domain.EgressL2TP}
	output := make([]EgressCapability, 0, len(modes))
	if state == nil {
		for _, mode := range modes {
			output = append(output, EgressCapability{Mode: mode, Missing: []string{"routeros_not_configured"}})
		}
		return output
	}
	output = append(output, EgressCapability{Mode: domain.EgressDirect, Available: true, Evidence: []string{"foxos_owned_cleanup_only"}})

	blocked := EgressCapability{Mode: domain.EgressBlocked}
	blocked.Missing = append(blocked.Missing, filterReadiness(*state)...)
	blocked.Available = len(blocked.Missing) == 0
	if blocked.Available {
		blocked.Evidence = []string{"forward_anchor_first", "fasttrack_absent"}
	}
	output = append(output, blocked)

	for _, mode := range []domain.EgressType{domain.EgressMihomoNode, domain.EgressProxyChain} {
		output = append(output, EgressCapability{
			Mode: mode, Experimental: true,
			Missing:  []string{"transparent_ingress_unverified", "return_path_unverified", "management_bypass_unverified", "fasttrack_handling_unverified", "exit_ip_probe_unverified"},
			Evidence: []string{"route_marker_is_not_dataplane_evidence"},
		})
	}

	l2tp := EgressCapability{Mode: domain.EgressL2TP, Experimental: true}
	l2tp.Missing = append(l2tp.Missing, mangleReadiness(*state)...)
	running := false
	for _, item := range state.L2TPClients {
		if !disabled(item) && strings.EqualFold(item["running"], "true") {
			running = true
			break
		}
	}
	if !running {
		l2tp.Missing = append(l2tp.Missing, "running_l2tp_client_missing")
	}
	table := false
	for _, item := range state.RoutingTables {
		if strings.HasPrefix(item["name"], "foxos-l2tp-") && !disabled(item) && (strings.EqualFold(item["fib"], "yes") || strings.EqualFold(item["fib"], "true")) {
			table = true
			break
		}
	}
	if !table {
		l2tp.Missing = append(l2tp.Missing, "per_policy_l2tp_fib_table_missing")
	}
	sort.Strings(l2tp.Missing)
	l2tp.Available = len(l2tp.Missing) == 0
	if l2tp.Available {
		l2tp.Evidence = []string{"prerouting_anchor_first", "fasttrack_absent", "running_l2tp_client", "policy_fib_table_present"}
	}
	output = append(output, l2tp)
	return output
}

func filterReadiness(state EgressState) []string {
	missing := make([]string, 0, 2)
	if requireAnchor(state.FilterRules, "forward", "foxos:anchor:forward", egressFilterChain) != nil {
		missing = append(missing, "forward_anchor_missing_or_not_first")
	}
	if hasActiveFastTrack(state.FilterRules) {
		missing = append(missing, "active_fasttrack")
	}
	return missing
}

func mangleReadiness(state EgressState) []string {
	missing := make([]string, 0, 2)
	if requireAnchor(state.MangleRules, "prerouting", "foxos:anchor:mangle", egressMangleChain) != nil {
		missing = append(missing, "prerouting_anchor_missing_or_not_first")
	}
	if hasActiveFastTrack(state.FilterRules) {
		missing = append(missing, "active_fasttrack")
	}
	return missing
}
