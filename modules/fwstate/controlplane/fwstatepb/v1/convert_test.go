package fwstatepb

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

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
