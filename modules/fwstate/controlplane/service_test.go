package fwstate_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// Test_FWStateService_UpdateConfig_MaskSetsAllFieldsAndClearsSync verifies
// that every supported path installs its value and an absent selected
// destination clears synchronization.
func Test_FWStateService_UpdateConfig_MaskSetsAllFieldsAndClearsSync(t *testing.T) {
	const name = "masked-all"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, name)
	maps := newFWStateTestMaps(t, agent, name, 1024)
	service := fwstate.NewFWStateService(agent)
	syncConfig := &fwstatepb.SyncConfig{
		SrcAddr: syncTestAddr(), DstAddrMulticast: syncTestAddr(),
		DstEther:       &commonpb.MACAddress{Addr: 0x333300000001},
		DstAddrUnicast: syncTestAddr(), PortUnicast: 10000,
		PortMulticast: 9999, TcpSynAck: 11, TcpSyn: 12, TcpFin: 13,
		Tcp: 14, Udp: 15, Default: 16, SyncSuppressTimeout: 17,
	}
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name: name, MapNameV4: maps.v4Name(), MapNameV6: maps.v6Name(),
		SyncConfig: syncConfig,
		UpdateMask: &fwstatepb.FieldMask{Paths: []string{
			"map_name_v4", "map_name_v6", "sync_config.src_addr",
			"sync_config.dst_ether", "sync_config.dst_addr_unicast", "sync_config.port_unicast",
			"sync_config.dst_addr_multicast", "sync_config.port_multicast",
			"sync_config.tcp_syn_ack", "sync_config.tcp_syn", "sync_config.tcp_fin",
			"sync_config.tcp", "sync_config.udp", "sync_config.default",
			"sync_config.sync_suppress_timeout",
		}},
	})
	stored := showConfig(t, service, name)
	require.Equal(t, maps.v4Name(), stored.GetMapNameV4())
	require.Equal(t, maps.v6Name(), stored.GetMapNameV6())
	require.Equal(t, syncConfig, stored.GetSyncConfig())

	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name: name,
		UpdateMask: &fwstatepb.FieldMask{Paths: []string{
			"sync_config.src_addr", "sync_config.dst_addr_multicast",
			"sync_config.port_multicast", "map_name_v6",
			"sync_config.dst_ether", "sync_config.dst_addr_unicast", "sync_config.port_unicast",
		}},
	})
	stored = showConfig(t, service, name)
	require.Empty(t, stored.GetMapNameV6())
	require.Equal(t, maps.v4Name(), stored.GetMapNameV4())
	require.Zero(t, stored.GetSyncConfig().GetPortMulticast())
	require.Equal(t, make([]byte, 16), stored.GetSyncConfig().GetSrcAddr().GetAddr())
	require.Nil(t, stored.GetSyncConfig().GetDstAddrMulticast())
	require.Nil(t, stored.GetSyncConfig().GetDstAddrUnicast())
	require.Zero(t, stored.GetSyncConfig().GetPortUnicast())
	require.Zero(t, stored.GetSyncConfig().GetDstEther().GetAddr())
	require.Equal(t, syncConfig.GetTcp(), stored.GetSyncConfig().GetTcp())
}

// Test_FWStateService_UpdateConfig_MaskedConcurrentWritesPreserveBothChanges
// verifies that stale unselected values cannot revert another writer.
func Test_FWStateService_UpdateConfig_MaskedConcurrentWritesPreserveBothChanges(t *testing.T) {
	const name = "masked-concurrent"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, name)
	service := fwstate.NewFWStateService(agent)
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name:       name,
		SyncConfig: &fwstatepb.SyncConfig{Tcp: 60e9, Udp: 30e9},
	})

	requests := []*fwstatepb.UpdateConfigRequest{
		{
			Name:       name,
			SyncConfig: &fwstatepb.SyncConfig{Tcp: 120e9, Udp: 30e9},
			UpdateMask: &fwstatepb.FieldMask{Paths: []string{"sync_config.tcp"}},
		},
		{
			Name:       name,
			SyncConfig: &fwstatepb.SyncConfig{Tcp: 60e9, Udp: 45e9},
			UpdateMask: &fwstatepb.FieldMask{Paths: []string{"sync_config.udp"}},
		},
	}
	start := make(chan struct{})
	var writers errgroup.Group
	for _, request := range requests {
		writers.Go(func() error {
			<-start
			_, err := service.UpdateConfig(t.Context(), request)
			return err
		})
	}
	close(start)
	require.NoError(t, writers.Wait())
	stored := showConfig(t, service, name).GetSyncConfig()
	require.EqualValues(t, 120e9, stored.GetTcp())
	require.EqualValues(t, 45e9, stored.GetUdp())
}

