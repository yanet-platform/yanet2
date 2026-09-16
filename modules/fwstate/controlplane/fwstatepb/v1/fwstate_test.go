package fwstatepb

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
	validMaskPaths := []string{
		"map_name_v4", "map_name_v6",
		"sync_config.dst_ether", "sync_config.dst_addr_unicast",
		"sync_config.port_unicast", "sync_config.src_addr",
		"sync_config.dst_addr_multicast", "sync_config.port_multicast",
		"sync_config.tcp_syn_ack", "sync_config.tcp_syn",
		"sync_config.tcp_fin", "sync_config.tcp",
		"sync_config.udp", "sync_config.default",
		"sync_config.sync_suppress_timeout",
	}
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
			name: "partial destination deferred to merged validation",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				SyncConfig: &SyncConfig{PortMulticast: 9999},
			},
		},
		{
			name: "raw multicast port overflow",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				SyncConfig: &SyncConfig{PortMulticast: 65536},
			},
			message: "sync_config: port_multicast 65536 exceeds maximum allowed value 65535",
		},
		{
			name: "raw unicast port overflow",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				SyncConfig: &SyncConfig{PortUnicast: 65536},
			},
			message: "sync_config: port_unicast 65536 exceeds maximum allowed value 65535",
		},
		{
			name: "raw source address width",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				SyncConfig: &SyncConfig{SrcAddr: &commonpb.IPAddress{Addr: make([]byte, 4)}},
			},
			message: "sync_config: src_addr must be a 16-byte IPv6 address, got 4 bytes",
		},
		{
			name: "raw multicast address width",
			request: &UpdateConfigRequest{
				Name: "fwstate0",
				SyncConfig: &SyncConfig{
					DstAddrMulticast: &commonpb.IPAddress{Addr: make([]byte, 4)},
				},
			},
			message: "sync_config: dst_addr_multicast must be a 16-byte IPv6 address, got 4 bytes",
		},
		{
			name: "raw unicast address width",
			request: &UpdateConfigRequest{
				Name: "fwstate0",
				SyncConfig: &SyncConfig{
					DstAddrUnicast: &commonpb.IPAddress{Addr: make([]byte, 17)},
				},
			},
			message: "sync_config: dst_addr_unicast must be a 16-byte IPv6 address, got 17 bytes",
		},
		{
			name: "raw destination MAC width",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				SyncConfig: &SyncConfig{DstEther: &commonpb.MACAddress{Addr: 1 << 48}},
			},
			message: "sync_config: dst_ether must be an EUI-48 address",
		},
		{
			name: "raw multicast address without port",
			request: &UpdateConfigRequest{
				Name: "fwstate0",
				SyncConfig: &SyncConfig{
					DstAddrMulticast: &commonpb.IPAddress{Addr: make([]byte, 16)},
				},
			},
			message: "sync_config: port_multicast is required with dst_addr_multicast",
		},
		{
			name: "raw unicast address without port",
			request: &UpdateConfigRequest{
				Name: "fwstate0",
				SyncConfig: &SyncConfig{
					DstAddrUnicast: &commonpb.IPAddress{Addr: make([]byte, 16)},
				},
			},
			message: "sync_config: port_unicast is required with dst_addr_unicast",
		},
		{
			name: "clear multicast with endpoint fields",
			request: &UpdateConfigRequest{
				Name:           "fwstate0",
				ClearMulticast: true,
				SyncConfig: &SyncConfig{
					DstAddrMulticast: &commonpb.IPAddress{Addr: make([]byte, 16)},
					PortMulticast:    9999,
				},
			},
			message: "invalid sync endpoint update: clear_multicast cannot be combined with multicast endpoint fields",
		},
		{
			name: "clear unicast with endpoint fields",
			request: &UpdateConfigRequest{
				Name:         "fwstate0",
				ClearUnicast: true,
				SyncConfig:   &SyncConfig{PortUnicast: 9999},
			},
			message: "invalid sync endpoint update: clear_unicast cannot be combined with unicast endpoint fields",
		},
		{
			name: "clear flags with mask",
			request: &UpdateConfigRequest{
				Name:           "fwstate0",
				ClearMulticast: true,
				UpdateMask:     &FieldMask{},
			},
			message: "endpoint clear flags cannot be combined with an update mask",
		},
		{
			name: "unknown mask path",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				UpdateMask: &FieldMask{Paths: []string{"unknown"}},
			},
			message: "unknown update mask path \"unknown\"",
		},
		{
			name: "whole sync config mask path",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				UpdateMask: &FieldMask{Paths: []string{"sync_config"}},
			},
			message: "unknown update mask path \"sync_config\"",
		},
		{
			name: "empty mask path",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				UpdateMask: &FieldMask{Paths: []string{""}},
			},
			message: "unknown update mask path \"\"",
		},
		{
			name: "masked raw value is deferred",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				SyncConfig: &SyncConfig{PortMulticast: 65536},
				UpdateMask: &FieldMask{},
			},
		},
		{
			name: "unselected invalid map name is deferred",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				MapNameV6:  "map\x00name",
				UpdateMask: &FieldMask{Paths: []string{"map_name_v4"}},
			},
		},
		{
			name: "map name contains NUL",
			request: &UpdateConfigRequest{
				Name:      "fwstate0",
				MapNameV4: "map\x00name",
			},
			message: "map_name_v4 must not contain NUL",
		},
		{
			name: "map name reaches byte limit",
			request: &UpdateConfigRequest{
				Name:      "fwstate0",
				MapNameV6: strings.Repeat("a", 80),
			},
			message: "map_name_v6 must be shorter than 80 bytes",
		},
		{
			name: "all supported mask paths",
			request: &UpdateConfigRequest{
				Name:       "fwstate0",
				UpdateMask: &FieldMask{Paths: validMaskPaths},
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

// Test_UpdateConfigRequest_ValidateEndpointClears verifies that each clear
// flag rejects fields belonging to its endpoint and accepts an empty clear.
func Test_UpdateConfigRequest_ValidateEndpointClears(t *testing.T) {
	cases := []struct {
		name    string
		request *UpdateConfigRequest
		message string
	}{
		{name: "nil request", request: nil},
		{name: "no clear flags", request: &UpdateConfigRequest{}},
		{
			name:    "empty multicast clear",
			request: &UpdateConfigRequest{ClearMulticast: true},
		},
		{
			name: "multicast fields with clear",
			request: &UpdateConfigRequest{
				ClearMulticast: true,
				SyncConfig: &SyncConfig{
					DstAddrMulticast: &commonpb.IPAddress{Addr: []byte{1}},
				},
			},
			message: "clear_multicast cannot be combined with multicast endpoint fields",
		},
		{
			name: "unicast fields with clear",
			request: &UpdateConfigRequest{
				ClearUnicast: true,
				SyncConfig:   &SyncConfig{PortUnicast: 1},
			},
			message: "clear_unicast cannot be combined with unicast endpoint fields",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.ValidateEndpointClears()
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
		TcpSynAck: 120e9,
		TcpSyn:    120e9,
		TcpFin:    120e9,
		Tcp:       120e9,
		Udp:       30e9,
		Default:   16e9,
	}
	require.NoError(t, valid.ValidateTimeouts())

	tooLarge := uint64(1) << 48
	invalid := &SyncConfig{
		TcpSynAck: 120e9,
		TcpSyn:    tooLarge,
		TcpFin:    120e9,
		Tcp:       120e9,
		Udp:       tooLarge,
		Default:   16e9,
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
		Tcp:                 cfwstateTTL48Max,
		SyncSuppressTimeout: 1,
	}
	err := overflowing.ValidateTimeouts()
	require.Error(t, err)
	require.Contains(t, err.Error(), "tcp+sync_suppress_timeout")

	// The same timeout with no suppress, or with a window that still fits, is
	// accepted.
	require.NoError(t, (&SyncConfig{Tcp: cfwstateTTL48Max}).ValidateTimeouts())
	require.NoError(t, (&SyncConfig{
		Tcp:                 120e9,
		SyncSuppressTimeout: 8e9,
	}).ValidateTimeouts())
}

// Test_SyncConfig_ValidateFields_RejectsUnusableValues verifies that values
// outside the stored representation and destination addresses without ports
// are rejected.
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
		{name: "boundary port", config: &SyncConfig{PortMulticast: 65535}},
		{
			name:    "port above the boundary",
			config:  &SyncConfig{PortMulticast: 65536},
			message: "port_multicast 65536 exceeds maximum allowed value 65535",
		},
		{
			name:    "unicast port above the boundary",
			config:  &SyncConfig{PortUnicast: 65536},
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
			name: "multicast address without port",
			config: &SyncConfig{
				DstAddrMulticast: addr(16),
			},
			message: "port_multicast is required with dst_addr_multicast",
		},
		{
			name: "short multicast address",
			config: &SyncConfig{
				DstAddrMulticast: addr(4),
				PortMulticast:    1,
			},
			message: "dst_addr_multicast must be a 16-byte IPv6 address, got 4 bytes",
		},
		{
			name: "unicast address without port",
			config: &SyncConfig{
				DstAddrUnicast: addr(16),
			},
			message: "port_unicast is required with dst_addr_unicast",
		},
		{
			name: "long unicast address",
			config: &SyncConfig{
				DstAddrUnicast: addr(17),
				PortUnicast:    1,
			},
			message: "dst_addr_unicast must be a 16-byte IPv6 address, got 17 bytes",
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

// Test_SyncConfig_Validate_DestinationIsAllOrNothing verifies that a
// merged config naming part of the sync destination is rejected.
//
// One naming all of an endpoint, or none of the endpoints, is accepted.
func Test_SyncConfig_Validate_DestinationIsAllOrNothing(t *testing.T) {
	addr := func() *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}
	}
	zeroAddr := func() *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: make([]byte, 16)}
	}

	t.Run("missing multicast address", func(t *testing.T) {
		config := &SyncConfig{SrcAddr: addr(), PortMulticast: 1}

		require.ErrorContains(t, config.Validate(), "dst_addr_multicast")
	})

	t.Run("zero multicast port", func(t *testing.T) {
		config := &SyncConfig{SrcAddr: addr(), DstAddrMulticast: addr()}

		require.ErrorContains(t, config.Validate(), "port_multicast")
	})

	t.Run("only the source address", func(t *testing.T) {
		// Source/MAC values can remain stored after the last destination is
		// removed; without an endpoint they are inert and synchronization is
		// disabled.
		require.NoError(t, (&SyncConfig{SrcAddr: addr()}).Validate())
	})

	t.Run("no destination at all", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{}).Validate())
	})

	// The stored form of an unset address, which every merge over a
	// config without synchronization produces.
	t.Run("zero-filled addresses", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{
			SrcAddr:          zeroAddr(),
			DstAddrMulticast: zeroAddr(),
		}).Validate())
	})

	t.Run("complete destination", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{
			SrcAddr:          addr(),
			DstEther:         &commonpb.MACAddress{Addr: 0x333300000001},
			DstAddrMulticast: addr(),
			PortMulticast:    1,
		}).Validate())
	})

	t.Run("complete unicast destination", func(t *testing.T) {
		require.NoError(t, (&SyncConfig{
			SrcAddr:        addr(),
			DstEther:       &commonpb.MACAddress{Addr: 0x333300000001},
			DstAddrUnicast: addr(),
			PortUnicast:    1,
		}).Validate())
	})

	t.Run("unicast multicast address", func(t *testing.T) {
		require.ErrorContains(t, (&SyncConfig{
			SrcAddr:        addr(),
			DstEther:       &commonpb.MACAddress{Addr: 0x333300000001},
			DstAddrUnicast: &commonpb.IPAddress{Addr: []byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}},
			PortUnicast:    1,
		}).Validate(), "dst_addr_unicast")
	})
}
