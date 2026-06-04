package fwstatepb

import (
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// TestPortsRoundTrip verifies that port_unicast and port_multicast survive
// the Pb->C->Pb round-trip with correct LE<->BE byte-order conversion.
func TestPortsRoundTrip(t *testing.T) {
	const portMulticast uint32 = 4789
	const portUnicast uint32 = 9999

	pb := &SyncConfig{
		SrcAddr:          &commonpb.IPAddress{Addr: make([]byte, 16)},
		DstEther:         commonpb.NewMACAddressEUI48([6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}),
		DstAddrMulticast: &commonpb.IPAddress{Addr: make([]byte, 16)},
		DstAddrUnicast:   &commonpb.IPAddress{Addr: make([]byte, 16)},
		PortMulticast:    portMulticast,
		PortUnicast:      portUnicast,
	}

	cCfg := pb.ToC()
	got := FromCSyncConfig(cCfg)

	require.Equal(t, portMulticast, got.PortMulticast)
	require.Equal(t, portUnicast, got.PortUnicast)
	require.Equal(t, pb.DstEther.GetAddr(), got.DstEther.GetAddr())
}

func TestSyncConfig_ToCWithDefaults(t *testing.T) {
	t.Run("omitted", func(t *testing.T) {
		current := cfwstate.SyncConfig{
			DstEther: [6]byte{0x10, 0x11, 0x12, 0x13, 0x14, 0x15},
		}
		pb := &SyncConfig{}

		cfg := pb.ToCWithDefaults(current)

		require.Equal(t, current.DstEther, cfg.DstEther)
	})

	t.Run("explicit_zero", func(t *testing.T) {
		current := cfwstate.SyncConfig{
			DstEther: [6]byte{0x10, 0x11, 0x12, 0x13, 0x14, 0x15},
		}
		pb := &SyncConfig{
			DstEther: &commonpb.MACAddress{},
		}

		cfg := pb.ToCWithDefaults(current)

		require.Equal(t, [6]byte{}, cfg.DstEther)
	})
}
