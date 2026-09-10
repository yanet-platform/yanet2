package netplan_test

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// Test_Parse_DataplaneFixture verifies that only the two KNI, eight VLANs and
// existing loopback are managed, including explicitly configured IPv6LL.
func Test_Parse_DataplaneFixture(t *testing.T) {
	state, err := netplan.ParseFile("testdata/dataplane.yaml")
	require.NoError(t, err)
	require.Len(t, state.Links, 11)
	counts := map[netplan.LinkKind]int{}
	for _, link := range state.Links {
		counts[link.Kind]++
		require.Equal(t, 9000, link.MTU)
		if link.Kind == netplan.LinkKindVLAN {
			require.Contains(t, []int{1600, 1619, 2000, 802}, link.VLANID)
			require.False(t, link.IPv6LinkLocal)
			expected := "fe80::f1/64"
			if link.Parent == "kni1" {
				expected = "fe80::1f1/64"
			}
			require.Contains(t, link.Addresses, netip.MustParsePrefix(expected))
		}
	}
	require.Equal(t, map[netplan.LinkKind]int{
		netplan.LinkKindKNI: 2, netplan.LinkKindVLAN: 8, netplan.LinkKindLoopback: 1,
	}, counts)
}

// Test_Parse_ManagedBoundary verifies that unsupported managed input fails
// while omitted sections, aliases and unrelated host configuration are accepted.
func Test_Parse_ManagedBoundary(t *testing.T) {
	for _, test := range []struct {
		name  string
		yaml  string
		valid bool
	}{
		{name: "omitted sections", yaml: "network: {version: 2}", valid: true},
		{name: "explicit empty sections", yaml: "network: {version: 2, ethernets: {}, vlans: {}, dummy-devices: {}}", valid: true},
		{name: "aliases and empty lists", yaml: "defaults: &empty {addresses: [], link-local: []}\nnetwork: {version: 2, ethernets: {kni0: *empty}}", valid: true},
		{name: "misspelled section", yaml: "network: {version: 2, etherents: {kni0: {}}}"},
		{name: "unknown section", yaml: "network: {version: 2, unrelated: {}}"},
		{name: "known host sections", yaml: "network: {version: 2, renderer: networkd, bonds: {}, bridges: {}, wifis: {}, tunnels: {}}", valid: true},
		{name: "missing network", yaml: "other: {}"},
		{name: "null network", yaml: "network: null"},
		{name: "missing version", yaml: "network: {}"},
		{name: "unsupported version", yaml: "network: {version: 1}"},
		{name: "fractional version", yaml: "network: {version: 2.0}"},
		{name: "string version", yaml: "network: {version: '2'}"},
		{name: "null ethernets", yaml: "network: {version: 2, ethernets: null}"},
		{name: "sequence VLANs", yaml: "network: {version: 2, vlans: []}"},
		{name: "null dummy section", yaml: "network: {version: 2, dummy-devices: null}"},
		{name: "null managed link", yaml: "network: {version: 2, ethernets: {kni0: null}}"},
		{name: "null addresses", yaml: "network: {version: 2, ethernets: {kni0: {addresses: null}}}"},
		{name: "invalid address", yaml: "network: {version: 2, ethernets: {kni0: {addresses: [invalid]}}}"},
		{name: "IPv6 prefix conflict", yaml: "network: {version: 2, ethernets: {kni0: {addresses: ['fe80::1/64', 'fe80::1/128']}}}"},
		{name: "DHCP4 enabled", yaml: "network: {version: 2, ethernets: {kni0: {dhcp4: true}}}"},
		{name: "DHCP6 enabled", yaml: "network: {version: 2, dummy-devices: {loop1: {dhcp6: true}}}"},
		{name: "IPv4LL enabled", yaml: "network: {version: 2, ethernets: {kni0: {link-local: [ipv4]}}}"},
		{name: "unknown address family", yaml: "network: {version: 2, ethernets: {kni0: {link-local: [ipx]}}}"},
		{name: "activation disabled", yaml: "network: {version: 2, ethernets: {kni0: {activation-mode: off}}}"},
		{name: "unknown managed setting", yaml: "network: {version: 2, ethernets: {kni0: {typo: 1}}}"},
		{name: "null RA", yaml: "network: {version: 2, ethernets: {kni0: {accept-ra: null}}}"},
		{name: "undersized MTU", yaml: "network: {version: 2, ethernets: {kni0: {mtu: 1200}}}"},
		{name: "negative MTU", yaml: "network: {version: 2, ethernets: {kni0: {mtu: -1}}}"},
		{name: "oversized MTU", yaml: "network: {version: 2, ethernets: {kni0: {mtu: 2147483648}}}"},
		{name: "invalid dummy name", yaml: "network: {version: 2, dummy-devices: {'../x': {}}}"},
		{name: "global sysctl name", yaml: "network: {version: 2, dummy-devices: {all: {}}}"},
		{name: "dummy cannot create KNI", yaml: "network: {version: 2, dummy-devices: {kni0: {}}}"},
		{name: "missing VLAN parent", yaml: "network: {version: 2, vlans: {v100: {id: 100}}}"},
		{name: "unknown VLAN parent", yaml: "network: {version: 2, vlans: {v100: {id: 100, link: unknown}}}"},
		{name: "stacked VLAN", yaml: "network: {version: 2, ethernets: {kni0: {}}, vlans: {v100: {id: 100, link: kni0}, v200: {id: 200, link: v100}}}"},
		{name: "duplicate name", yaml: "network: {version: 2, ethernets: {kni0: {}}, vlans: {kni0: {id: 100, link: kni0}}}"},
		{name: "duplicate VLAN identity", yaml: "network: {version: 2, ethernets: {kni0: {}}, vlans: {a: {id: 100, link: kni0}, b: {id: 100, link: kni0}}}"},
		{name: "invalid YAML", yaml: "network: ["},
		{name: "multiple documents", yaml: "network: {version: 2}\n---\nnetwork: {version: 2}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := netplan.Parse([]byte(test.yaml))
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Equal(t, netplan.State{}, state)
			}
		})
	}
}

