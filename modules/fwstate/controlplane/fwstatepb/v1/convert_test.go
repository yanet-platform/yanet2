package fwstatepb

import (
	"testing"

	"github.com/stretchr/testify/require"
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
		PortMulticast:    portMulticast,
		DstAddrUnicast:   &commonpb.IPAddress{Addr: dstUnicast},
		PortUnicast:      portUnicast,
	}

	cCfg := pb.ToC()
	got := FromCSyncConfig(cCfg)

	require.Equal(t, portMulticast, got.PortMulticast)
	require.Equal(t, dstEther, got.DstEther.EUI48())
	require.Equal(t, dstUnicast, got.DstAddrUnicast.Addr)
	require.Equal(t, portUnicast, got.PortUnicast)
}

// Test_SyncConfig_ToCWithDefaults_DestinationsReplaceBothPairs verifies that
// omitting one destination from an explicit destination set disables it.
func Test_SyncConfig_ToCWithDefaults_DestinationsReplaceBothPairs(t *testing.T) {
	current := cfwstate.SyncConfig{
		DstAddrMulticast: [16]byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		PortMulticast:    9999,
		DstAddrUnicast:   [16]byte{0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2},
		PortUnicast:      10000,
	}
	pb := &SyncConfig{
		DstAddrUnicast: &commonpb.IPAddress{Addr: current.DstAddrUnicast[:]},
		PortUnicast:    uint32(current.PortUnicast),
	}

	cfg := pb.ToCWithDefaults(current)

	require.Equal(t, [16]byte{}, cfg.DstAddrMulticast)
	require.Zero(t, cfg.PortMulticast)
	require.Equal(t, current.DstAddrUnicast, cfg.DstAddrUnicast)
	require.Equal(t, current.PortUnicast, cfg.PortUnicast)
}

// Test_SyncConfig_ToCWithDefaults_MulticastOnlyClearsUnicast verifies that a
// multicast-only replacement does not restore the current unicast endpoint.
func Test_SyncConfig_ToCWithDefaults_MulticastOnlyClearsUnicast(t *testing.T) {
	current := cfwstate.SyncConfig{
		DstAddrMulticast: [16]byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		PortMulticast:    9999,
		DstAddrUnicast:   [16]byte{0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2},
		PortUnicast:      10000,
	}
	pb := &SyncConfig{
		DstAddrMulticast: &commonpb.IPAddress{Addr: current.DstAddrMulticast[:]},
		PortMulticast:    uint32(current.PortMulticast),
	}

	cfg := pb.ToCWithDefaults(current)

	require.Equal(t, current.DstAddrMulticast, cfg.DstAddrMulticast)
	require.Equal(t, current.PortMulticast, cfg.PortMulticast)
	require.Zero(t, cfg.DstAddrUnicast)
	require.Zero(t, cfg.PortUnicast)
}

// Test_SyncConfig_ToCWithDefaultsAndClears_ClearsSelectedEndpoint verifies
// that an explicit clear can disable one endpoint while retaining the other.
func Test_SyncConfig_ToCWithDefaultsAndClears_ClearsSelectedEndpoint(t *testing.T) {
	current := cfwstate.SyncConfig{
		DstAddrMulticast: [16]byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		PortMulticast:    9999,
		DstAddrUnicast:   [16]byte{0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2},
		PortUnicast:      10000,
	}

	cfg := (&SyncConfig{}).ToCWithDefaultsAndClears(current, true, false)

	require.Zero(t, cfg.DstAddrMulticast)
	require.Zero(t, cfg.PortMulticast)
	require.Equal(t, current.DstAddrUnicast, cfg.DstAddrUnicast)
	require.Equal(t, current.PortUnicast, cfg.PortUnicast)
}

// Test_SyncConfig_ToCWithDefaultsAndClears_ClearsWithoutSyncConfig verifies
// that a clear-only request can disable an endpoint while retaining settings.
func Test_SyncConfig_ToCWithDefaultsAndClears_ClearsWithoutSyncConfig(t *testing.T) {
	current := cfwstate.SyncConfig{
		DstAddrMulticast:    [16]byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		PortMulticast:       9999,
		SyncSuppressTimeout: 8e9,
	}

	cfg := (*SyncConfig)(nil).ToCWithDefaultsAndClears(current, true, false)

	require.Zero(t, cfg.DstAddrMulticast)
	require.Zero(t, cfg.PortMulticast)
	require.Equal(t, current.SyncSuppressTimeout, cfg.SyncSuppressTimeout)
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

func TestSyncConfig_ToCWithDefaults(t *testing.T) {
	t.Run("omitted", func(t *testing.T) {
		current := cfwstate.SyncConfig{
			SrcAddr:             [16]byte{1},
			DstAddrMulticast:    [16]byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
			PortMulticast:       9999,
			SyncSuppressTimeout: 1,
		}
		pb := &SyncConfig{}

		cfg := pb.ToCWithDefaults(current)

		require.Equal(t, current.SrcAddr, cfg.SrcAddr)
		require.Equal(t, current.DstAddrMulticast, cfg.DstAddrMulticast)
		require.Equal(t, current.PortMulticast, cfg.PortMulticast)
	})
}

// TestSyncSuppressTimeoutRoundTrip verifies that sync_suppress_timeout
// behaves correctly through the Pb->C->Pb round-trip and the
// ToCWithDefaults merge: a non-zero value overrides, while a zero value
// inherits the current window.
func TestSyncSuppressTimeoutRoundTrip(t *testing.T) {
	const suppress uint64 = 8e9

	t.Run("round_trip", func(t *testing.T) {
		pb := &SyncConfig{SyncSuppressTimeout: suppress}
		got := FromCSyncConfig(pb.ToC())
		require.Equal(t, suppress, got.GetSyncSuppressTimeout())
	})

	t.Run("zero_inherits", func(t *testing.T) {
		// Zero is indistinguishable from omitted and inherits the current
		// window, like every other scalar in the merge.
		current := cfwstate.SyncConfig{SyncSuppressTimeout: suppress}
		pb := &SyncConfig{}
		cfg := pb.ToCWithDefaults(current)
		require.Equal(t, suppress, cfg.SyncSuppressTimeout)
	})

	t.Run("explicit_override", func(t *testing.T) {
		current := cfwstate.SyncConfig{SyncSuppressTimeout: suppress}
		pb := &SyncConfig{SyncSuppressTimeout: 1e9}
		cfg := pb.ToCWithDefaults(current)
		require.Equal(t, uint64(1e9), cfg.SyncSuppressTimeout)
	})
}
