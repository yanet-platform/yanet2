package fwstate_test

import (
	"net/netip"
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

	v6 := func(raw []byte) *commonpb.IPv6Address {
		var value [16]byte
		copy(value[:], raw)
		return commonpb.NewIPv6Address(value)
	}
	syncConfig := &fwstatepb.SyncConfig{
		Endpoints: []*fwstatepb.SyncEndpoint{
			{
				SrcAddr: v6(source),
				DstAddr: v6(multicast),
				Port:    proto.Uint32(4789),
			},
			{
				SrcAddr: v6(source),
				DstAddr: v6(unicast),
				Port:    proto.Uint32(4790),
			},
		},
		Listeners: []*fwstatepb.SyncListenEndpoint{
			{DstAddr: v6(multicast), Port: proto.Uint32(4789)},
			{DstAddr: v6(unicast), Port: proto.Uint32(4790)},
		},
	}

	nativeConfig, err := fwstate.SyncConfigToC(syncConfig)
	require.NoError(t, err)
	got := fwstate.SyncConfigFromC(nativeConfig)

	require.Len(t, got.GetEndpoints(), 2)
	require.Equal(t, netip.MustParseAddr("2001:db8::1"), got.GetEndpoints()[0].GetSrcAddr().ToAddr())
	require.Equal(t, netip.MustParseAddr("ff02::1"), got.GetEndpoints()[0].GetDstAddr().ToAddr())
	require.EqualValues(t, 4789, got.GetEndpoints()[0].GetPort())
	require.Equal(t, netip.MustParseAddr("2001:db8::2"), got.GetEndpoints()[1].GetDstAddr().ToAddr())
	require.EqualValues(t, 4790, got.GetEndpoints()[1].GetPort())

	require.Len(t, got.GetListeners(), 2)
	require.Equal(t, netip.MustParseAddr("ff02::1"), got.GetListeners()[0].GetDstAddr().ToAddr())
	require.EqualValues(t, 4789, got.GetListeners()[0].GetPort())
	require.Equal(t, netip.MustParseAddr("2001:db8::2"), got.GetListeners()[1].GetDstAddr().ToAddr())
	require.EqualValues(t, 4790, got.GetListeners()[1].GetPort())
}

// Test_SyncConfig_SyncSuppressTimeoutRoundTrip verifies that sync suppression
// timing survives the protobuf-to-native round trip.
func Test_SyncConfig_SyncSuppressTimeoutRoundTrip(t *testing.T) {
	const suppress uint64 = 8e9

	syncConfig := &fwstatepb.SyncConfig{SyncSuppressTimeout: proto.Uint64(suppress)}
	native, err := fwstate.SyncConfigToC(syncConfig)
	require.NoError(t, err)
	got := fwstate.SyncConfigFromC(native)
	require.Equal(t, suppress, got.GetSyncSuppressTimeout())
}

// Test_SyncConfig_SyncMTURoundTrip verifies that the sync MTU survives the
// protobuf-to-native round trip.
func Test_SyncConfig_SyncMTURoundTrip(t *testing.T) {
	native, err := fwstate.SyncConfigToC(&fwstatepb.SyncConfig{SyncMtu: proto.Uint32(9000)})
	require.NoError(t, err)
	got := fwstate.SyncConfigFromC(native)

	require.Equal(t, uint32(9000), got.GetSyncMtu())
}

// Test_DefaultSyncConfig_MatchesC verifies that the Go defaults equal the
// config the C module reports before any update.
func Test_DefaultSyncConfig_MatchesC(t *testing.T) {
	want := fwstate.SyncConfigFromC(cfwstate.DefaultSyncConfig())
	got := fwstatepb.DefaultSyncConfig()

	require.True(t, proto.Equal(want, got), "want %v, got %v", want, got)
}
