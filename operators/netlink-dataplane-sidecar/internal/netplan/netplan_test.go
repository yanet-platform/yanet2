package netplan_test

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// Test_Parse_IPv4AndIPv6State verifies that managed base and VLAN settings are
// converted to typed links while address host bits remain intact.
func Test_Parse_IPv4AndIPv6State(t *testing.T) {
	state, err := netplan.Parse([]byte(`
network:
  version: 2
  ethernets:
    kni0:
      mtu: 9000
      addresses:
        - 192.0.2.10/24
        - 2001:db8::10/64
      accept-ra: true
      link-local: [ipv6, ipv4]
  vlans:
    tenant.100:
      link: kni0
      id: 100
      mtu: 8900
      addresses: [198.51.100.7/25, 2001:db8:1::7/64]
      accept-ra: false
      link-local: []
`))
	require.NoError(t, err)
	require.Equal(t, netplan.State{Links: []netplan.Link{
		{
			Name: "kni0",
			MTU:  9000,
			Addresses: []netip.Prefix{
				netip.MustParsePrefix("192.0.2.10/24"),
				netip.MustParsePrefix("2001:db8::10/64"),
			},
			AcceptRA: boolPointer(true),
			LinkLocal: []string{
				"ipv6",
				"ipv4",
			},
		},
		{
			Name:   "tenant.100",
			Parent: "kni0",
			VLANID: 100,
			MTU:    8900,
			Addresses: []netip.Prefix{
				netip.MustParsePrefix("198.51.100.7/25"),
				netip.MustParsePrefix("2001:db8:1::7/64"),
			},
			AcceptRA:  boolPointer(false),
			LinkLocal: []string{},
		},
	}}, state)
}

