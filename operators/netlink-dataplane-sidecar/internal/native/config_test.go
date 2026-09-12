package native_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/native"
)

// Test_Config_StrictSchema verifies that unsupported keys, shapes and scalar
// types cannot disappear during typed decoding of the embedded mapping.
func Test_Config_StrictSchema(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{name: "sequence root", data: "[]"},
		{name: "scalar root", data: "false"},
		{name: "network wrapper", data: "network: {}"},
		{name: "version", data: "version: 2"},
		{name: "renderer", data: "renderer: networkd"},
		{name: "bridge", data: "bridges: {}"},
		{name: "bond", data: "bonds: {}"},
		{name: "tunnel", data: "tunnels: {}"},
		{name: "section null", data: "ethernets: null"},
		{name: "section sequence", data: "vlans: []"},
		{name: "link null", data: "dummy-devices: {dummy0: null}"},
		{name: "link sequence", data: "ethernets: {kni0: []}"},
		{name: "numeric link name", data: "ethernets: {123: {}}"},
		{name: "duplicate section", data: "ethernets: {}\nethernets: {}"},
		{name: "duplicate link", data: "ethernets: {kni0: {}, kni0: {}}"},
		{name: "duplicate field", data: "ethernets: {kni0: {mtu: 1500, mtu: 9000}}"},
		{name: "routes", data: "ethernets: {kni0: {routes: []}}"},
		{name: "routing policy", data: "ethernets: {kni0: {routing-policy: []}}"},
		{name: "administrative state", data: "ethernets: {kni0: {up: true}}"},
		{name: "unknown field", data: "ethernets: {kni0: {typo: 1}}"},
		{name: "non VLAN empty parent", data: "ethernets: {kni0: {link: ''}}"},
		{name: "non VLAN ID zero", data: "dummy-devices: {dummy0: {id: 0}}"},
		{name: "missing VLAN ID", data: "vlans: {v0: {link: kni0}}"},
		{name: "missing VLAN parent", data: "vlans: {v0: {id: 0}}"},
		{name: "empty VLAN parent", data: "vlans: {v0: {id: 0, link: ''}}"},
		{name: "numeric VLAN parent", data: "vlans: {v0: {id: 0, link: 123}}"},
		{name: "quoted MTU", data: "ethernets: {kni0: {mtu: '1500'}}"},
		{name: "fractional MTU", data: "ethernets: {kni0: {mtu: 1500.0}}"},
		{name: "hex MTU", data: "ethernets: {kni0: {mtu: 0x1000}}"},
		{name: "oversized MTU", data: "ethernets: {kni0: {mtu: 2147483648}}"},
		{name: "numeric address", data: "ethernets: {kni0: {addresses: [123]}}"},
		{name: "address scalar", data: "ethernets: {kni0: {addresses: '192.0.2.1/24'}}"},
		{name: "IPv4 link local", data: "ethernets: {kni0: {link-local: [ipv4]}}"},
		{name: "duplicate link local", data: "ethernets: {kni0: {link-local: [ipv6, ipv6]}}"},
		{name: "string RA", data: "ethernets: {kni0: {accept-ra: 'false'}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config native.Config
			require.Error(t, yaml.Unmarshal([]byte(tc.data), &config))
		})
	}
	for _, tc := range []struct {
		field string
		value string
	}{
		{field: "mtu", value: "1500"},
		{field: "addresses", value: "[192.0.2.1/24]"},
		{field: "link-local", value: "[ipv6]"},
		{field: "accept-ra", value: "false"},
		{field: "dhcp4", value: "false"},
		{field: "dhcp6", value: "false"},
		{field: "id", value: "0"},
		{field: "link", value: "kni0"},
	} {
		t.Run("null "+tc.field, func(t *testing.T) {
			defaults := ""
			if tc.field != "id" {
				defaults += "id: 0, "
			}
			if tc.field != "link" {
				defaults += "link: kni0, "
			}
			topology := "ethernets: {kni0: {}}\nvlans: {v0: {" + defaults + tc.field + ": %s}}"
			var config native.Config
			require.Error(t, yaml.Unmarshal(fmt.Appendf(nil, topology, "null"), &config))
			require.NoError(t, yaml.Unmarshal(fmt.Appendf(nil, topology, tc.value), &config))
		})
	}
}

// Test_Config_DisabledDHCP verifies that DHCP declarations are strictly boolean
// and false-only for every managed kind, independent of RA policy.
func Test_Config_DisabledDHCP(t *testing.T) {
	for _, topology := range []string{
		"ethernets: {kni0: {%s}}", "ethernets: {lo: {%s}}",
		"ethernets: {kni0: {}}, vlans: {v0: {id: 0, link: kni0, %s}}",
		"dummy-devices: {dummy0: {%s}}",
	} {
		for _, field := range []string{"dhcp4", "dhcp6"} {
			for _, value := range []string{"false", "true", "null", "'false'", "'no'", "no", "0", "[]"} {
				t.Run(topology+"/"+field+"/"+value, func(t *testing.T) {
					var config native.Config
					err := yaml.Unmarshal([]byte("{"+fmt.Sprintf(topology, field+": "+value+", accept-ra: true")+"}"), &config)
					if value == "false" {
						require.NoError(t, err)
						var omitted native.Config
						require.NoError(t, yaml.Unmarshal([]byte("{"+fmt.Sprintf(topology, "accept-ra: true")+"}"), &omitted))
						require.Equal(t, omitted, config)
					} else {
						require.Error(t, err)
					}
				})
			}
		}
	}
}
