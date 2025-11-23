package lib

import "testing"

func TestExpandCIDR_Basic(t *testing.T) {
	got := ExpandCIDR("172.20.29.5/30")
	if len(got) != 4 {
		t.Fatalf("expected 4 addresses for /30, got %d", len(got))
	}
	// Ensure first and last look plausible without strict ordering requirements
	if got[0] == "" || got[len(got)-1] == "" {
		t.Fatalf("unexpected empty ip in expansion")
	}
}


