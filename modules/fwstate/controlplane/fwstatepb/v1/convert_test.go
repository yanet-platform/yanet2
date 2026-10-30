package fwstatepb

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_SyncConfig_Merge verifies that an update overwrites only the settings
// it carries, that endpoints replace the stored list wholesale, and that
// no_endpoints removes it.
func Test_SyncConfig_Merge(t *testing.T) {
	multicast := func() *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: []byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}
	}
	source := func() *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: []byte{0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}
	}
	unicast := func() *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: []byte{0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}}
	}
	endpoint := func(dst *commonpb.IPAddress, port uint32) *SyncEndpoint {
		return &SyncEndpoint{SrcAddr: source(), DstAddr: dst, Port: new(port)}
	}
	listener := func(dst *commonpb.IPAddress, port uint32) *SyncListenEndpoint {
		return &SyncListenEndpoint{DstAddr: dst, Port: new(port)}
	}
	stored := func() *SyncConfig {
		return &SyncConfig{
			Endpoints: []*SyncEndpoint{
				endpoint(multicast(), 9999),
				endpoint(unicast(), 10000),
			},
			Listeners: []*SyncListenEndpoint{
				listener(multicast(), 9999),
				listener(unicast(), 10000),
			},
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
			name: "endpoint list replaces the stored list wholesale",
			update: &SyncConfig{
				Endpoints: []*SyncEndpoint{endpoint(multicast(), 7777)},
			},
			want: func() *SyncConfig {
				cfg := stored()
				cfg.Endpoints = []*SyncEndpoint{endpoint(multicast(), 7777)}
				return cfg
			}(),
		},
		{
			name:   "no_endpoints removes the stored list",
			update: &SyncConfig{NoEndpoints: new(true)},
			want: func() *SyncConfig {
				cfg := stored()
				cfg.Endpoints = nil
				cfg.NoEndpoints = new(true)
				return cfg
			}(),
		},
		{
			name: "listener list replaces the stored list wholesale",
			update: &SyncConfig{
				Listeners: []*SyncListenEndpoint{listener(multicast(), 7777)},
			},
			want: func() *SyncConfig {
				cfg := stored()
				cfg.Listeners = []*SyncListenEndpoint{listener(multicast(), 7777)}
				return cfg
			}(),
		},
		{
			name:   "no_listeners removes the stored list",
			update: &SyncConfig{NoListeners: new(true)},
			want: func() *SyncConfig {
				cfg := stored()
				cfg.Listeners = nil
				cfg.NoListeners = new(true)
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
