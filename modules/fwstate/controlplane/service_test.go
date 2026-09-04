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

// Test_FWStateService_UpdateConfig_RejectsPartialSyncDestination verifies
// that a request naming part of the sync destination is refused.
//
// The InvalidArgument it fails with names every part left out, and the
// agent is not touched before it does.
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
// The installed module links no map and matches no sync packet, leaving
// both to a later update.
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
	require.Equal(t, make([]byte, 16), stored.GetSyncConfig().GetDstAddrMulticast().GetAddr())

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
