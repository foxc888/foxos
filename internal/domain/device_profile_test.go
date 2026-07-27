package domain

import (
	"errors"
	"testing"
)

func TestValidateDeviceMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		alias   string
		vendor  string
		tags    []string
		wantErr bool
	}{
		{name: "valid", alias: "Office laptop", vendor: "Framework", tags: []string{"work", "trusted"}},
		{name: "duplicate tags", tags: []string{"IoT", "iot"}, wantErr: true},
		{name: "surrounding whitespace", alias: " laptop", wantErr: true},
		{name: "control character", vendor: "bad\nvalue", wantErr: true},
		{name: "too many tags", tags: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15", "16", "17"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateDeviceMetadata(test.alias, test.vendor, test.tags)
			if errors.Is(err, ErrInvalidDeviceProfile) != test.wantErr {
				t.Fatalf("err=%v wantErr=%t", err, test.wantErr)
			}
		})
	}
}

func TestNormalizeMAC(t *testing.T) {
	t.Parallel()
	value, err := NormalizeMAC("aa-bb-cc-dd-ee-ff")
	if err != nil || value != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("value=%q err=%v", value, err)
	}
}