// Test_FWStateService_UpdateConfig_MaskClearsValuesAndPreservesUnselectedFields
// verifies that explicit zero and empty values survive construction.
func Test_FWStateService_UpdateConfig_MaskClearsValuesAndPreservesUnselectedFields(t *testing.T) {
	const name = "masked-clear"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, name)
	maps := newFWStateTestMaps(t, agent, name, 1024)
	service := fwstate.NewFWStateService(agent)
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name:       name,
		MapNameV4:  maps.v4Name(),
		MapNameV6:  maps.v6Name(),
		SyncConfig: &fwstatepb.SyncConfig{SyncSuppressTimeout: 8e9},
	})
	before := showConfig(t, service, name)
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name: name,
		// Unselected invalid values must not affect the update.
		MapNameV6:  "not-a-published-map",
		SyncConfig: &fwstatepb.SyncConfig{PortMulticast: 65536},
		UpdateMask: &fwstatepb.FieldMask{Paths: []string{
			"map_name_v4", "sync_config.sync_suppress_timeout", "sync_config.udp",
		}},
	})
	stored := showConfig(t, service, name)
	require.Empty(t, stored.GetMapNameV4())
	require.Equal(t, before.GetMapNameV6(), stored.GetMapNameV6())
	require.Zero(t, stored.GetSyncConfig().GetSyncSuppressTimeout())
	require.Zero(t, stored.GetSyncConfig().GetUdp())
	require.Equal(t, before.GetSyncConfig().GetTcp(), stored.GetSyncConfig().GetTcp())

	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name:       name,
		MapNameV4:  maps.v4Name(),
		SyncConfig: &fwstatepb.SyncConfig{Udp: 99e9},
		UpdateMask: &fwstatepb.FieldMask{},
	})
	require.Equal(t, stored, showConfig(t, service, name))
}

