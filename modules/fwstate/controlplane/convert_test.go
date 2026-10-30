package fwstate_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/xnetip"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
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
		Listeners: []*fwstatepb.SyncListenEndpoint{
			{DstAddr: &commonpb.IPAddress{Addr: multicast}, Port: proto.Uint32(4789)},
			{DstAddr: &commonpb.IPAddress{Addr: unicast}, Port: proto.Uint32(4790)},
		},
	}

	nativeConfig, err := fwstate.SyncConfigToC(syncConfig)
	require.NoError(t, err)
	got := fwstate.SyncConfigFromC(nativeConfig)

	require.Len(t, got.GetEndpoints(), 2)
	require.Equal(t, source, got.GetEndpoints()[0].GetSrcAddr().Addr)
	require.Equal(t, multicast, got.GetEndpoints()[0].GetDstAddr().Addr)
	require.EqualValues(t, 4789, got.GetEndpoints()[0].GetPort())
	require.Equal(t, unicast, got.GetEndpoints()[1].GetDstAddr().Addr)
	require.EqualValues(t, 4790, got.GetEndpoints()[1].GetPort())

	require.Len(t, got.GetListeners(), 2)
	require.Equal(t, multicast, got.GetListeners()[0].GetDstAddr().Addr)
	require.EqualValues(t, 4789, got.GetListeners()[0].GetPort())
	require.Equal(t, unicast, got.GetListeners()[1].GetDstAddr().Addr)
	require.EqualValues(t, 4790, got.GetListeners()[1].GetPort())
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
			roundTrip, err := fwstate.SyncConfigToC(syncConfig)
			require.NoError(t, err)
			require.Len(t, roundTrip.Endpoints, tc.wantCount)
			if tc.wantCount > 0 {
				require.Equal(t, uint16(9999), roundTrip.Endpoints[0].Port)
				require.Equal(t, multicast, roundTrip.Endpoints[0].DstAddr)
			}
		})
	}
}

// Test_SyncConfig_SyncRulesRoundTrip verifies that the scope rules
// survive the protobuf-to-native round trip as addr-then-mask pairs and
// inclusive port spans.
func Test_SyncConfig_SyncRulesRoundTrip(t *testing.T) {
	network4, err := xnetip.Network4FromCIDR(netip.MustParseAddr("10.1.0.0"), 24)
	require.NoError(t, err)
	network6, err := xnetip.Network6FromCIDR(netip.MustParseAddr("2001:db8::"), 32)
	require.NoError(t, err)

	syncConfig := &fwstatepb.SyncConfig{
		Rules: []*fwstatepb.SyncRule{{
			Net4S: []*commonpb.IPv4Network{
				commonpb.NewIPv4NetworkFrom4(network4),
			},
			Net6S: []*commonpb.IPv6Network{
				commonpb.NewIPv6NetworkFrom6(network6),
			},
			SrcPorts: []*filterpb.PortRange{
				{From: 100, To: 200},
				{From: 9999, To: 9999},
			},
		}},
	}

	native, err := fwstate.SyncConfigToC(syncConfig)
	require.NoError(t, err)
	got := fwstate.SyncConfigFromC(native)

	require.Len(t, got.GetRules(), 1)
	rule := got.GetRules()[0]
	require.Len(t, rule.GetNet4S(), 1)
	gotNetwork4, err := rule.GetNet4S()[0].ToNetwork4()
	require.NoError(t, err)
	require.Equal(t, "10.1.0.0/24", gotNetwork4.String())
	require.Len(t, rule.GetNet6S(), 1)
	gotNetwork6, err := rule.GetNet6S()[0].ToNetwork6()
	require.NoError(t, err)
	require.Equal(t, "2001:db8::/32", gotNetwork6.String())
	require.Len(t, rule.GetSrcPorts(), 2)
	require.Equal(t, uint32(100), rule.GetSrcPorts()[0].GetFrom())
	require.Equal(t, uint32(200), rule.GetSrcPorts()[0].GetTo())
	require.Equal(t, uint32(9999), rule.GetSrcPorts()[1].GetFrom())
	require.Equal(t, uint32(9999), rule.GetSrcPorts()[1].GetTo())
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
