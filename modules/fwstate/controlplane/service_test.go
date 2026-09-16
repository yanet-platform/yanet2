package fwstate_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// Test_FWStateService_UpdateConfig_SetsAllFieldsAndClearsSync verifies that
// every carried field installs its value and zero ports clear synchronization.
func Test_FWStateService_UpdateConfig_SetsAllFieldsAndClearsSync(t *testing.T) {
	const name = "update-all"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, name)
	maps := newFWStateTestMaps(t, agent, name, 1024)
	service := fwstate.NewFWStateService(agent)
	syncConfig := &fwstatepb.SyncConfig{
		SrcAddr: syncTestAddr(), DstAddrMulticast: syncTestAddr(),
		DstEther:       &commonpb.MACAddress{Addr: 0x333300000001},
		DstAddrUnicast: syncTestAddr(), PortUnicast: proto.Uint32(10000),
		PortMulticast: proto.Uint32(9999), TcpSynAck: proto.Uint64(11), TcpSyn: proto.Uint64(12),
		TcpFin: proto.Uint64(13), Tcp: proto.Uint64(14), Udp: proto.Uint64(15),
		Default: proto.Uint64(16), SyncSuppressTimeout: proto.Uint64(17),
	}
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name:       name,
		MapNameV4:  proto.String(maps.v4Name()),
		MapNameV6:  proto.String(maps.v6Name()),
		SyncConfig: syncConfig,
	})
	stored := showConfig(t, service, name)
	require.Equal(t, maps.v4Name(), stored.GetMapNameV4())
	require.Equal(t, maps.v6Name(), stored.GetMapNameV6())
	require.True(t, proto.Equal(syncConfig, stored.GetSyncConfig()), "got %v", stored.GetSyncConfig())

	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name:      name,
		MapNameV6: proto.String(""),
		SyncConfig: &fwstatepb.SyncConfig{
			PortMulticast: proto.Uint32(0),
			PortUnicast:   proto.Uint32(0),
		},
	})
	stored = showConfig(t, service, name)
	require.Empty(t, stored.GetMapNameV6())
	require.Equal(t, maps.v4Name(), stored.GetMapNameV4())
	require.Zero(t, stored.GetSyncConfig().GetPortMulticast())
	require.Nil(t, stored.GetSyncConfig().GetDstAddrMulticast())
	require.Nil(t, stored.GetSyncConfig().GetDstAddrUnicast())
	require.Zero(t, stored.GetSyncConfig().GetPortUnicast())
	require.Equal(t, syncConfig.GetTcp(), stored.GetSyncConfig().GetTcp())
}

// Test_FWStateService_UpdateConfig_ConcurrentWritesPreserveBothChanges
// verifies that fields one writer leaves out cannot revert another writer.
func Test_FWStateService_UpdateConfig_ConcurrentWritesPreserveBothChanges(t *testing.T) {
	const name = "update-concurrent"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, name)
	service := fwstate.NewFWStateService(agent)
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name:       name,
		SyncConfig: &fwstatepb.SyncConfig{Tcp: proto.Uint64(60e9), Udp: proto.Uint64(30e9)},
	})

	requests := []*fwstatepb.UpdateConfigRequest{
		{Name: name, SyncConfig: &fwstatepb.SyncConfig{Tcp: proto.Uint64(120e9)}},
		{Name: name, SyncConfig: &fwstatepb.SyncConfig{Udp: proto.Uint64(45e9)}},
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

// Test_FWStateService_UpdateConfig_ZeroValuesOverwriteAndAbsentFieldsKeep
// verifies that explicit zero and empty values survive construction while
// absent fields keep the stored values.
func Test_FWStateService_UpdateConfig_ZeroValuesOverwriteAndAbsentFieldsKeep(t *testing.T) {
	const name = "update-zero"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, name)
	maps := newFWStateTestMaps(t, agent, name, 1024)
	service := fwstate.NewFWStateService(agent)
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name:       name,
		MapNameV4:  proto.String(maps.v4Name()),
		MapNameV6:  proto.String(maps.v6Name()),
		SyncConfig: &fwstatepb.SyncConfig{SyncSuppressTimeout: proto.Uint64(8e9)},
	})
	before := showConfig(t, service, name)
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name:      name,
		MapNameV4: proto.String(""),
		SyncConfig: &fwstatepb.SyncConfig{
			SyncSuppressTimeout: proto.Uint64(0),
			Udp:                 proto.Uint64(0),
		},
	})
	stored := showConfig(t, service, name)
	require.Empty(t, stored.GetMapNameV4())
	require.Equal(t, before.GetMapNameV6(), stored.GetMapNameV6())
	require.Zero(t, stored.GetSyncConfig().GetSyncSuppressTimeout())
	require.Zero(t, stored.GetSyncConfig().GetUdp())
	require.Equal(t, before.GetSyncConfig().GetTcp(), stored.GetSyncConfig().GetTcp())

	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{Name: name})
	require.True(t, proto.Equal(stored, showConfig(t, service, name)))
}