// Test_FWStateService_UpdateConfig_InvalidMaskDoesNotPublish verifies that
// a bad path or selected value leaves the published configuration intact.
func Test_FWStateService_UpdateConfig_InvalidMaskDoesNotPublish(t *testing.T) {
	const name = "masked-invalid"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, name)
	service := fwstate.NewFWStateService(agent)
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name: name, UpdateMask: &fwstatepb.FieldMask{},
	})
	before := showConfig(t, service, name)
	for _, tc := range []struct {
		name       string
		path       string
		syncConfig *fwstatepb.SyncConfig
	}{
		{"unknown field", "unknown", nil},
		{"whole sync config", "sync_config", nil},
		{"unknown sync field", "sync_config.unknown", nil},
		{"unicast port overflow", "sync_config.port_unicast", &fwstatepb.SyncConfig{PortUnicast: 65536}},
		{"MAC overflow", "sync_config.dst_ether", &fwstatepb.SyncConfig{DstEther: &commonpb.MACAddress{Addr: 1 << 48}}},
		{"empty path", "", nil},
		{"port overflow", "sync_config.port_multicast", &fwstatepb.SyncConfig{PortMulticast: 65536}},
		{"partial sync destination", "sync_config.port_multicast", &fwstatepb.SyncConfig{PortMulticast: 9999}},
		{"timeout overflow", "sync_config.udp", &fwstatepb.SyncConfig{Udp: 1 << 48}},
		{"invalid address width", "sync_config.src_addr", &fwstatepb.SyncConfig{SrcAddr: &commonpb.IPAddress{Addr: []byte{1}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.UpdateConfig(t.Context(), &fwstatepb.UpdateConfigRequest{
				Name: name, SyncConfig: tc.syncConfig,
				UpdateMask: &fwstatepb.FieldMask{Paths: []string{"sync_config.tcp", tc.path}},
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Equal(t, before, showConfig(t, service, name))
		})
	}
}

// Test_FWStateService_UpdateConfig_MaskedEndpointsPreserveOtherDestination
// verifies that editing one endpoint leaves the other active, including clears.
func Test_FWStateService_UpdateConfig_MaskedEndpointsPreserveOtherDestination(t *testing.T) {
	const name = "masked-endpoints"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, name)
	service := fwstate.NewFWStateService(agent)
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name: name,
		SyncConfig: &fwstatepb.SyncConfig{
			SrcAddr: syncTestAddr(), DstEther: &commonpb.MACAddress{Addr: 0x333300000001},
			DstAddrMulticast: syncTestAddr(), PortMulticast: 9999,
			DstAddrUnicast: syncTestAddr(), PortUnicast: 10000,
		},
	})
	before := showConfig(t, service, name).GetSyncConfig()
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name: name, SyncConfig: &fwstatepb.SyncConfig{PortUnicast: 10001},
		UpdateMask: &fwstatepb.FieldMask{Paths: []string{"sync_config.port_unicast"}},
	})
	stored := showConfig(t, service, name).GetSyncConfig()
	require.EqualValues(t, 10001, stored.GetPortUnicast())
	require.Equal(t, before.GetDstAddrUnicast(), stored.GetDstAddrUnicast())
	require.Equal(t, before.GetDstAddrMulticast(), stored.GetDstAddrMulticast())
	require.Equal(t, before.GetPortMulticast(), stored.GetPortMulticast())

	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name: name,
		UpdateMask: &fwstatepb.FieldMask{Paths: []string{
			"sync_config.dst_addr_multicast", "sync_config.port_multicast",
		}},
	})
	stored = showConfig(t, service, name).GetSyncConfig()
	require.Nil(t, stored.GetDstAddrMulticast())
	require.Zero(t, stored.GetPortMulticast())
	require.Equal(t, before.GetDstAddrUnicast(), stored.GetDstAddrUnicast())
	require.EqualValues(t, 10001, stored.GetPortUnicast())
}

// Test_FWStateService_UpdateConfig_RejectsMaskWithEndpointClearFlags verifies
// that mixing the two update contracts never publishes a partial change.
func Test_FWStateService_UpdateConfig_RejectsMaskWithEndpointClearFlags(t *testing.T) {
	service := fwstate.NewFWStateService(nil)
	for _, request := range []*fwstatepb.UpdateConfigRequest{
		{Name: "cfg", ClearMulticast: true, UpdateMask: &fwstatepb.FieldMask{}},
		{Name: "cfg", ClearUnicast: true, UpdateMask: &fwstatepb.FieldMask{}},
	} {
		_, err := service.UpdateConfig(t.Context(), request)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}
}

// Test_SyncConfig_ValidateFields verifies that explicit values which would be
// lost or truncated by the C representation are rejected before merging.
func Test_SyncConfig_ValidateFields(t *testing.T) {
	cases := []struct {
		name          string
		portMulticast uint32
		portUnicast   uint32
		dstEther      uint64
		srcAddr       []byte
		dstMulticast  []byte
		dstUnicast    []byte
		wantErr       bool
		wantDetail    string
	}{
		{
			name:          "zero",
			portMulticast: 0,
			wantErr:       false,
		},
		{
			name:          "boundary value",
			portMulticast: 65535,
			wantErr:       false,
		},
		{
			name:          "just above boundary",
			portMulticast: 65536,
			wantErr:       true,
			wantDetail:    "port_multicast",
		},
		{
			name:        "unicast just above boundary",
			portUnicast: 65536,
			wantErr:     true,
			wantDetail:  "port_unicast",
		},
		{
			name:       "MAC outside EUI-48",
			dstEther:   0x100333300000001,
			wantErr:    true,
			wantDetail: "dst_ether",
		},
		{
			name:       "short source address",
			srcAddr:    make([]byte, 4),
			wantErr:    true,
			wantDetail: "src_addr",
		},
		{
			name:         "multicast address without port",
			dstMulticast: make([]byte, 16),
			wantErr:      true,
			wantDetail:   "port_multicast",
		},
		{
			name:          "short multicast address",
			portMulticast: 1,
			dstMulticast:  make([]byte, 4),
			wantErr:       true,
			wantDetail:    "dst_addr_multicast",
		},
		{
			name:       "unicast address without port",
			dstUnicast: make([]byte, 16),
			wantErr:    true,
			wantDetail: "port_unicast",
		},
		{
			name:        "long unicast address",
			portUnicast: 1,
			dstUnicast:  make([]byte, 17),
			wantErr:     true,
			wantDetail:  "dst_addr_unicast",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &fwstatepb.SyncConfig{
				SrcAddr:       &commonpb.IPAddress{Addr: tc.srcAddr},
				PortMulticast: tc.portMulticast,
				PortUnicast:   tc.portUnicast,
				DstEther:      &commonpb.MACAddress{Addr: tc.dstEther},
				DstAddrMulticast: &commonpb.IPAddress{
					Addr: tc.dstMulticast,
				},
				DstAddrUnicast: &commonpb.IPAddress{
					Addr: tc.dstUnicast,
				},
			}

			err := cfg.ValidateFields()
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}

			require.ErrorContains(t, err, tc.wantDetail)
		})
	}
}

