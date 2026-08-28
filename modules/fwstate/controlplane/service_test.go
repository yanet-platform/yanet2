package fwstate_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

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
