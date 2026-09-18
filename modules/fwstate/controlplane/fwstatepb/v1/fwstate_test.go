package fwstatepb

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

const cfwstateTTL48Max = cfwstate.TTL48Max

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
			request: &UpdateConfigRequest{Name: "fwstate0", MapNameV4: proto.String("")},
		},
		{
			name:    "map name contains NUL",
			request: &UpdateConfigRequest{Name: "fwstate0", MapNameV4: proto.String("map\x00name")},
			message: "map_name_v4 must not contain NUL",
		},
		{
			name:    "map name reaches byte limit",
			request: &UpdateConfigRequest{Name: "fwstate0", MapNameV6: proto.String(strings.Repeat("a", 80))},
			message: "map_name_v6 must be shorter than 80 bytes",
		},
		{
			name: "sync config delegation",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				SyncConfig: &SyncConfig{PortMulticast: proto.Uint32(65536)},
			},
			message: "sync_config: port_multicast 65536 exceeds maximum allowed value 65535",
		},
		{
			name: "partial destination left to merged validation",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				SyncConfig: &SyncConfig{PortMulticast: proto.Uint32(9999)},
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

func TestValidateSyncConfigTimeouts(t *testing.T) {
	valid := &SyncConfig{
		TcpSynAck: proto.Uint64(120e9),
		TcpSyn:    proto.Uint64(120e9),
		TcpFin:    proto.Uint64(120e9),
		Tcp:       proto.Uint64(120e9),
		Udp:       proto.Uint64(30e9),
		Default:   proto.Uint64(16e9),
	}
	require.NoError(t, valid.ValidateTimeouts())

	tooLarge := uint64(1) << 48
	invalid := &SyncConfig{
		TcpSynAck: proto.Uint64(120e9),
		TcpSyn:    proto.Uint64(tooLarge),
		TcpFin:    proto.Uint64(120e9),
		Tcp:       proto.Uint64(120e9),
		Udp:       proto.Uint64(tooLarge),
		Default:   proto.Uint64(16e9),
	}
	err := invalid.ValidateTimeouts()
	require.Error(t, err)
	require.Contains(t, err.Error(), "tcp_syn")
	require.Contains(t, err.Error(), "udp")
}

// TestValidateSyncConfigTimeoutsSuppressOverflow verifies that a suppress
// window large enough to push an otherwise-valid timeout past the 48-bit
// last_ttl limit is rejected, since the dataplane stores the inflated
// (timeout + suppress) value.
func TestValidateSyncConfigTimeoutsSuppressOverflow(t *testing.T) {
	// A suppress window that by itself fits, but added to the default timeout
	// overflows the 48-bit field.
	overflowing := &SyncConfig{
		Tcp:                 proto.Uint64(cfwstateTTL48Max),
		SyncSuppressTimeout: proto.Uint64(1),
	}
	err := overflowing.ValidateTimeouts()
	require.Error(t, err)
	require.Contains(t, err.Error(), "tcp+sync_suppress_timeout")

	// The same timeout with no suppress, or with a window that still fits, is
	// accepted.
	require.NoError(t, (&SyncConfig{Tcp: proto.Uint64(cfwstateTTL48Max)}).ValidateTimeouts())
	require.NoError(t, (&SyncConfig{
		Tcp:                 proto.Uint64(120e9),
		SyncSuppressTimeout: proto.Uint64(8e9),
	}).ValidateTimeouts())
}

// Test_SyncConfig_ValidateFields_RejectsUnusableValues verifies that values
// outside the stored representation and an address with a zero port are
// rejected.
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
		{name: "boundary port", config: &SyncConfig{PortMulticast: proto.Uint32(65535)}},
		{
			name:    "port above the boundary",
			config:  &SyncConfig{PortMulticast: proto.Uint32(65536)},
			message: "port_multicast 65536 exceeds maximum allowed value 65535",
		},
		{
			name:    "unicast port above the boundary",
			config:  &SyncConfig{PortUnicast: proto.Uint32(65536)},
			message: "port_unicast 65536 exceeds maximum allowed value 65535",
		},
		{
			name:    "MAC outside EUI-48",
			config:  &SyncConfig{DstEther: &commonpb.MACAddress{Addr: 0x100333300000001}},
			message: "dst_ether must be an EUI-48 address",
		},
		{
			name:    "short source address",
			config:  &SyncConfig{SrcAddr: addr(4)},
			message: "src_addr must be a 16-byte IPv6 address, got 4 bytes",
		},
		{
			name:    "short multicast address",
			config:  &SyncConfig{DstAddrMulticast: addr(4), PortMulticast: proto.Uint32(1)},
			message: "dst_addr_multicast must be a 16-byte IPv6 address, got 4 bytes",
		},
		{
			name:    "long unicast address",
			config:  &SyncConfig{DstAddrUnicast: addr(17), PortUnicast: proto.Uint32(1)},
			message: "dst_addr_unicast must be a 16-byte IPv6 address, got 17 bytes",
		},
		{
			name:   "address without port left to merged validation",
			config: &SyncConfig{DstAddrMulticast: addr(16)},
		},
		{
			name:   "zero port without address clears the endpoint",
			config: &SyncConfig{PortMulticast: proto.Uint32(0)},
		},
		{
			name:    "multicast address with zero port",
			config:  &SyncConfig{DstAddrMulticast: addr(16), PortMulticast: proto.Uint32(0)},
			message: "dst_addr_multicast cannot be combined with a zero port_multicast",
		},
		{
			name:    "unicast address with zero port",
			config:  &SyncConfig{DstAddrUnicast: addr(16), PortUnicast: proto.Uint32(0)},
			message: "dst_addr_unicast cannot be combined with a zero port_unicast",
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

// Test_SyncConfig_ValidateMerged_DestinationIsAllOrNothing verifies that a
// merged config naming part of the sync destination is rejected.
//
// One naming all of an endpoint, or none of the endpoints, is accepted.
func Test_SyncConfig_ValidateMerged_DestinationIsAllOrNothing(t *testing.T) {
	addr := func() *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}
	}
	zeroAddr := func() *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: make([]byte, 16)}
	}

	t.Run("missing multicast address", func(t *testing.T) {
		config := &SyncConfig{SrcAddr: addr(), PortMulticast: proto.Uint32(1)}

		require.ErrorContains(t, config.ValidateMerged(), "dst_addr_multicast")
	})

	t.Run("zero multicast port", func(t *testing.T) {
		config := &SyncConfig{SrcAddr: addr(), DstAddrMulticast: addr()}

		require.ErrorContains(t, config.ValidateMerged(), "port_multicast")
	})

	t.Run("only the source address", func(t *testing.T) {
		// Source/MAC values can remain stored after the last destination is
		// removed; without an endpoint they are inert and synchronization is
		// disabled.
		require.NoError(t, (&SyncConfig{SrcAddr: addr()}).ValidateMerged())
	})

	t.Run("no destination at all", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{}).ValidateMerged())
	})

	// The stored form of an unset address, which every merge over a
	// config without synchronization produces.
	t.Run("zero-filled addresses", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{
			SrcAddr:          zeroAddr(),
			DstAddrMulticast: zeroAddr(),
		}).ValidateMerged())
	})

	t.Run("complete destination", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{
			SrcAddr:          addr(),
			DstEther:         &commonpb.MACAddress{Addr: 0x333300000001},
			DstAddrMulticast: addr(),
			PortMulticast:    proto.Uint32(1),
		}).ValidateMerged())
	})

	t.Run("complete unicast destination", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{
			SrcAddr:        addr(),
			DstEther:       &commonpb.MACAddress{Addr: 0x333300000001},
			DstAddrUnicast: addr(),
			PortUnicast:    proto.Uint32(1),
		}).ValidateMerged())
	})

	t.Run("unicast multicast address", func(t *testing.T) {
		require.ErrorContains(t, (&SyncConfig{
			SrcAddr:        addr(),
			DstEther:       &commonpb.MACAddress{Addr: 0x333300000001},
			DstAddrUnicast: &commonpb.IPAddress{Addr: []byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}},
			PortUnicast:    proto.Uint32(1),
		}).ValidateMerged(), "dst_addr_unicast")
	})
}
