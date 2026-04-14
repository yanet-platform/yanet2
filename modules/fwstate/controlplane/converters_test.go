package fwstate

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb"
)

// TestPortsRoundTrip verifies that port_unicast and port_multicast survive
// the Pb->C->Pb round-trip with correct LE<->BE byte-order conversion.
func TestPortsRoundTrip(t *testing.T) {
	const portMulticast uint32 = 4789
	const portUnicast uint32 = 9999

	pb := &fwstatepb.SyncConfig{
		SrcAddr:          make([]byte, 16),
		DstEther:         make([]byte, 6),
		DstAddrMulticast: make([]byte, 16),
		DstAddrUnicast:   make([]byte, 16),
		PortMulticast:    portMulticast,
		PortUnicast:      portUnicast,
	}

	cCfg := ConvertPbToCSyncConfig(pb)
	got := ConvertCSyncConfigToPb(&cCfg)

	require.Equal(t, portMulticast, got.PortMulticast)
	require.Equal(t, portUnicast, got.PortUnicast)
}
