package routeros

import (
	"context"
	"errors"
	"testing"
)

func dhcpPlanningState(ranges string, leases ...Lease) BindingState {
	return BindingState{
		Leases: leases,
		Pools: []IPPool{{
			ID: "*10", Name: "pool-lan", Ranges: ranges, Comment: "foxos:dhcp-pool:dhcp-lan",
		}},
		Networks: []DHCPNetwork{{ID: "*11", Address: "10.0.0.0/24", Gateway: "10.0.0.1"}},
		Addresses: []IPAddress{{
			ID: "*12", Address: "10.0.0.1/24", Network: "10.0.0.0", Interface: "bridge-lan", Disabled: "false",
		}},
		DHCPServers: []DHCPServer{{
			ID: "*13", Name: "dhcp-lan", Interface: "bridge-lan", AddressPool: "pool-lan", Running: "true", Disabled: "false",
		}},
	}
}

func TestPlanDHCPAddressesCountsInclusiveRangeEndpoints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		state           BindingState
		configured      int
		excluded        int
		dynamicCapacity int
		occupied        int
		remaining       int
	}{
		{
			name:       "100 through 200 is 101",
			state:      dhcpPlanningState("10.0.0.100-10.0.0.200"),
			configured: 101, dynamicCapacity: 101, remaining: 101,
		},
		{
			name:       "10 through 254 is 245",
			state:      dhcpPlanningState("10.0.0.10-10.0.0.254"),
			configured: 245, dynamicCapacity: 245, remaining: 245,
		},
		{
			name: "one reserved address inside range leaves 244",
			state: dhcpPlanningState("10.0.0.10-10.0.0.254",
				Lease{ID: "*1", Address: "10.0.0.20", MACAddress: "AA:BB:CC:DD:EE:FF", Server: "dhcp-lan", Dynamic: "false"},
				Lease{ID: "*2", Address: "10.0.0.21", MACAddress: "11:22:33:44:55:66", Server: "dhcp-lan", Dynamic: "true"},
			),
			configured: 245, excluded: 1, dynamicCapacity: 244, occupied: 1, remaining: 243,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, err := PlanDHCPAddresses(test.state)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Servers) != 1 {
				t.Fatalf("plan=%+v", plan)
			}
			got := plan.Servers[0]
			if got.ConfiguredCapacity != test.configured || got.ExcludedWithinPool != test.excluded ||
				got.DynamicCapacity != test.dynamicCapacity || got.DynamicOccupied != test.occupied || got.Remaining != test.remaining {
				t.Fatalf("capacity=%+v", got)
			}
		})
	}
}

func TestPlanDHCPAddressesUsesARPAsConflictEvidence(t *testing.T) {
	t.Parallel()
	state := dhcpPlanningState("10.0.0.100-10.0.0.200",
		Lease{ID: "*1", Address: "10.0.0.120", MACAddress: "AA:BB:CC:DD:EE:FF", Server: "dhcp-lan", Dynamic: "true"},
	)
	state.ARP = []ARP{
		{Address: "10.0.0.120", MACAddress: "AA:BB:CC:DD:EE:FF", Interface: "bridge-lan", Complete: "true"},
		{Address: "10.0.0.121", MACAddress: "11:22:33:44:55:66", Interface: "bridge-lan", Complete: "true"},
	}
	plan, err := PlanDHCPAddresses(state)
	if err != nil {
		t.Fatal(err)
	}
	got := plan.Servers[0]
	if got.DynamicOccupied != 1 || got.ExcludedWithinPool != 1 || got.DynamicCapacity != 100 {
		t.Fatalf("capacity=%+v", got)
	}
	foundARP := false
	for _, conflict := range got.Conflicts {
		foundARP = foundARP || conflict.Kind == "arp_pool_conflict" && conflict.Address == "10.0.0.121"
	}
	if !foundARP {
		t.Fatalf("conflicts=%+v", got.Conflicts)
	}
}