// Test_ValidateSyncConfig_DestinationPairs verifies that every configured
// destination is complete and unicast rejects multicast addresses.
func Test_ValidateSyncConfig_DestinationPairs(t *testing.T) {
	newConfig := func() *fwstatepb.SyncConfig {
		return &fwstatepb.SyncConfig{
			SrcAddr:       &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
			DstEther:      &commonpb.MACAddress{Addr: 0x333300000001},
			PortMulticast: 1,
		}
	}

	t.Run("missing multicast address", func(t *testing.T) {
		err := newConfig().Validate()
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "dst_addr_multicast"))
	})

	t.Run("zero multicast port", func(t *testing.T) {
		cfg := newConfig()
		cfg.DstAddrMulticast = &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}
		cfg.PortMulticast = 0

		err := cfg.Validate()
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "port_multicast"))
	})

	t.Run("valid", func(t *testing.T) {
		cfg := newConfig()
		cfg.DstAddrMulticast = &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}

		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("unicast only", func(t *testing.T) {
		cfg := newConfig()
		cfg.PortMulticast = 0
		cfg.DstAddrUnicast = &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}
		cfg.PortUnicast = 2

		require.NoError(t, cfg.Validate())
	})

	t.Run("unicast address without port", func(t *testing.T) {
		cfg := newConfig()
		cfg.PortMulticast = 0
		cfg.DstAddrUnicast = &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}

		err := cfg.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "port_unicast")
	})

	t.Run("unicast port without address", func(t *testing.T) {
		cfg := newConfig()
		cfg.PortMulticast = 0
		cfg.PortUnicast = 2

		err := cfg.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "dst_addr_unicast")
	})

	t.Run("multicast address in unicast destination", func(t *testing.T) {
		cfg := newConfig()
		cfg.PortMulticast = 0
		cfg.DstAddrUnicast = &commonpb.IPAddress{Addr: []byte{0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}
		cfg.PortUnicast = 2

		err := cfg.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "dst_addr_unicast")
	})
}

// Test_UpdateConfigRequest_ValidateEndpointClears verifies that contradictory
// endpoint fields are rejected when an update explicitly clears that endpoint.
func Test_UpdateConfigRequest_ValidateEndpointClears(t *testing.T) {
	request := &fwstatepb.UpdateConfigRequest{
		ClearMulticast: true,
		SyncConfig: &fwstatepb.SyncConfig{
			DstAddrMulticast: syncTestAddr(),
			PortMulticast:    syncTestPort,
		},
	}

	require.ErrorContains(t, request.ValidateEndpointClears(), "clear_multicast")
}

