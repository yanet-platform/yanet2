package fwstatepb

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_ShowConfigRequest_Validate verifies that a show request must name a
// configuration, including when the protobuf receiver is nil.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *ShowConfigRequest
		message string
	}{
		{name: "empty name", request: &ShowConfigRequest{}, message: "name is required"},
		{name: "named request", request: &ShowConfigRequest{Name: "fwstate0"}},
		{name: "nil request", request: nil, message: "name is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_DeleteConfigRequest_Validate verifies that a delete request must name a
// configuration, including when the protobuf receiver is nil.
func Test_DeleteConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *DeleteConfigRequest
		message string
	}{
		{name: "empty name", request: &DeleteConfigRequest{}, message: "name is required"},
		{name: "named request", request: &DeleteConfigRequest{Name: "fwstate0"}},
		{name: "nil request", request: nil, message: "name is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_UpdateConfigRequest_Validate verifies that request-local FWState rules
// run without applying state-dependent merged configuration checks.
func Test_UpdateConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *UpdateConfigRequest
		message string
	}{
		{name: "empty name", request: &UpdateConfigRequest{}, message: "name is required"},
		{name: "nil request", request: nil, message: "name is required"},
		{
			name:    "named request without optional fields",
			request: &UpdateConfigRequest{Name: "fwstate0"},
		},
		{
			name:    "empty map name unlinks",
			request: &UpdateConfigRequest{Name: "fwstate0", MapNameV4: new("")},
		},
		{
			name:    "map name contains NUL",
			request: &UpdateConfigRequest{Name: "fwstate0", MapNameV4: new("map\x00name")},
			message: "map_name_v4 must not contain NUL",
		},
		{
			name:    "map name reaches byte limit",
			request: &UpdateConfigRequest{Name: "fwstate0", MapNameV6: new(strings.Repeat("a", 80))},
			message: "map_name_v6 must be shorter than 80 bytes",
		},
		{
			name: "sync config delegation",
			request: &UpdateConfigRequest{
				Name: "fwstate0",
				SyncConfig: &SyncConfig{Endpoints: []*SyncEndpoint{
					{Port: proto.Uint32(65536)},
				}},
			},
			message: "sync_config: endpoints[0].port must be in 1..65535, got 65536",
		},
		{
			name: "timeout overflow",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				SyncConfig: &SyncConfig{Udp: proto.Uint64(1 << 48)},
			},
			message: "sync_config: udp 281474976710656 exceeds maximum allowed value 281474976710655",
		},
		{
			name: "sync mtu below one frame",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				SyncConfig: &SyncConfig{SyncMtu: proto.Uint32(103)},
			},
			message: "sync_config: sync_mtu 103 is below the minimum 104",
		},
		{
			name: "suppression timeout overflow",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				SyncConfig: &SyncConfig{SyncSuppressTimeout: proto.Uint64(1 << 48)},
			},
			message: "sync_config: sync_suppress_timeout 281474976710656 exceeds maximum allowed value 281474976710655",
		},
		{
			name: "port without addresses left to merged validation",
			request: &UpdateConfigRequest{
				Name: "fwstate0",
				SyncConfig: &SyncConfig{Endpoints: []*SyncEndpoint{
					{Port: proto.Uint32(9999)},
				}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_SyncConfig_ValidateTimeouts verifies that request timeout fields fit
// individually while merged timeouts include the suppression window.
func Test_SyncConfig_ValidateTimeouts(t *testing.T) {
	cases := []struct {
		name          string
		config        *SyncConfig
		fieldsMessage string
		mergedMessage string
	}{
		{
			name: "all timeouts fit",
			config: &SyncConfig{
				TcpSynAck: proto.Uint64(120e9),
				TcpSyn:    proto.Uint64(120e9),
				TcpFin:    proto.Uint64(120e9),
				Tcp:       proto.Uint64(120e9),
				Udp:       proto.Uint64(30e9),
				Default:   proto.Uint64(16e9),
			},
		},
		{
			name: "first overflowing timeout is reported",
			config: &SyncConfig{
				TcpSyn: proto.Uint64(1 << 48),
				Udp:    proto.Uint64(1 << 48),
			},
			fieldsMessage: "tcp_syn 281474976710656 exceeds maximum allowed value 281474976710655",
			mergedMessage: "timeout values exceed 48-bit limit: [tcp_syn+sync_suppress_timeout udp+sync_suppress_timeout]",
		},
		{
			name: "effective timeout includes suppression",
			config: &SyncConfig{
				Tcp:                 new(TTL48Max),
				SyncSuppressTimeout: proto.Uint64(1),
			},
			mergedMessage: "timeout values exceed 48-bit limit: [tcp+sync_suppress_timeout]",
		},
		{
			name:   "maximum timeout without suppression fits",
			config: &SyncConfig{Tcp: new(TTL48Max)},
		},
		{
			name: "timeout with fitting suppression fits",
			config: &SyncConfig{
				Tcp:                 proto.Uint64(120e9),
				SyncSuppressTimeout: proto.Uint64(8e9),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fieldError := tc.config.ValidateFields()
			if tc.fieldsMessage == "" {
				require.NoError(t, fieldError)
			} else {
				require.EqualError(t, fieldError, tc.fieldsMessage)
			}

			mergedError := tc.config.ValidateMerged()
			if tc.mergedMessage == "" {
				require.NoError(t, mergedError)
				return
			}
			require.EqualError(t, mergedError, tc.mergedMessage)
		})
	}
}

// Test_SyncConfig_ValidateFields_RejectsUnusableValues verifies that values
// outside the stored representation and incomplete or conflicting endpoint
// lists are rejected.
func Test_SyncConfig_ValidateFields_RejectsUnusableValues(t *testing.T) {
	addr := func(size int) *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: make([]byte, size)}
	}

	cases := []struct {
		name    string
		config  *SyncConfig
		message string
	}{
		{name: "nothing set", config: &SyncConfig{}},
		{
			name: "boundary port",
			config: &SyncConfig{Endpoints: []*SyncEndpoint{
				{Port: proto.Uint32(65535)},
			}},
		},
		{
			name: "port above the boundary",
			config: &SyncConfig{Endpoints: []*SyncEndpoint{
				{Port: proto.Uint32(65536)},
			}},
			message: "endpoints[0].port must be in 1..65535, got 65536",
		},
		{
			name: "zero port",
			config: &SyncConfig{Endpoints: []*SyncEndpoint{
				{Port: proto.Uint32(0)},
			}},
			message: "endpoints[0].port must be in 1..65535, got 0",
		},
		{
			name: "short source address",
			config: &SyncConfig{Endpoints: []*SyncEndpoint{
				{SrcAddr: addr(4), Port: proto.Uint32(1)},
			}},
			message: "endpoints[0].src_addr must be a 16-byte IPv6 address, got 4 bytes",
		},
		{
			name: "short destination address",
			config: &SyncConfig{Endpoints: []*SyncEndpoint{
				{DstAddr: addr(4), Port: proto.Uint32(1)},
			}},
			message: "endpoints[0].dst_addr must be a 16-byte IPv6 address, got 4 bytes",
		},
		{
			name: "no_endpoints combined with endpoints",
			config: &SyncConfig{
				NoEndpoints: new(true),
				Endpoints:   []*SyncEndpoint{{Port: proto.Uint32(1)}},
			},
			message: "no_endpoints cannot be combined with endpoints",
		},
		{
			name:   "no_endpoints alone",
			config: &SyncConfig{NoEndpoints: new(true)},
		},
		{
			name: "endpoint list above the bound",
			config: &SyncConfig{
				Endpoints: func() []*SyncEndpoint {
					endpoints := make([]*SyncEndpoint, MaxSyncEndpoints+1)
					for idx := range endpoints {
						endpoints[idx] = &SyncEndpoint{Port: proto.Uint32(1)}
					}
					return endpoints
				}(),
			},
			message: "at most 8 sync endpoints are supported, got 9",
		},
		{
			name: "addresses without a port",
			config: &SyncConfig{Endpoints: []*SyncEndpoint{
				{SrcAddr: addr(16), DstAddr: addr(16)},
			}},
			message: "endpoints[0].port must be in 1..65535, got 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.ValidateFields()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}

			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_SyncConfig_ValidateMerged_EndpointsAreAllOrNothing verifies that a
// merged config naming part of an endpoint is rejected.
//
// Endpoints naming everything, or an empty list, are accepted.
func Test_SyncConfig_ValidateMerged_EndpointsAreAllOrNothing(t *testing.T) {
	addr := func() *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}
	}
	zeroAddr := func() *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: make([]byte, 16)}
	}

	t.Run("missing source address", func(t *testing.T) {
		config := &SyncConfig{Endpoints: []*SyncEndpoint{
			{DstAddr: addr(), Port: proto.Uint32(1)},
		}}

		require.ErrorContains(t, config.ValidateMerged(), "endpoints[0].src_addr")
	})

	t.Run("missing destination address", func(t *testing.T) {
		config := &SyncConfig{Endpoints: []*SyncEndpoint{
			{SrcAddr: addr(), Port: proto.Uint32(1)},
		}}

		require.ErrorContains(t, config.ValidateMerged(), "endpoints[0].dst_addr")
	})

	t.Run("zero port", func(t *testing.T) {
		config := &SyncConfig{Endpoints: []*SyncEndpoint{
			{SrcAddr: addr(), DstAddr: addr()},
		}}

		require.ErrorContains(t, config.ValidateMerged(), "endpoints[0].port")
	})

	t.Run("no endpoint at all", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{}).ValidateMerged())
	})

	// The stored form of an unset address, which every merge over a
	// config without synchronization produces.
	t.Run("zero-filled addresses name no endpoint", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{
			Endpoints: []*SyncEndpoint{},
		}).ValidateMerged())
	})

	t.Run("zero-filled address bytes of a carried endpoint", func(t *testing.T) {
		config := &SyncConfig{Endpoints: []*SyncEndpoint{
			{SrcAddr: zeroAddr(), DstAddr: zeroAddr(), Port: proto.Uint32(1)},
		}}

		require.ErrorContains(t, config.ValidateMerged(), "endpoints[0].src_addr")
	})

	t.Run("complete multicast endpoint", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{
			Endpoints: []*SyncEndpoint{
				{
					SrcAddr: addr(),
					DstAddr: &commonpb.IPAddress{Addr: []byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}},
					Port:    proto.Uint32(1),
				},
			},
		}).ValidateMerged())
	})

	t.Run("complete unicast endpoint", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{
			Endpoints: []*SyncEndpoint{
				{SrcAddr: addr(), DstAddr: addr(), Port: proto.Uint32(1)},
			},
		}).ValidateMerged())
	})

	t.Run("several complete endpoints", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{
			Endpoints: []*SyncEndpoint{
				{SrcAddr: addr(), DstAddr: addr(), Port: proto.Uint32(1)},
				{SrcAddr: addr(), DstAddr: addr(), Port: proto.Uint32(2)},
			},
		}).ValidateMerged())
	})

	t.Run("second endpoint incomplete", func(t *testing.T) {
		config := &SyncConfig{
			Endpoints: []*SyncEndpoint{
				{SrcAddr: addr(), DstAddr: addr(), Port: proto.Uint32(1)},
				{SrcAddr: addr(), Port: proto.Uint32(2)},
			},
		}

		require.ErrorContains(t, config.ValidateMerged(), "endpoints[1].dst_addr")
	})
}

// Test_SyncConfig_ValidateFields_SyncMTU verifies that request MTU values
// accept zero and any value holding one frame that fits 16 bits.
func Test_SyncConfig_ValidateFields_SyncMTU(t *testing.T) {
	cases := []struct {
		name    string
		mtu     uint32
		message string
	}{
		{name: "zero selects the default", mtu: 0},
		{name: "one frame", mtu: 104},
		{name: "boundary", mtu: 65535},
		{name: "below one frame", mtu: 103, message: "sync_mtu 103 is below the minimum 104"},
		{name: "above the boundary", mtu: 65536, message: "sync_mtu 65536 exceeds maximum allowed value 65535"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&SyncConfig{SyncMtu: new(tc.mtu)}).ValidateFields()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}
