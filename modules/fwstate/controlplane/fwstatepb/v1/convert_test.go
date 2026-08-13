package fwstatepb

import (
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// TestPortsRoundTrip verifies that port_multicast survives the Pb->C->Pb
// round-trip with correct LE<->BE byte-order conversion.
func TestPortsRoundTrip(t *testing.T) {
	const portMulticast uint32 = 4789

	pb := &SyncConfig{
		SrcAddr:          &commonpb.IPAddress{Addr: make([]byte, 16)},
		DstAddrMulticast: &commonpb.IPAddress{Addr: make([]byte, 16)},
		PortMulticast:    portMulticast,
	}

	cCfg := pb.ToC()
	got := FromCSyncConfig(cCfg)

	require.Equal(t, portMulticast, got.PortMulticast)
}

func TestSyncConfig_ToCWithDefaults(t *testing.T) {
	t.Run("omitted", func(t *testing.T) {
		current := cfwstate.SyncConfig{
			SrcAddr: [16]byte{1},
		}
		pb := &SyncConfig{}

		cfg := pb.ToCWithDefaults(current)

		require.Equal(t, current.SrcAddr, cfg.SrcAddr)
	})
}

// TestSyncSuppressTimeoutRoundTrip verifies that sync_suppress_timeout, an
// optional field, behaves correctly through the Pb->C->Pb round-trip and the
// ToCWithDefaults merge: an explicit value (including zero = disable)
// overrides, while an absent field inherits the current window.
func TestSyncSuppressTimeoutRoundTrip(t *testing.T) {
	const suppress uint64 = 8e9

	t.Run("round_trip", func(t *testing.T) {
		pb := &SyncConfig{SyncSuppressTimeout: u64Ptr(suppress)}
		got := FromCSyncConfig(pb.ToC())
		require.Equal(t, suppress, got.GetSyncSuppressTimeout())
	})

	t.Run("explicit_zero_disables", func(t *testing.T) {
		// An explicit zero is the documented disable value and must override,
		// not inherit, the current window.
		current := cfwstate.SyncConfig{SyncSuppressTimeout: suppress}
		pb := &SyncConfig{SyncSuppressTimeout: u64Ptr(0)}
		cfg := pb.ToCWithDefaults(current)
		require.Equal(t, uint64(0), cfg.SyncSuppressTimeout)
	})

	t.Run("explicit_override", func(t *testing.T) {
		current := cfwstate.SyncConfig{SyncSuppressTimeout: suppress}
		pb := &SyncConfig{SyncSuppressTimeout: u64Ptr(1e9)}
		cfg := pb.ToCWithDefaults(current)
		require.Equal(t, uint64(1e9), cfg.SyncSuppressTimeout)
	})

	t.Run("absent_inherits", func(t *testing.T) {
		// A client that omits the field (nil) keeps the current window, so an
		// older web form does not silently disable suppression.
		current := cfwstate.SyncConfig{SyncSuppressTimeout: suppress}
		pb := &SyncConfig{}
		cfg := pb.ToCWithDefaults(current)
		require.Equal(t, suppress, cfg.SyncSuppressTimeout)
	})
}
