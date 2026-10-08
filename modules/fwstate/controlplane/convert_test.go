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

// Test_SyncConfig_EmitFieldsRoundTrip verifies that both destinations survive
// the protobuf-to-native round trip.
func Test_SyncConfig_EmitFieldsRoundTrip(t *testing.T) {
	const portMulticast uint32 = 4789
	const portUnicast uint32 = 4790
	destinationEther := [6]byte{0x33, 0x33, 0, 0, 0, 1}
	destinationUnicast := []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}

	syncConfig := &fwstatepb.SyncConfig{
		SrcAddr:          &commonpb.IPAddress{Addr: make([]byte, 16)},
		DstEther:         commonpb.NewMACAddressEUI48(destinationEther),
		DstAddrMulticast: &commonpb.IPAddress{Addr: make([]byte, 16)},
		PortMulticast:    proto.Uint32(portMulticast),
		DstAddrUnicast:   &commonpb.IPAddress{Addr: destinationUnicast},
		PortUnicast:      proto.Uint32(portUnicast),
	}

	nativeConfig := fwstate.SyncConfigToC(syncConfig)
	got := fwstate.SyncConfigFromC(nativeConfig)

	require.Equal(t, portMulticast, got.GetPortMulticast())
	require.Equal(t, destinationEther, got.DstEther.EUI48())
	require.Equal(t, destinationUnicast, got.DstAddrUnicast.Addr)
	require.Equal(t, portUnicast, got.GetPortUnicast())
}

// Test_SyncConfig_SingleDestinationRoundTrip verifies that conversion retains
// destination addresses only when their ports are nonzero.
func Test_SyncConfig_SingleDestinationRoundTrip(t *testing.T) {
	multicast := [16]byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	unicast := [16]byte{0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	tests := []struct {
		name          string
		config        cfwstate.SyncConfig
		wantMulticast bool
		wantUnicast   bool
	}{
		{
			name: "multicast only",
			config: cfwstate.SyncConfig{
				DstAddrMulticast: multicast,
				PortMulticast:    9999,
				DstAddrUnicast:   unicast,
			},
			wantMulticast: true,
		},
		{
			name: "unicast only",
			config: cfwstate.SyncConfig{
				DstAddrMulticast: multicast,
				DstAddrUnicast:   unicast,
				PortUnicast:      10000,
			},
			wantUnicast: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			syncConfig := fwstate.SyncConfigFromC(tc.config)

			require.Equal(t, tc.wantMulticast, syncConfig.DstAddrMulticast != nil)
			require.Equal(t, tc.wantUnicast, syncConfig.DstAddrUnicast != nil)
			roundTrip := fwstate.SyncConfigToC(syncConfig)
			if tc.wantMulticast {
				require.Equal(t, multicast, roundTrip.DstAddrMulticast)
				require.Equal(t, uint16(9999), roundTrip.PortMulticast)
			} else {
				require.Zero(t, roundTrip.DstAddrMulticast)
				require.Zero(t, roundTrip.PortMulticast)
			}
			if tc.wantUnicast {
				require.Equal(t, unicast, roundTrip.DstAddrUnicast)
				require.Equal(t, uint16(10000), roundTrip.PortUnicast)
			} else {
				require.Zero(t, roundTrip.DstAddrUnicast)
				require.Zero(t, roundTrip.PortUnicast)
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
