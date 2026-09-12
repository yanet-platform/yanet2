package native_test

import (
	"net/netip"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/native"
)

// Test_Source_Fixture verifies that native input yields sorted, normalized
// KNI, VLAN, loopback and dummy configuration without an external file.
func Test_Source_Fixture(t *testing.T) {
	data, err := os.ReadFile("testdata/native.yaml")
	require.NoError(t, err)
	var config native.Config
	require.NoError(t, yaml.Unmarshal(data, &config))
	var source desired.Source = &native.Source{Config: config}
	state, err := source.Load()
	require.NoError(t, err)
	acceptRA := false
	require.Equal(t, desired.State{Links: []desired.Link{
		{Name: "dummy0", Kind: desired.LinkKindDummy, Addresses: []netip.Prefix{netip.MustParsePrefix("198.51.100.1/32")}},
		{Name: "kni0", Kind: desired.LinkKindKNI, MTU: 1500, IPv6LinkLocal: true, AcceptRA: &acceptRA},
		{Name: "kni0.100", Kind: desired.LinkKindVLAN, Parent: "kni0", VLANID: 100, MTU: 1500, IPv6LinkLocal: true, AcceptRA: &acceptRA,
			Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.2/24"), netip.MustParsePrefix("2001:db8:100::2/64")}},
		{Name: "lo", Kind: desired.LinkKindLoopback, IPv6LinkLocal: true, Addresses: []netip.Prefix{netip.MustParsePrefix("2001:db8::1/128")}},
	}}, state)
}

// Test_Source_Ownership verifies that config mutation and later loads cannot
// alter an earlier state, including its addresses and optional RA setting.
func Test_Source_Ownership(t *testing.T) {
	var config native.Config
	require.NoError(t, yaml.Unmarshal([]byte("ethernets: {kni0: {accept-ra: false, addresses: ['192.0.2.7/24'], link-local: [ipv6]}}"), &config))
	var source desired.Source = &native.Source{Config: config}
	first, err := source.Load()
	require.NoError(t, err)
	expected := first.Clone()
	second, err := source.Load()
	require.NoError(t, err)
	second.Links[0].Addresses[0] = netip.MustParsePrefix("2001:db8::2/64")
	*second.Links[0].AcceptRA = true
	link := config.Ethernets["kni0"]
	link.Addresses[0] = "198.51.100.2/32"
	*link.AcceptRA = true
	*link.LinkLocal = nil
	require.Equal(t, expected, first)
	third, err := source.Load()
	require.NoError(t, err)
	require.NotEqual(t, expected, third)
}

// Test_Source_Defaults verifies that empty topology and optional fields retain
// their defaults while VLAN zero and decimal leading zeros remain explicit.
func Test_Source_Defaults(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want desired.State
	}{
		{name: "empty topology", data: "{}"},
		{name: "defaults", data: "ethernets: {kni0: {}}", want: desired.State{Links: []desired.Link{{Name: "kni0", IPv6LinkLocal: true}}}},
		{name: "zero MTU", data: "ethernets: {kni0: {mtu: 0}}", want: desired.State{Links: []desired.Link{{Name: "kni0", IPv6LinkLocal: true}}}},
		{name: "decimal MTU", data: "ethernets: {kni0: {mtu: 03000}}", want: desired.State{Links: []desired.Link{{Name: "kni0", MTU: 3000, IPv6LinkLocal: true}}}},
		{name: "VLAN zero", data: "ethernets: {kni0: {}}\nvlans: {v0: {id: 0, link: kni0}}", want: desired.State{Links: []desired.Link{
			{Name: "kni0", IPv6LinkLocal: true}, {Name: "v0", Kind: desired.LinkKindVLAN, Parent: "kni0", IPv6LinkLocal: true},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config native.Config
			require.NoError(t, yaml.Unmarshal([]byte(tc.data), &config))
			var source desired.Source = &native.Source{Config: config}
			state, err := source.Load()
			require.NoError(t, err)
			require.Equal(t, tc.want, state)
		})
	}
}

// Test_Source_DirectConfigValidation verifies that constructing Go config
// directly cannot bypass DHCP, topology, prefix or MTU validation.
func Test_Source_DirectConfigValidation(t *testing.T) {
	zero, invalidID := 0, 4095
	ipv4, duplicate := []string{"ipv4"}, []string{"ipv6", "ipv6"}
	for _, tc := range []struct {
		name   string
		config native.Config
	}{
		{name: "enabled DHCP4", config: native.Config{Ethernets: map[string]native.LinkConfig{"kni0": {DHCP4: true}}}},
		{name: "enabled DHCP6", config: native.Config{DummyDevices: map[string]native.LinkConfig{"dummy0": {DHCP6: true}}}},
		{name: "enabled VLAN DHCP", config: native.Config{VLANs: map[string]native.LinkConfig{"v0": {ID: &zero, Link: "kni0", DHCP4: true}}}},
		{name: "onboard name", config: native.Config{Ethernets: map[string]native.LinkConfig{"eth0": {}}}},
		{name: "unsupported Ethernet", config: native.Config{Ethernets: map[string]native.LinkConfig{"enp0": {}}}},
		{name: "bad prefix", config: native.Config{Ethernets: map[string]native.LinkConfig{"kni0": {Addresses: []string{"invalid"}}}}},
		{name: "IPv6 prefix conflict", config: native.Config{Ethernets: map[string]native.LinkConfig{"kni0": {Addresses: []string{"fe80::1/64", "fe80::1/128"}}}}},
		{name: "duplicate link", config: native.Config{Ethernets: map[string]native.LinkConfig{"kni0": {}}, DummyDevices: map[string]native.LinkConfig{"kni0": {}}}},
		{name: "missing VLAN ID", config: native.Config{VLANs: map[string]native.LinkConfig{"v0": {Link: "kni0"}}}},
		{name: "missing VLAN link", config: native.Config{VLANs: map[string]native.LinkConfig{"v0": {ID: &zero}}}},
		{name: "unknown parent", config: native.Config{VLANs: map[string]native.LinkConfig{"v0": {ID: &zero, Link: "kni0"}}}},
		{name: "bad VLAN ID", config: native.Config{Ethernets: map[string]native.LinkConfig{"kni0": {}}, VLANs: map[string]native.LinkConfig{"v0": {ID: &invalidID, Link: "kni0"}}}},
		{name: "non VLAN ID", config: native.Config{Ethernets: map[string]native.LinkConfig{"kni0": {ID: &zero}}}},
		{name: "non VLAN link", config: native.Config{DummyDevices: map[string]native.LinkConfig{"dummy0": {Link: "kni0"}}}},
		{name: "IPv4 link local", config: native.Config{Ethernets: map[string]native.LinkConfig{"kni0": {LinkLocal: &ipv4}}}},
		{name: "duplicate link local", config: native.Config{Ethernets: map[string]native.LinkConfig{"kni0": {LinkLocal: &duplicate}}}},
		{name: "negative MTU", config: native.Config{Ethernets: map[string]native.LinkConfig{"kni0": {MTU: -1}}}},
		{name: "MTU disables IPv6", config: native.Config{Ethernets: map[string]native.LinkConfig{"kni0": {MTU: 1200}}}},
		{name: "child exceeds parent MTU", config: native.Config{Ethernets: map[string]native.LinkConfig{"kni0": {MTU: 1500}}, VLANs: map[string]native.LinkConfig{"v0": {ID: &zero, Link: "kni0", MTU: 9000}}}},
		{name: "stacked VLAN", config: native.Config{Ethernets: map[string]native.LinkConfig{"kni0": {}}, VLANs: map[string]native.LinkConfig{"v0": {ID: &zero, Link: "kni0"}, "v1": {ID: &zero, Link: "v0"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var source desired.Source = &native.Source{Config: tc.config}
			state, err := source.Load()
			require.Error(t, err)
			require.Equal(t, desired.State{}, state)
		})
	}
}
