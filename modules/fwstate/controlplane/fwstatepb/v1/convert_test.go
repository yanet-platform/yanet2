package fwstatepb

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// Test_SyncConfig_EmitFieldsRoundTrip verifies that both destinations survive
// the Pb->C->Pb conversion.
func Test_SyncConfig_EmitFieldsRoundTrip(t *testing.T) {
	const portMulticast uint32 = 4789
	const portUnicast uint32 = 4790
	dstEther := [6]byte{0x33, 0x33, 0, 0, 0, 1}
	dstUnicast := []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}

	pb := &SyncConfig{
		SrcAddr:          &commonpb.IPAddress{Addr: make([]byte, 16)},
		DstEther:         commonpb.NewMACAddressEUI48(dstEther),
		DstAddrMulticast: &commonpb.IPAddress{Addr: make([]byte, 16)},
		PortMulticast:    proto.Uint32(portMulticast),
		DstAddrUnicast:   &commonpb.IPAddress{Addr: dstUnicast},
		PortUnicast:      proto.Uint32(portUnicast),
	}

	cCfg := pb.ToC()
	got := FromCSyncConfig(cCfg)

	require.Equal(t, portMulticast, got.GetPortMulticast())
	require.Equal(t, dstEther, got.DstEther.EUI48())
	require.Equal(t, dstUnicast, got.DstAddrUnicast.Addr)
	require.Equal(t, portUnicast, got.GetPortUnicast())
}

// Test_SyncConfig_SingleDestinationRoundTrip verifies that conversion retains
// only destination pairs whose address and port are both configured.
func Test_SyncConfig_SingleDestinationRoundTrip(t *testing.T) {
	multicast := [16]byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	unicast := [16]byte{0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	tests := []struct {
		name          string
		cfg           cfwstate.SyncConfig
		wantMulticast bool
		wantUnicast   bool
	}{
		{
			name: "multicast only",
			cfg: cfwstate.SyncConfig{
				DstAddrMulticast: multicast,
				PortMulticast:    9999,
				DstAddrUnicast:   unicast,
			},
			wantMulticast: true,
		},
		{
			name: "unicast only",
			cfg: cfwstate.SyncConfig{
				DstAddrMulticast: multicast,
				DstAddrUnicast:   unicast,
				PortUnicast:      10000,
			},
			wantUnicast: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pb := FromCSyncConfig(tc.cfg)

			require.Equal(t, tc.wantMulticast, pb.DstAddrMulticast != nil)
			require.Equal(t, tc.wantUnicast, pb.DstAddrUnicast != nil)
			roundTrip := pb.ToC()
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

// Test_SyncConfig_Merge verifies that an update overwrites only the settings
// it carries and that a zero port clears its endpoint address.
func Test_SyncConfig_Merge(t *testing.T) {
	multicast := func() *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: []byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}
	}
	unicast := func() *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: []byte{0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}}
	}
	stored := func() *SyncConfig {
		return &SyncConfig{
			DstAddrMulticast:    multicast(),
			PortMulticast:       proto.Uint32(9999),
			DstAddrUnicast:      unicast(),
			PortUnicast:         proto.Uint32(10000),
			Tcp:                 proto.Uint64(120e9),
			SyncSuppressTimeout: proto.Uint64(8e9),
		}
	}

	cases := []struct {
		name   string
		update *SyncConfig
		want   *SyncConfig
	}{
		{
			name:   "nil update keeps every setting",
			update: nil,
			want:   stored(),
		},
		{
			name:   "empty update keeps every setting",
			update: &SyncConfig{},
			want:   stored(),
		},
		{
			name:   "explicit zero timeout overwrites the stored one",
			update: &SyncConfig{SyncSuppressTimeout: proto.Uint64(0)},
			want: func() *SyncConfig {
				cfg := stored()
				cfg.SyncSuppressTimeout = proto.Uint64(0)
				return cfg
			}(),
		},
		{
			name:   "address replaces the stored address and keeps the port",
			update: &SyncConfig{DstAddrMulticast: unicast()},
			want: func() *SyncConfig {
				cfg := stored()
				cfg.DstAddrMulticast = unicast()
				return cfg
			}(),
		},
		{
			name:   "zero multicast port clears the multicast endpoint only",
			update: &SyncConfig{PortMulticast: proto.Uint32(0)},
			want: func() *SyncConfig {
				cfg := stored()
				cfg.DstAddrMulticast = nil
				cfg.PortMulticast = proto.Uint32(0)
				return cfg
			}(),
		},
		{
			name:   "zero unicast port clears the unicast endpoint only",
			update: &SyncConfig{PortUnicast: proto.Uint32(0)},
			want: func() *SyncConfig {
				cfg := stored()
				cfg.DstAddrUnicast = nil
				cfg.PortUnicast = proto.Uint32(0)
				return cfg
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := stored()
			cfg.Merge(tc.update)
			require.True(t, proto.Equal(tc.want, cfg), "want %v, got %v", tc.want, cfg)
		})
	}
}

// TestSyncSuppressTimeoutRoundTrip verifies that sync_suppress_timeout
// survives the Pb->C->Pb round-trip.
func TestSyncSuppressTimeoutRoundTrip(t *testing.T) {
	const suppress uint64 = 8e9

	pb := &SyncConfig{SyncSuppressTimeout: proto.Uint64(suppress)}
	got := FromCSyncConfig(pb.ToC())
	require.Equal(t, suppress, got.GetSyncSuppressTimeout())
}

// Test_SyncConfig_SyncMTURoundTrip verifies that the sync MTU survives the
// Pb->C->Pb conversion.
func Test_SyncConfig_SyncMTURoundTrip(t *testing.T) {
	got := FromCSyncConfig((&SyncConfig{SyncMtu: proto.Uint32(9000)}).ToC())

	require.Equal(t, uint32(9000), got.GetSyncMtu())
}