// Test_Parse_DecimalMTU verifies that leading zeros and YAML merge keys retain
// the decimal MTU rather than the YAML decoder's octal interpretation.
func Test_Parse_DecimalMTU(t *testing.T) {
	for _, test := range []struct {
		name string
		yaml string
		mtu  int
	}{
		{name: "leading zero", yaml: "network: {version: 2, ethernets: {kni0: {mtu: 03000}}}", mtu: 3000},
		{name: "merged MTU", yaml: "defaults: &base {mtu: 9000}\nnetwork: {version: 2, ethernets: {kni0: {<<: *base}}}", mtu: 9000},
		{name: "aliased network section", yaml: "defaults: &base {ethernets: {kni0: {mtu: 09000}}}\nnetwork: {<<: *base, version: 2}", mtu: 9000},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := netplan.Parse([]byte(test.yaml))
			require.NoError(t, err)
			require.Len(t, state.Links, 1)
			require.Equal(t, test.mtu, state.Links[0].MTU)
		})
	}
}

// Test_Parse_LinkLocalGrammar verifies that omission enables IPv6 generation
// and explicit empty or IPv6-only lists select the supported boolean policy.
func Test_Parse_LinkLocalGrammar(t *testing.T) {
	for _, test := range []struct {
		name    string
		fields  string
		enabled bool
	}{
		{name: "default generation", fields: "{}", enabled: true},
		{name: "disabled generation", fields: "{link-local: []}"},
		{name: "explicit IPv6 generation", fields: "{link-local: [ipv6]}", enabled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := netplan.Parse([]byte("network: {version: 2, ethernets: {kni0: " + test.fields + "}}"))
			require.NoError(t, err)
			require.Equal(t, test.enabled, state.Links[0].IPv6LinkLocal)
		})
	}
}

// Test_Parse_DeterministicDiagnostic verifies that multiple invalid links and
// fields report the same lexicographically first failure on every parse.
func Test_Parse_DeterministicDiagnostic(t *testing.T) {
	data := []byte("network: {version: 2, ethernets: {kni1: {zzz: true}, kni0: {zzz: true, aaa: true}}}")
	for range 20 {
		_, err := netplan.Parse(data)
		require.ErrorContains(t, err, `link "kni0": unsupported setting "aaa"`)
	}
}

// Test_Parse_DecimalVLANGrammar verifies that zero and leading decimal zeros
// are accepted, while omitted IDs and nondecimal syntax cannot become VLAN zero.
func Test_Parse_DecimalVLANGrammar(t *testing.T) {
	for _, test := range []struct {
		name       string
		field      string
		identifier int
		valid      bool
	}{
		{name: "zero", field: "id: 0", valid: true},
		{name: "maximum", field: "id: 4094", identifier: 4094, valid: true},
		{name: "quoted", field: "id: '802'", identifier: 802, valid: true},
		{name: "leading zeros", field: "id: 0010", identifier: 10, valid: true},
		{name: "quoted leading zeros", field: "id: '0802'", identifier: 802, valid: true},
		{name: "missing", field: ""},
		{name: "null", field: "id: null"},
		{name: "empty", field: "id: ''"},
		{name: "negative", field: "id: -1"},
		{name: "too large", field: "id: 4095"},
		{name: "overflow", field: "id: 999999999999999999999"},
		{name: "hex", field: "id: 0x10"},
		{name: "plus sign", field: "id: +10"},
		{name: "fraction", field: "id: 10.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := netplan.Parse(fmt.Appendf(nil,
				"network:\n  version: 2\n  ethernets: {kni0: {}}\n  vlans:\n    v0:\n      link: kni0\n      %s\n",
				test.field,
			))
			if test.valid {
				require.NoError(t, err)
				require.Equal(t, test.identifier, state.Links[1].VLANID)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// Test_Parse_HostRoutingAndLoopbacks verifies that host interfaces and routing
// fields do not affect the managed topology or loopback egress eligibility.
func Test_Parse_HostRoutingAndLoopbacks(t *testing.T) {
	state, err := netplan.Parse([]byte(`
network:
  version: 2
  ethernets:
    eth0: {dhcp4: true, addresses: [invalid]}
    eth1: null
    kni0:
      addresses: [192.0.2.7/24, '2001:db8::7/64']
      routes: [{to: default, via: invalid, table: invalid}]
      routing-policy: invalid
    lo: {addresses: ['2001:db8::1/128']}
  dummy-devices:
    loop1: {addresses: [198.51.100.1/32]}
  vlans:
    host.1: {link: eth0, id: invalid}
`))
	require.NoError(t, err)
	require.Len(t, state.Links, 3)
	require.True(t, state.Links[0].IsEgress())
	require.Equal(t, netip.MustParsePrefix("192.0.2.7/24"), state.Links[0].Addresses[0])
	require.Equal(t, netplan.LinkKindLoopback, state.Links[1].Kind)
	require.Equal(t, netplan.LinkKindDummy, state.Links[2].Kind)
	require.False(t, state.Links[1].IsEgress())
	require.False(t, state.Links[2].IsEgress())
}
