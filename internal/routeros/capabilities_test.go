package routeros

import (
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func TestEgressCapabilitiesNeverTreatRouteMarkerAsTransparentDataPlane(t *testing.T) {
	t.Parallel()
	state := readyEgressState(domain.EgressMihomoNode, "node-a")
	capabilities := EgressCapabilities(&state)
	byMode := make(map[domain.EgressType]EgressCapability, len(capabilities))
	for _, capability := range capabilities {
		byMode[capability.Mode] = capability
	}
	if !byMode[domain.EgressDirect].Available || !byMode[domain.EgressBlocked].Available {
		t.Fatalf("expected direct and blocked readiness: %+v", byMode)
	}
	for _, mode := range []domain.EgressType{domain.EgressMihomoNode, domain.EgressProxyChain} {
		capability := byMode[mode]
		if capability.Available || !containsString(capability.Missing, "transparent_ingress_unverified") || !containsString(capability.Evidence, "route_marker_is_not_dataplane_evidence") {
			t.Fatalf("mode=%s capability=%+v", mode, capability)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
