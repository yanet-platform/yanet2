package main

import (
	"debug/dwarf"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAnonymousEnumName pins the key: order-independent, no path or line.
func TestAnonymousEnumName(t *testing.T) {
	a := anonymousEnumName([]*dwarf.EnumValue{{Name: "ip_family_ip4"}, {Name: "ip_family_ip6"}})
	b := anonymousEnumName([]*dwarf.EnumValue{{Name: "ip_family_ip6"}, {Name: "ip_family_ip4"}})
	require.Equal(t, a, b)
	require.Equal(t, "anon@ip_family_ip4,ip_family_ip6", a)
}