// Test_FWStateService_UpdateConfig_RejectsPartialSyncDestination verifies that
// the service rejects a destination port without its address before it
// attempts to build or publish a C-side module.
func Test_FWStateService_UpdateConfig_RejectsPartialSyncDestination(t *testing.T) {
	service := fwstate.NewFWStateService(nil)

	_, err := service.UpdateConfig(t.Context(), &fwstatepb.UpdateConfigRequest{
		Name: "cfg",
		SyncConfig: &fwstatepb.SyncConfig{
			PortMulticast: 9999,
		},
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, err.Error(), "src_addr")
	require.Contains(t, err.Error(), "dst_addr_multicast")
}

// Test_FWStateService_UpdateConfig_RejectsUnrepresentableMapNames verifies
// that a map name the C object registry cannot carry is refused.
//
// A name too long, or one holding a NUL byte, would be truncated on the
// way in and link an unintended map, so it is refused before any C state
// is built.
func Test_FWStateService_UpdateConfig_RejectsUnrepresentableMapNames(t *testing.T) {
	cases := []struct {
		name   string
		mapV4  string
		mapV6  string
		detail string
	}{
		{
			name:   "v4 name at the C field limit",
			mapV4:  strings.Repeat("a", 80),
			mapV6:  "maps-v6",
			detail: "shorter than 80 bytes",
		},
		{
			name:   "v6 name with embedded NUL",
			mapV4:  "maps-v4",
			mapV6:  "ma\x00ps-v6",
			detail: "must not contain NUL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := fwstate.NewFWStateService(nil)

			_, err := service.UpdateConfig(t.Context(), &fwstatepb.UpdateConfigRequest{
				Name:      "cfg",
				MapNameV4: tc.mapV4,
				MapNameV6: tc.mapV6,
			})
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Contains(t, err.Error(), tc.detail)
		})
	}
}

// syncTestAddr is the address both ends of a test sync destination use;
// only its presence matters to the validation under test.
func syncTestAddr() *commonpb.IPAddress {
	return &commonpb.IPAddress{
		Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
	}
}

const syncTestPort = 9999

// showConfig reads back the stored config, which must exist.
func showConfig(
	testingTB testing.TB,
	service *fwstate.FWStateService,
	name string,
) *fwstatepb.ShowConfigResponse {
	testingTB.Helper()

	response, err := service.ShowConfig(
		testingTB.Context(), &fwstatepb.ShowConfigRequest{Name: name},
	)
	require.NoError(testingTB, err)

	return response
}

// Test_FWStateService_UpdateConfig_CreatesConfigWithoutSyncOrMaps
// verifies that a create naming only the config succeeds.
//
// The installed module links no map and leaves external synchronization
// disabled, while trusted internal events are consumed until a later update.
func Test_FWStateService_UpdateConfig_CreatesConfigWithoutSyncOrMaps(t *testing.T) {
	const configName = "fwstate-bare"

	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-bare")
	service := fwstate.NewFWStateService(agent)

	_, err := service.UpdateConfig(t.Context(), &fwstatepb.UpdateConfigRequest{
		Name: configName,
	})
	require.NoError(t, err)
	require.True(t, hasCPConfig(agent.DPConfig().CPConfigs(), fwstateModuleType, configName))

	stored := showConfig(t, service, configName)
	require.Empty(t, stored.GetMapNameV4())
	require.Empty(t, stored.GetMapNameV6())
	require.Zero(t, stored.GetSyncConfig().GetPortMulticast())
	require.Equal(t, make([]byte, 16), stored.GetSyncConfig().GetSrcAddr().GetAddr())
	require.Nil(t, stored.GetSyncConfig().GetDstAddrMulticast())

	// The timeouts are not part of the sync destination and always carry
	// the defaults, so an unconfigured config is still a usable one.
	require.NotZero(t, stored.GetSyncConfig().GetUdp())
}

// Test_FWStateService_UpdateConfig_AttachesMapsAndSyncLater verifies
// that a later update supplies what a bare create left out.
func Test_FWStateService_UpdateConfig_AttachesMapsAndSyncLater(t *testing.T) {
	const configName = "fwstate-attach-later"

	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-attach-later")
	maps := newFWStateTestMaps(t, agent, "attach-later", 1024)
	service := fwstate.NewFWStateService(agent)

	_, err := service.UpdateConfig(t.Context(), &fwstatepb.UpdateConfigRequest{
		Name: configName,
	})
	require.NoError(t, err)

	_, err = service.UpdateConfig(t.Context(), &fwstatepb.UpdateConfigRequest{
		Name:      configName,
		MapNameV4: maps.v4Name(),
		MapNameV6: maps.v6Name(),
		SyncConfig: &fwstatepb.SyncConfig{
			SrcAddr:          syncTestAddr(),
			DstEther:         &commonpb.MACAddress{Addr: 0x333300000001},
			DstAddrMulticast: syncTestAddr(),
			PortMulticast:    syncTestPort,
		},
	})
	require.NoError(t, err)

	stored := showConfig(t, service, configName)
	require.Equal(t, maps.v4Name(), stored.GetMapNameV4())
	require.Equal(t, maps.v6Name(), stored.GetMapNameV6())
	require.EqualValues(t, syncTestPort, stored.GetSyncConfig().GetPortMulticast())
	require.Equal(t, syncTestAddr().GetAddr(), stored.GetSyncConfig().GetDstAddrMulticast().GetAddr())
}

