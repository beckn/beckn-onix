package catalog

// eligibility_test.go — tests for Select (network-scope eligibility): public,
// matching, and non-matching network cases.

import (
	"reflect"
	"testing"
)

func TestSelect(t *testing.T) {
	crawler := []string{"network-a.example.com", "network-b.example.com"}
	tests := []struct {
		name        string
		networkIDs  []string
		wantTake    bool
		wantVisible []string
	}{
		{"public (nil networkIds) is always taken, visible to all", nil, true, nil},
		{"empty networkIds is public", []string{}, true, nil},
		{"network catalog matching a crawler network is taken", []string{"network-a.example.com"}, true, []string{"network-a.example.com"}},
		{"network catalog not matching is skipped", []string{"other.net"}, false, nil},
		{"matches one of several networks", []string{"other.net", "network-b.example.com"}, true, []string{"other.net", "network-b.example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := CatalogEntry{CatalogID: "p/c", Status: StatusActive, NetworkIDs: tt.networkIDs}
			take, visible := Select(e, crawler)
			if take != tt.wantTake {
				t.Fatalf("take = %v, want %v", take, tt.wantTake)
			}
			if take && !reflect.DeepEqual(visible, tt.wantVisible) {
				t.Fatalf("visibleTo = %v, want %v", visible, tt.wantVisible)
			}
		})
	}
}
