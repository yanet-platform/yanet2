package framework_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/yncp"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// dataplaneYAML mirrors the parts of the dataplane configuration these tests
// assert on. The dataplane itself parses this YAML in C, so there is no shared
// Go schema to decode into.
type dataplaneYAML struct {
	Dataplane struct {
		Devices []struct {
			PortName string `yaml:"port_name"`
			Workers  []struct {
				CoreID int `yaml:"core_id"`
			} `yaml:"workers"`
		} `yaml:"devices"`
		Connections []struct {
			SrcDevice string `yaml:"src_device"`
			DstDevice string `yaml:"dst_device"`
		} `yaml:"connections"`
	} `yaml:"dataplane"`
}

func decodeDataplaneConfig(t *testing.T, opts framework.DataplaneOptions) dataplaneYAML {
	t.Helper()

	var cfg dataplaneYAML
	require.NoError(t, yaml.Unmarshal([]byte(framework.DataplaneConfig(opts)), &cfg))

	return cfg
}

// TestDataplaneConfig_DefaultDevices pins the baseline device set and its
// connection pairing, the shape every test package sharing the default
// baseline snapshot boots with.
func TestDataplaneConfig_DefaultDevices(t *testing.T) {
	cfg := decodeDataplaneConfig(t, framework.DataplaneOptions{})

	require.Len(t, cfg.Dataplane.Devices, 2)
	require.Equal(t, "01:00.0", cfg.Dataplane.Devices[0].PortName)
	require.Equal(t, "virtio_user_kni0", cfg.Dataplane.Devices[1].PortName)
	require.Len(t, cfg.Dataplane.Connections, 2)
}

// TestDataplaneConfig_ExtraDevices verifies that an extra device is declared
// and connected in both directions to every other device.
//
// The connection half is what the assertion is really for: an extra device
// with no connection to the port a worker polls silently drops every packet
// routed to it and increments remote_tx_drops, which reads as a broken test
// rather than a missing pairing.
func TestDataplaneConfig_ExtraDevices(t *testing.T) {
	cfg := decodeDataplaneConfig(t, framework.DataplaneOptions{
		ExtraDevices: []string{"02:00.0"},
	})

	names := make([]string, 0, len(cfg.Dataplane.Devices))
	for _, device := range cfg.Dataplane.Devices {
		names = append(names, device.PortName)
		require.Len(t, device.Workers, 1, "device %q must declare exactly one worker", device.PortName)
	}
	require.Equal(t, []string{"01:00.0", "virtio_user_kni0", "02:00.0"}, names)

	pairs := make(map[string]bool, len(cfg.Dataplane.Connections))
	for _, connection := range cfg.Dataplane.Connections {
		pairs[connection.SrcDevice+"->"+connection.DstDevice] = true
	}
	require.Len(t, pairs, len(cfg.Dataplane.Connections), "connections must not repeat a pair")
	for _, src := range names {
		for _, dst := range names {
			if src == dst {
				continue
			}
			require.True(t, pairs[src+"->"+dst], "missing connection %s -> %s", src, dst)
		}
	}
}

// TestDefaultControlplaneConfig_NoUnknownKeys guards the functional harness's
// baseline controlplane YAML against a key that matches no field in
// yncp.Config, the same check the director now applies at startup.
func TestDefaultControlplaneConfig_NoUnknownKeys(t *testing.T) {
	require.NoError(t, xcfg.CheckKnownKeys[yncp.Config]([]byte(framework.DefaultControlplaneConfig())))
}

// TestDefaultControlplaneConfig_StartsIntendedSet decodes the harness's
// baseline controlplane YAML through the same path the director's main()
// drives, and asserts every module and device the YAML lists actually
// starts, since an absent modules/devices key now means the module or
// device is not started at all.
func TestDefaultControlplaneConfig_StartsIntendedSet(t *testing.T) {
	cfg := &yncp.Config{}
	cfg.Default()

	err := xcfg.Decode([]byte(framework.DefaultControlplaneConfig()), cfg, xcfg.WithKnownFields())
	require.NoError(t, err)

	require.NotNil(t, cfg.Modules.Route.Unwrap())
	require.NotNil(t, cfg.Modules.RouteMPLS.Unwrap())
	require.NotNil(t, cfg.Modules.Decap.Unwrap())
	require.NotNil(t, cfg.Modules.DSCP.Unwrap())
	require.NotNil(t, cfg.Modules.Forward.Unwrap())
	require.NotNil(t, cfg.Modules.NAT64.Unwrap())
	require.NotNil(t, cfg.Modules.Pdump.Unwrap())
	require.NotNil(t, cfg.Modules.ACL.Unwrap())
	require.NotNil(t, cfg.Modules.Mirror.Unwrap())
	require.NotNil(t, cfg.Modules.Blackhole.Unwrap())

	require.NotNil(t, cfg.Devices.Plain.Unwrap())
	require.NotNil(t, cfg.Devices.Vlan.Unwrap())
	// Trafgen is deliberately absent from the harness config, matching the
	// shipped default: the harness never relied on it.
	require.Nil(t, cfg.Devices.Trafgen.Unwrap())
}