// Test_FWStateService_UpdateConfig_ClearsLastSyncEndpoint verifies that an
// explicit clear reaches the installed config instead of being treated as an
// omitted destination update.
func Test_FWStateService_UpdateConfig_ClearsLastSyncEndpoint(t *testing.T) {
	const configName = "fwstate-clear-last-endpoint"

	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-clear-last-endpoint")
	maps := newFWStateTestMaps(t, agent, "clear-last-endpoint", 1024)
	service := fwstate.NewFWStateService(agent)

	_, err := service.UpdateConfig(t.Context(), validDeleteTestUpdateRequest(
		configName, maps.v4Name(), maps.v6Name(),
	))
	require.NoError(t, err)

	_, err = service.UpdateConfig(t.Context(), &fwstatepb.UpdateConfigRequest{
		Name:           configName,
		SyncConfig:     &fwstatepb.SyncConfig{},
		ClearMulticast: true,
	})
	require.NoError(t, err)

	stored := showConfig(t, service, configName)
	require.Nil(t, stored.GetSyncConfig().GetDstAddrMulticast())
	require.Zero(t, stored.GetSyncConfig().GetPortMulticast())
	require.Empty(t, stored.GetSyncConfig().GetDstAddrUnicast())
	require.Zero(t, stored.GetSyncConfig().GetPortUnicast())
}

// Test_FWStateService_UpdateConfig_KeepsUnnamedLinksAndUntouchedSync
// verifies that unnamed links and untouched sync stay as they were.
//
// An update naming neither a map nor a sync destination must not
// silently unlink the maps or stop synchronization.
func Test_FWStateService_UpdateConfig_KeepsUnnamedLinksAndUntouchedSync(t *testing.T) {
	const (
		configName = "fwstate-partial-update"
		udpTimeout = 45e9
	)

	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-partial-update")
	maps := newFWStateTestMaps(t, agent, "partial-update", 1024)
	service := fwstate.NewFWStateService(agent)

	_, err := service.UpdateConfig(t.Context(), validDeleteTestUpdateRequest(
		configName, maps.v4Name(), maps.v6Name(),
	))
	require.NoError(t, err)

	// A timeout-only update: no map name, and a sync config naming no
	// part of the destination.
	_, err = service.UpdateConfig(t.Context(), &fwstatepb.UpdateConfigRequest{
		Name:       configName,
		SyncConfig: &fwstatepb.SyncConfig{Udp: udpTimeout},
	})
	require.NoError(t, err)

	stored := showConfig(t, service, configName)
	require.Equal(t, maps.v4Name(), stored.GetMapNameV4())
	require.Equal(t, maps.v6Name(), stored.GetMapNameV6())
	require.EqualValues(t, udpTimeout, stored.GetSyncConfig().GetUdp())
	require.EqualValues(t, syncTestPort, stored.GetSyncConfig().GetPortMulticast())
	require.Equal(t, syncTestAddr().GetAddr(), stored.GetSyncConfig().GetSrcAddr().GetAddr())

	// An update carrying no sync settings at all keeps them too.
	_, err = service.UpdateConfig(t.Context(), &fwstatepb.UpdateConfigRequest{
		Name:      configName,
		MapNameV4: maps.v4Name(),
		MapNameV6: maps.v6Name(),
	})
	require.NoError(t, err)

	stored = showConfig(t, service, configName)
	require.EqualValues(t, udpTimeout, stored.GetSyncConfig().GetUdp())
	require.EqualValues(t, syncTestPort, stored.GetSyncConfig().GetPortMulticast())
	require.Equal(t, syncTestAddr().GetAddr(), stored.GetSyncConfig().GetDstAddrMulticast().GetAddr())
}
