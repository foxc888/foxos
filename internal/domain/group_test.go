package domain

import "testing"

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