// Test_FWStateService_UpdateConfig_EndpointsPreserveOtherDestination verifies
// that editing one endpoint leaves the other active, including clears.
func Test_FWStateService_UpdateConfig_EndpointsPreserveOtherDestination(t *testing.T) {
	const name = "update-endpoints"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, name)
	service := fwstate.NewFWStateService(agent)
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name: name,
		SyncConfig: &fwstatepb.SyncConfig{
			SrcAddr: syncTestAddr(), DstEther: &commonpb.MACAddress{Addr: 0x333300000001},
			DstAddrMulticast: syncTestAddr(), PortMulticast: proto.Uint32(9999),
			DstAddrUnicast: syncTestAddr(), PortUnicast: proto.Uint32(10000),
		},
	})
	before := showConfig(t, service, name).GetSyncConfig()
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name:       name,
		SyncConfig: &fwstatepb.SyncConfig{PortUnicast: proto.Uint32(10001)},
	})
	stored := showConfig(t, service, name).GetSyncConfig()
	require.EqualValues(t, 10001, stored.GetPortUnicast())
	require.Equal(t, before.GetDstAddrUnicast().GetAddr(), stored.GetDstAddrUnicast().GetAddr())
	require.Equal(t, before.GetDstAddrMulticast().GetAddr(), stored.GetDstAddrMulticast().GetAddr())
	require.Equal(t, before.GetPortMulticast(), stored.GetPortMulticast())

	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{
		Name:       name,
		SyncConfig: &fwstatepb.SyncConfig{PortMulticast: proto.Uint32(0)},
	})
	stored = showConfig(t, service, name).GetSyncConfig()
	require.Nil(t, stored.GetDstAddrMulticast())
	require.Zero(t, stored.GetPortMulticast())
	require.Equal(t, before.GetDstAddrUnicast().GetAddr(), stored.GetDstAddrUnicast().GetAddr())
	require.EqualValues(t, 10001, stored.GetPortUnicast())
}

// Test_FWStateService_UpdateConfig_InvalidMergedConfigDoesNotPublish verifies
// that state-dependent validation runs inside the writer callback and leaves
// the previous generation published.
func Test_FWStateService_UpdateConfig_InvalidMergedConfigDoesNotPublish(t *testing.T) {
	const name = "update-invalid-merged"
	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, name)
	service := fwstate.NewFWStateService(agent)
	publishConfig(t, service, &fwstatepb.UpdateConfigRequest{Name: name})
	before := showConfig(t, service, name)

	cases := []struct {
		name       string
		syncConfig *fwstatepb.SyncConfig
	}{
		{
			name:       "port without a stored address",
			syncConfig: &fwstatepb.SyncConfig{PortMulticast: proto.Uint32(9999)},
		},
		{
			name:       "address without a stored port",
			syncConfig: &fwstatepb.SyncConfig{DstAddrMulticast: syncTestAddr()},
		},
		{
			name:       "timeout overflow",
			syncConfig: &fwstatepb.SyncConfig{Udp: proto.Uint64(1 << 48)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.UpdateConfig(t.Context(), &fwstatepb.UpdateConfigRequest{
				Name:       name,
				SyncConfig: tc.syncConfig,
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.True(t, proto.Equal(before, showConfig(t, service, name)))
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
			PortMulticast: proto.Uint32(1),
		}
	}

	t.Run("missing multicast address", func(t *testing.T) {
		err := newConfig().ValidateMerged()
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "dst_addr_multicast"))
	})

	t.Run("zero multicast port", func(t *testing.T) {
		cfg := newConfig()
		cfg.DstAddrMulticast = &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}
		cfg.PortMulticast = proto.Uint32(0)

		err := cfg.ValidateMerged()
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "port_multicast"))
	})

	t.Run("valid", func(t *testing.T) {
		cfg := newConfig()
		cfg.DstAddrMulticast = &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}

		err := cfg.ValidateMerged()
		require.NoError(t, err)
	})

	t.Run("unicast only", func(t *testing.T) {
		cfg := newConfig()
		cfg.PortMulticast = proto.Uint32(0)
		cfg.DstAddrUnicast = &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}
		cfg.PortUnicast = proto.Uint32(2)

		require.NoError(t, cfg.ValidateMerged())
	})

	t.Run("unicast address without port", func(t *testing.T) {
		cfg := newConfig()
		cfg.PortMulticast = proto.Uint32(0)
		cfg.DstAddrUnicast = &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}

		err := cfg.ValidateMerged()
		require.Error(t, err)
		require.Contains(t, err.Error(), "port_unicast")
	})

	t.Run("unicast port without address", func(t *testing.T) {
		cfg := newConfig()
		cfg.PortMulticast = proto.Uint32(0)
		cfg.PortUnicast = proto.Uint32(2)

		err := cfg.ValidateMerged()
		require.Error(t, err)
		require.Contains(t, err.Error(), "dst_addr_unicast")
	})

	t.Run("multicast address in unicast destination", func(t *testing.T) {
		cfg := newConfig()
		cfg.PortMulticast = proto.Uint32(0)
		cfg.DstAddrUnicast = &commonpb.IPAddress{Addr: []byte{0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}
		cfg.PortUnicast = proto.Uint32(2)

		err := cfg.ValidateMerged()
		require.Error(t, err)
		require.Contains(t, err.Error(), "dst_addr_unicast")
	})
}

