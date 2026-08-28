package fwstatepb

import (
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

const cfwstateTTL48Max = cfwstate.TTL48Max

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

// Test_SyncConfig_ValidateFields_RejectsUnusableValues verifies that a
// value stated in a form the config cannot store is rejected.
//
// A port past the uint16 range and an address of a width other than an
// IPv6 one fail; the values that stand for "leave this as it is" pass.
func Test_SyncConfig_ValidateFields_RejectsUnusableValues(t *testing.T) {
	addr := func(size int) *commonpb.IPAddress {
		return &commonpb.IPAddress{Addr: make([]byte, size)}
	}

	cases := []struct {
		name    string
		config  *SyncConfig
		wantErr string
	}{
		{
			name:   "nothing set",
			config: &SyncConfig{},
		},
		{
			name:   "boundary port",
			config: &SyncConfig{PortMulticast: 65535},
		},
		{
			name:    "port above the boundary",
			config:  &SyncConfig{PortMulticast: 65536},
			wantErr: "port_multicast",
		},
		{
			name:    "ipv4-width source address",
			config:  &SyncConfig{SrcAddr: addr(4)},
			wantErr: "src_addr",
		},
		{
			name:    "truncated multicast address",
			config:  &SyncConfig{DstAddrMulticast: addr(15)},
			wantErr: "dst_addr_multicast",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.ValidateFields()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.ErrorContains(t, err, tc.wantErr)
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
