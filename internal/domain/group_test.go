package domain

import (
	"fmt"
	"strings"
	"testing"
)

func TestGroupValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		group   Group
		wantErr bool
	}{
		{name: "select", group: Group{ID: "select", Name: "Select", Type: "select", NodeIDs: []string{"node-a"}}},
		{name: "ordered chain", group: Group{ID: "chain", Name: "Chain", Type: "chain", NodeIDs: []string{"entry", "relay"}}},
		{name: "chain needs two nodes", group: Group{ID: "chain", Name: "Chain", Type: "chain", NodeIDs: []string{"entry"}}, wantErr: true},
		{name: "chain rejects nested groups", group: Group{ID: "chain", Name: "Chain", Type: "chain", NodeIDs: []string{"entry", "relay"}, GroupIDs: []string{"other"}}, wantErr: true},
		{name: "duplicate member", group: Group{ID: "select", Name: "Select", Type: "select", NodeIDs: []string{"node-a", "node-a"}}, wantErr: true},
		{name: "health URL requires HTTP", group: Group{ID: "test", Name: "Test", Type: "url-test", NodeIDs: []string{"node-a"}, URL: "file:///etc/passwd"}, wantErr: true},
		{name: "health URL rejects credentials", group: Group{ID: "test", Name: "Test", Type: "url-test", NodeIDs: []string{"node-a"}, URL: "https://user:pass@example.com/"}, wantErr: true},
		{name: "health interval lower boundary", group: Group{ID: "test", Name: "Test", Type: "url-test", NodeIDs: []string{"node-a"}, URL: "https://example.com/", Interval: 10}},
		{name: "health interval too aggressive", group: Group{ID: "test", Name: "Test", Type: "url-test", NodeIDs: []string{"node-a"}, URL: "https://example.com/", Interval: 9}, wantErr: true},
		{name: "health interval upper boundary", group: Group{ID: "test", Name: "Test", Type: "url-test", NodeIDs: []string{"node-a"}, URL: "https://example.com/", Interval: 86400}},
		{name: "health interval too large", group: Group{ID: "test", Name: "Test", Type: "url-test", NodeIDs: []string{"node-a"}, URL: "https://example.com/", Interval: 86401}, wantErr: true},
		{name: "url test tolerance boundary", group: Group{ID: "test", Name: "Test", Type: "url-test", NodeIDs: []string{"node-a"}, URL: "https://example.com/", Tolerance: 10000}},
		{name: "fallback rejects tolerance", group: Group{ID: "fallback", Name: "Fallback", Type: "fallback", NodeIDs: []string{"node-a"}, URL: "https://example.com/", Tolerance: 1}, wantErr: true},
		{name: "load balance strategy", group: Group{ID: "balance", Name: "Balance", Type: "load-balance", NodeIDs: []string{"node-a"}, URL: "https://example.com/", Strategy: "round-robin"}},
		{name: "invalid load balance strategy", group: Group{ID: "balance", Name: "Balance", Type: "load-balance", NodeIDs: []string{"node-a"}, URL: "https://example.com/", Strategy: "random"}, wantErr: true},
		{name: "select rejects strategy", group: Group{ID: "select", Name: "Select", Type: "select", NodeIDs: []string{"node-a"}, Strategy: "round-robin"}, wantErr: true},
		{name: "member limit", group: Group{ID: "large", Name: "Large", Type: "select", NodeIDs: memberIDs(maxGroupMembers)}},
		{name: "member limit exceeded", group: Group{ID: "large", Name: "Large", Type: "select", NodeIDs: memberIDs(maxGroupMembers + 1)}, wantErr: true},
		{name: "chain hop limit", group: Group{ID: "chain", Name: "Chain", Type: "chain", NodeIDs: memberIDs(maxChainHops)}},
		{name: "chain hop limit exceeded", group: Group{ID: "chain", Name: "Chain", Type: "chain", NodeIDs: memberIDs(maxChainHops + 1)}, wantErr: true},
		{name: "name delimiter", group: Group{ID: "select", Name: "Invalid,Name", Type: "select", NodeIDs: []string{"node-a"}}, wantErr: true},
		{name: "oversized URL", group: Group{ID: "test", Name: "Test", Type: "url-test", NodeIDs: []string{"node-a"}, URL: "https://example.com/" + strings.Repeat("x", 2048)}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.group.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error=%v, wantErr=%t", err, test.wantErr)
			}
		})
	}
}

func memberIDs(count int) []string {
	result := make([]string, count)
	for index := range result {
		result[index] = fmt.Sprintf("node-%d", index)
	}
	return result
}