// Test_FWStateService_UpdateConfig_RejectsPartialSyncDestination verifies that
// the service rejects a destination port without its address before it
// attempts to build or publish a C-side module.
func Test_FWStateService_UpdateConfig_RejectsPartialSyncDestination(t *testing.T) {
	service := fwstate.NewFWStateService(nil)

	_, err := service.UpdateConfig(t.Context(), &fwstatepb.UpdateConfigRequest{
		Name: "cfg",
		SyncConfig: &fwstatepb.SyncConfig{
			PortMulticast: proto.Uint32(9999),
		},
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, err.Error(), "src_addr")
	require.Contains(t, err.Error(), "dst_addr_multicast")
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
		MapNameV4: proto.String(maps.v4Name()),
		MapNameV6: proto.String(maps.v6Name()),
		SyncConfig: &fwstatepb.SyncConfig{
			SrcAddr:          syncTestAddr(),
			DstEther:         &commonpb.MACAddress{Addr: 0x333300000001},
			DstAddrMulticast: syncTestAddr(),
			PortMulticast:    proto.Uint32(syncTestPort),
		},
	})
	require.NoError(t, err)

	stored := showConfig(t, service, configName)
	require.Equal(t, maps.v4Name(), stored.GetMapNameV4())
	require.Equal(t, maps.v6Name(), stored.GetMapNameV6())
	require.EqualValues(t, syncTestPort, stored.GetSyncConfig().GetPortMulticast())
	require.Equal(t, syncTestAddr().GetAddr(), stored.GetSyncConfig().GetDstAddrMulticast().GetAddr())
}

// Test_FWStateService_UpdateConfig_ClearsLastSyncEndpoint verifies that a zero
// port disables the last configured endpoint in the installed config.
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
		Name:       configName,
		SyncConfig: &fwstatepb.SyncConfig{PortMulticast: proto.Uint32(0)},
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
		SyncConfig: &fwstatepb.SyncConfig{Udp: proto.Uint64(udpTimeout)},
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
		MapNameV4: proto.String(maps.v4Name()),
		MapNameV6: proto.String(maps.v6Name()),
	})
	require.NoError(t, err)

	stored = showConfig(t, service, configName)
	require.EqualValues(t, udpTimeout, stored.GetSyncConfig().GetUdp())
	require.EqualValues(t, syncTestPort, stored.GetSyncConfig().GetPortMulticast())
	require.Equal(t, syncTestAddr().GetAddr(), stored.GetSyncConfig().GetDstAddrMulticast().GetAddr())
}

// Test_FWStateService_ShowConfig_MissingConfig verifies that a missing config
// is NotFound unless the request tolerates it with an empty response.
func Test_FWStateService_ShowConfig_MissingConfig(t *testing.T) {
	service := fwstate.NewFWStateService(nil)

	_, err := service.ShowConfig(t.Context(), &fwstatepb.ShowConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))

	response, err := service.ShowConfig(t.Context(), &fwstatepb.ShowConfigRequest{
		Name:         "missing",
		OkIfNotFound: true,
	})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Empty(t, response.GetName())
}