// Test_Parse_RejectsNullManagedStanza verifies that incomplete managed entries
// cannot become authoritative empty address configurations.
func Test_Parse_RejectsNullManagedStanza(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "bare managed key", value: ""},
		{name: "explicit null", value: "null"},
		{name: "null shorthand", value: "~"},
		{name: "alias to null", value: "*empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := fmt.Sprintf(`
defaults: &empty null
network:
  version: 2
  vlans: {}
  ethernets:
    kni0: %s
`, test.value)

			state, err := netplan.Parse([]byte(data))

			require.ErrorContains(t, err, `link "kni0": configuration mapping is required`)
			require.Equal(t, netplan.State{}, state)
		})
	}
}

// Test_Parse_AcceptsExplicitEmptyManagedStanza verifies that intentionally
// empty mappings, including aliases, retain their normal clearing semantics.
func Test_Parse_AcceptsExplicitEmptyManagedStanza(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "empty mapping", value: "{}"},
		{name: "alias to empty mapping", value: "*empty"},
		{name: "explicit empty addresses", value: "{addresses: []}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := fmt.Sprintf(`
defaults: &empty {}
network:
  version: 2
  vlans: {}
  ethernets:
    management0: null
    kni0: %s
`, test.value)

			state, err := netplan.Parse([]byte(data))

			require.NoError(t, err)
			require.Len(t, state.Links, 1)
			require.Equal(t, "kni0", state.Links[0].Name)
			require.Empty(t, state.Links[0].Addresses)
		})
	}
}

// Test_Parse_RejectsNegativeMTU verifies that negative YAML sizes are rejected
// with the affected managed link identified in the error.
func Test_Parse_RejectsNegativeMTU(t *testing.T) {
	_, err := netplan.Parse([]byte(`
network:
  version: 2
  ethernets:
    kni0:
      mtu: -1
  vlans: {}
`))

	require.ErrorContains(t, err, `link "kni0": MTU must be within`)
}

// Test_Parse_RejectsOversizedMTU verifies that a YAML size above the signed
// kernel limit is rejected even when the host integer can represent it.
func Test_Parse_RejectsOversizedMTU(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("int cannot represent an MTU above MaxInt32")
	}
	data := fmt.Sprintf(`
network:
  version: 2
  ethernets:
    kni0:
      mtu: %d
  vlans: {}
`, uint64(1)<<31)

	_, err := netplan.Parse([]byte(data))

	require.ErrorContains(t, err, `link "kni0": MTU must be within`)
}

// Test_Parse_RejectsKernelInvalidManagedVLANName verifies that a quoted YAML
// name cannot bypass Linux's prohibition on colons in interface names.
func Test_Parse_RejectsKernelInvalidManagedVLANName(t *testing.T) {
	_, err := netplan.Parse([]byte(`
network:
  version: 2
  ethernets:
    kni0: {}
  vlans:
    "tenant:100":
      link: kni0
      id: 100
`))

	require.ErrorContains(t, err, `vlan "tenant:100": interface name contains`)
}

// Test_Parse_RejectsDuplicateManagedVLANIdentity verifies that distinct YAML
// names cannot request the same VLAN tag on the same managed parent.
func Test_Parse_RejectsDuplicateManagedVLANIdentity(t *testing.T) {
	_, err := netplan.Parse([]byte(`
network:
  version: 2
  ethernets:
    kni0: {}
  vlans:
    first.100:
      link: kni0
      id: 100
    second.100:
      link: kni0
      id: 100
`))

	require.ErrorContains(t, err, `parent "kni0" ID 100 is already used`)
}

// Test_Parse_IgnoresUnmanagedContent verifies that unsupported values outside
// the managed KNI hierarchy cannot invalidate the resulting state.
func Test_Parse_IgnoresUnmanagedContent(t *testing.T) {
	state, err := netplan.Parse([]byte(`
network:
  version: 2
  ethernets:
    kni1:
      addresses: [203.0.113.9/24]
    management0:
      dhcp4: definitely
      addresses: [not-a-prefix]
    cni0:
      mtu: invalid
    lo: ignored
    dummy0:
      link-local: [invalid]
  bonds:
    bond0:
      interfaces: [management0]
      unsupported: {invalid: [still, ignored]}
  bridges:
    bridge0:
      interfaces: [bond0]
  routing-policy:
    - from: invalid
  vlans:
    management.200:
      link: management0
      id: invalid
      addresses: invalid
    managed.10:
      link: kni1
      id: 10
      routes:
        - to: invalid
      routing-policy: invalid
`))
	require.NoError(t, err)
	require.Equal(t, []netplan.Link{
		{
			Name:      "kni1",
			Addresses: []netip.Prefix{netip.MustParsePrefix("203.0.113.9/24")},
			LinkLocal: []string{"ipv6"},
		},
		{
			Name:      "managed.10",
			Parent:    "kni1",
			VLANID:    10,
			Addresses: []netip.Prefix{},
			LinkLocal: []string{"ipv6"},
		},
	}, state.Links)
}

// Test_Parse_RejectsDHCP verifies that either DHCP family is rejected on a
// managed base link or VLAN.
func Test_Parse_RejectsDHCP(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "rejects IPv4 DHCP on a base link",
			yaml: "network:\n  version: 2\n  ethernets:\n    kni0:\n      dhcp4: true\n  vlans: {}\n",
			want: `link "kni0": dhcp4 must be disabled`,
		},
		{
			name: "rejects IPv6 DHCP on a VLAN",
			yaml: "network:\n  version: 2\n  ethernets:\n    kni0: {}\n  vlans:\n    vlan20:\n      link: kni0\n      id: 20\n      dhcp6: true\n",
			want: `link "vlan20": dhcp6 must be disabled`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := netplan.Parse([]byte(test.yaml))
			require.EqualError(t, err, test.want)
		})
	}
}

// Test_Parse_RejectsMalformedManagedAddress verifies that a bad managed CIDR
// reports both its link and list position.
func Test_Parse_RejectsMalformedManagedAddress(t *testing.T) {
	_, err := netplan.Parse([]byte(`
network:
  version: 2
  ethernets:
    kni3:
      addresses: [192.0.2.3/24, broken]
  vlans: {}
`))
	require.ErrorContains(t, err, `link "kni3": address 1 "broken"`)
}

// Test_Parse_RejectsBadVLANID verifies that managed VLAN identifiers must be
// inside the netplan VLAN range.
func Test_Parse_RejectsBadVLANID(t *testing.T) {
	for _, vlanID := range []int{0, 4095} {
		t.Run(strconv.Itoa(vlanID), func(t *testing.T) {
			yaml := []byte("network:\n  version: 2\n  ethernets:\n    kni0: {}\n  vlans:\n    vlan:\n      link: kni0\n      id: " +
				strconv.Itoa(vlanID) + "\n")
			_, err := netplan.Parse(yaml)
			require.EqualError(
				t,
				err,
				fmt.Sprintf(`vlan "vlan": ID %d is outside 1..4094`, vlanID),
			)
		})
	}
}

// Test_Parse_RejectsMissingVLANParent verifies that a VLAN without a parent is
// rejected rather than silently omitted from managed state.
func Test_Parse_RejectsMissingVLANParent(t *testing.T) {
	_, err := netplan.Parse([]byte("network:\n  version: 2\n  ethernets: {}\n  vlans:\n    orphan:\n      id: 10\n"))
	require.EqualError(t, err, `vlan "orphan": parent link is required`)
}

// Test_Parse_IgnoresUnmanagedVLANParent verifies that a VLAN attached outside
// the managed KNI set is unrelated state even when its other fields are bad.
func Test_Parse_IgnoresUnmanagedVLANParent(t *testing.T) {
	state, err := netplan.Parse([]byte(`
network:
  version: 2
  ethernets:
    kni0: {}
  vlans:
    management.10:
      link: management0
      id: invalid
      addresses: [invalid]
`))
	require.NoError(t, err)
	require.Equal(t, []netplan.Link{{
		Name:      "kni0",
		Addresses: []netip.Prefix{},
		LinkLocal: []string{"ipv6"},
	}}, state.Links)
}

// Test_Parse_RejectsDuplicateManagedName verifies that a base link and its
// child VLAN cannot occupy the same reconciliation key.
func Test_Parse_RejectsDuplicateManagedName(t *testing.T) {
	_, err := netplan.Parse([]byte(`
network:
  version: 2
  ethernets:
    kni0: {}
  vlans:
    kni0:
      link: kni0
      id: 100
`))
	require.EqualError(t, err, `duplicate managed link name "kni0"`)
}

// Test_Parse_DeterministicOrdering verifies that YAML mapping order does not
// affect the lexicographic managed-link order.
func Test_Parse_DeterministicOrdering(t *testing.T) {
	first, err := netplan.Parse([]byte(`
network:
  version: 2
  ethernets:
    kni2: {}
    kni10: {}
  vlans:
    z-vlan: {link: kni2, id: 20}
    a-vlan: {link: kni10, id: 10}
`))
	require.NoError(t, err)

	second, err := netplan.Parse([]byte(`
network:
  version: 2
  vlans:
    a-vlan: {id: 10, link: kni10}
    z-vlan: {id: 20, link: kni2}
  ethernets:
    kni10: {}
    kni2: {}
`))
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, []string{"a-vlan", "kni10", "kni2", "z-vlan"}, linkNames(first.Links))
}

// Test_Parse_RejectsInvalidLinkLocal verifies that managed link-local families
// are limited to values supported by netplan.
func Test_Parse_RejectsInvalidLinkLocal(t *testing.T) {
	_, err := netplan.Parse([]byte(`
network:
  version: 2
  ethernets:
    kni0:
      link-local: [ipv4, ipx]
  vlans: {}
`))
	require.EqualError(
		t,
		err,
		`link "kni0": link-local value 1 "ipx" is invalid; want ipv4 or ipv6`,
	)
}

// Test_Parse_RequiresCompleteGeneratedRoot verifies that valid YAML fragments
// cannot masquerade as an authoritative empty generated configuration.
func Test_Parse_RequiresCompleteGeneratedRoot(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		errorContains string
	}{
		{
			name:          "missing network",
			yaml:          "unrelated: {}\n",
			errorContains: "network mapping is required",
		},
		{
			name:          "null network",
			yaml:          "network: null\n",
			errorContains: "network mapping is required",
		},
		{
			name:          "missing version",
			yaml:          "network:\n  ethernets: {}\n  vlans: {}\n",
			errorContains: "network.version must be 2",
		},
		{
			name:          "null version",
			yaml:          "network:\n  version: null\n  ethernets: {}\n  vlans: {}\n",
			errorContains: "network.version must be 2",
		},
		{
			name:          "unsupported version",
			yaml:          "network:\n  version: 1\n  ethernets: {}\n  vlans: {}\n",
			errorContains: "network.version must be 2",
		},
		{
			name:          "fractional version",
			yaml:          "network:\n  version: 2.0\n  ethernets: {}\n  vlans: {}\n",
			errorContains: "network.version must be 2",
		},
		{
			name:          "string version",
			yaml:          "network:\n  version: \"2\"\n  ethernets: {}\n  vlans: {}\n",
			errorContains: "network.version must be 2",
		},
		{
			name:          "missing ethernets",
			yaml:          "network:\n  version: 2\n  vlans: {}\n",
			errorContains: "network.ethernets mapping is required",
		},
		{
			name:          "null ethernets",
			yaml:          "network:\n  version: 2\n  ethernets: null\n  vlans: {}\n",
			errorContains: "network.ethernets mapping is required",
		},
		{
			name:          "malformed ethernets",
			yaml:          "network:\n  version: 2\n  ethernets: []\n  vlans: {}\n",
			errorContains: "decode netplan YAML",
		},
		{
			name:          "truncated after ethernets",
			yaml:          "network:\n  version: 2\n  ethernets: {}\n",
			errorContains: "network.vlans mapping is required",
		},
		{
			name:          "null vlans",
			yaml:          "network:\n  version: 2\n  ethernets: {}\n  vlans: null\n",
			errorContains: "network.vlans mapping is required",
		},
		{
			name:          "malformed vlans",
			yaml:          "network:\n  version: 2\n  ethernets: {}\n  vlans: []\n",
			errorContains: "decode netplan YAML",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := netplan.Parse([]byte(test.yaml))
			require.ErrorContains(t, err, test.errorContains)
			require.Equal(t, netplan.State{}, state)
		})
	}
}

// Test_Parse_AcceptsCompleteEmptyGeneratedRoot verifies that an explicitly
// empty generated configuration remains authoritative and can request teardown.
func Test_Parse_AcceptsCompleteEmptyGeneratedRoot(t *testing.T) {
	state, err := netplan.Parse([]byte(`
network:
  version: 2
  ethernets: {}
  vlans: {}
`))
	require.NoError(t, err)
	require.Equal(t, netplan.State{Links: []netplan.Link{}}, state)
}

// Test_Parse_RejectsMalformedYAML verifies that syntax failures retain YAML
// decoder context for callers.
func Test_Parse_RejectsMalformedYAML(t *testing.T) {
	_, err := netplan.Parse([]byte("network:\n  version: 2\n  ethernets: [\n"))
	require.ErrorContains(t, err, "decode netplan YAML")
}

// Test_ParseFile_ReadsState verifies that file parsing has the same typed
// result as parsing the file contents directly.
func Test_ParseFile_ReadsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "50-yanet.yaml")
	data := []byte("network:\n  version: 2\n  ethernets:\n    kni7:\n      addresses: [192.0.2.7/32]\n  vlans: {}\n")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	want, err := netplan.Parse(data)
	require.NoError(t, err)
	got, err := netplan.ParseFile(path)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// linkNames returns reconciliation keys in their current order.
func linkNames(links []netplan.Link) []string {
	names := make([]string, 0, len(links))
	for _, link := range links {
		names = append(names, link.Name)
	}
	return names
}

func boolPointer(value bool) *bool {
	return &value
}
