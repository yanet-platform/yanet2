package fwstate_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
	fwstatepb "github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// Test_SyncConfig_EndpointsRoundTrip verifies that the whole endpoint list
// survives the protobuf-to-native round trip.
func Test_SyncConfig_EndpointsRoundTrip(t *testing.T) {
	source := []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	multicast := []byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	unicast := []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}

	syncConfig := &fwstatepb.SyncConfig{
		Endpoints: []*fwstatepb.SyncEndpoint{
			{
				SrcAddr: &commonpb.IPAddress{Addr: source},
				DstAddr: &commonpb.IPAddress{Addr: multicast},
				Port:    proto.Uint32(4789),
			},
			{
				SrcAddr: &commonpb.IPAddress{Addr: source},
				DstAddr: &commonpb.IPAddress{Addr: unicast},
				Port:    proto.Uint32(4790),
			},
		},
	}

	nativeConfig := fwstate.SyncConfigToC(syncConfig)
	got := fwstate.SyncConfigFromC(nativeConfig)

	require.Len(t, got.GetEndpoints(), 2)
	require.Equal(t, source, got.GetEndpoints()[0].GetSrcAddr().Addr)
	require.Equal(t, multicast, got.GetEndpoints()[0].GetDstAddr().Addr)
	require.EqualValues(t, 4789, got.GetEndpoints()[0].GetPort())
	require.Equal(t, unicast, got.GetEndpoints()[1].GetDstAddr().Addr)
	require.EqualValues(t, 4790, got.GetEndpoints()[1].GetPort())
}

// Test_SyncConfig_DisabledEndpointsDroppedRoundTrip verifies that conversion
// skips the endpoints a zero port disables.
func Test_SyncConfig_DisabledEndpointsDroppedRoundTrip(t *testing.T) {
	source := [16]byte{0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	multicast := [16]byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	unicast := [16]byte{0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	tests := []struct {
		name      string
		config    cfwstate.SyncConfig
		wantCount int
	}{
		{
			name: "enabled endpoint kept, disabled one dropped",
			config: cfwstate.SyncConfig{Endpoints: []cfwstate.SyncEndpoint{
				{SrcAddr: source, DstAddr: multicast, Port: 9999},
				{SrcAddr: source, DstAddr: unicast},
			}},
			wantCount: 1,
		},
		{
			name: "all endpoints disabled",
			config: cfwstate.SyncConfig{Endpoints: []cfwstate.SyncEndpoint{
				{SrcAddr: source, DstAddr: unicast},
			}},
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			syncConfig := fwstate.SyncConfigFromC(tc.config)

			require.Len(t, syncConfig.GetEndpoints(), tc.wantCount)
			roundTrip := fwstate.SyncConfigToC(syncConfig)
			require.Len(t, roundTrip.Endpoints, tc.wantCount)
			if tc.wantCount > 0 {
				require.Equal(t, uint16(9999), roundTrip.Endpoints[0].Port)
				require.Equal(t, multicast, roundTrip.Endpoints[0].DstAddr)
			}
		})
	}
}

// Test_SyncConfig_SyncSuppressTimeoutRoundTrip verifies that sync suppression
// timing survives the protobuf-to-native round trip.
func Test_SyncConfig_SyncSuppressTimeoutRoundTrip(t *testing.T) {
	const suppress uint64 = 8e9

	syncConfig := &fwstatepb.SyncConfig{SyncSuppressTimeout: proto.Uint64(suppress)}
	got := fwstate.SyncConfigFromC(fwstate.SyncConfigToC(syncConfig))
	require.Equal(t, suppress, got.GetSyncSuppressTimeout())
}

// Test_SyncConfig_SyncMTURoundTrip verifies that the sync MTU survives the
// protobuf-to-native round trip.
func Test_SyncConfig_SyncMTURoundTrip(t *testing.T) {
	got := fwstate.SyncConfigFromC(fwstate.SyncConfigToC(&fwstatepb.SyncConfig{SyncMtu: proto.Uint32(9000)}))

	require.Equal(t, uint32(9000), got.GetSyncMtu())
}