func TestPlanDHCPAddressesReadyRequiresEveryServer(t *testing.T) {
	t.Parallel()
	state := dhcpPlanningState("10.0.0.100-10.0.0.200")
	state.DHCPServers = append(state.DHCPServers, DHCPServer{
		ID: "*14", Name: "dhcp-unmapped", Interface: "bridge-other", AddressPool: "missing", Running: "true", Disabled: "false",
	})
	plan, err := PlanDHCPAddresses(state)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ready || len(plan.Servers) != 2 || plan.Servers[1].Ready || plan.Servers[1].Error == "" {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestPreviewDHCPExpansionRequiresExactOwnedPlan(t *testing.T) {
	t.Parallel()
	state := dhcpPlanningState("10.0.0.100-10.0.0.200")
	plan, err := PreviewDHCPExpansion(state, DHCPExpansionRequest{
		ServerName: "dhcp-lan", RequestedCapacity: 150, ProposedRanges: "10.0.0.50-10.0.0.200",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Executable || !plan.RequiresConfirmation || plan.After == nil ||
		plan.Before.DynamicCapacity != 101 || plan.After.DynamicCapacity != 151 ||
		plan.Operation == nil || plan.Operation.Path != "/rest/ip/pool/*10" {
		t.Fatalf("plan=%+v", plan)
	}
	state.Pools[0].Comment = "user-pool"
	preview, err := PreviewDHCPExpansion(state, DHCPExpansionRequest{
		ServerName: "dhcp-lan", RequestedCapacity: 150, ProposedRanges: "10.0.0.50-10.0.0.200",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Executable || preview.RequiresConfirmation || len(preview.Warnings) < 2 {
		t.Fatalf("preview=%+v", preview)
	}
}

func TestPreviewDHCPExpansionRejectsShrinkAndComparesLargeNetworkOptions(t *testing.T) {
	t.Parallel()
	state := dhcpPlanningState("10.0.0.100-10.0.0.200")
	if _, err := PreviewDHCPExpansion(state, DHCPExpansionRequest{
		ServerName: "dhcp-lan", RequestedCapacity: 80, ProposedRanges: "10.0.0.120-10.0.0.200",
	}); !errors.Is(err, ErrDHCPPlan) {
		t.Fatalf("shrink err=%v", err)
	}
	preview, err := PreviewDHCPExpansion(state, DHCPExpansionRequest{
		ServerName: "dhcp-lan", RequestedCapacity: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Executable || len(preview.Alternatives) != 2 ||
		preview.Alternatives[0].Strategy != "expand-to-/23" ||
		preview.Alternatives[1].Strategy != "split-vlan" {
		t.Fatalf("preview=%+v", preview)
	}
}

type dhcpExpansionWriterStub struct {
	state         BindingState
	writeCalls    int
	leaveExternal bool
}

func (s *dhcpExpansionWriterStub) BindingState(context.Context) (BindingState, error) {
	return cloneBindingState(s.state), nil
}

func (s *dhcpExpansionWriterStub) WriteDHCPPoolRanges(_ context.Context, id, owner, ranges string) error {
	s.writeCalls++
	for index := range s.state.Pools {
		if s.state.Pools[index].ID == id && s.state.Pools[index].Comment == owner {
			if s.leaveExternal && s.writeCalls == 1 {
				s.state.Pools[index].Ranges = "10.0.0.40-10.0.0.200"
			} else {
				s.state.Pools[index].Ranges = ranges
			}
			return nil
		}
	}
	return ErrDHCPExpansionStale
}

func TestDHCPExpansionExecutorUsesCASReadback(t *testing.T) {
	t.Parallel()
	state := dhcpPlanningState("10.0.0.100-10.0.0.200")
	plan, err := PreviewDHCPExpansion(state, DHCPExpansionRequest{
		ServerName: "dhcp-lan", RequestedCapacity: 150, ProposedRanges: "10.0.0.50-10.0.0.200",
	})
	if err != nil {
		t.Fatal(err)
	}
	writer := &dhcpExpansionWriterStub{state: cloneBindingState(state)}
	if err := (DHCPExpansionExecutor{Writer: writer}).Execute(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if writer.writeCalls != 1 || writer.state.Pools[0].Ranges != plan.ProposedRanges {
		t.Fatalf("writer=%+v", writer)
	}
	writer = &dhcpExpansionWriterStub{state: cloneBindingState(state), leaveExternal: true}
	if err := (DHCPExpansionExecutor{Writer: writer}).Execute(context.Background(), plan); !errors.Is(err, ErrCompensationStateChanged) {
		t.Fatalf("external change err=%v", err)
	}
	if writer.writeCalls != 1 {
		t.Fatalf("external state was overwritten: calls=%d", writer.writeCalls)
	}
}
