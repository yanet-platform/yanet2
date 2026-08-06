package neigh_test

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
)

// fakeKernelTable is a neigh.KernelTable backed by fixed link and neighbour
// lists instead of a live netlink socket.
type fakeKernelTable struct {
	links  []netlink.Link
	neighs []netlink.Neigh
}

// LinkList returns the fixed link list.
func (m fakeKernelTable) LinkList() ([]netlink.Link, error) {
	return m.links, nil
}

// NeighList returns the fixed neighbour list.
func (m fakeKernelTable) NeighList() ([]netlink.Neigh, error) {
	return m.neighs, nil
}

// TestNeighMonitorRejectsUnusableSourceMAC verifies that a neighbour whose
// link cannot provide a usable source MAC — a nil, all-zero, or non-EUI-48
// hardware address — is left out of the resolved nexthop cache instead of
// being admitted with a fabricated or unusable address.
func TestNeighMonitorRejectsUnusableSourceMAC(t *testing.T) {
	nexthop := netip.MustParseAddr("10.0.0.1")
	neighbourMAC := net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}

	tests := []struct {
		name             string
		linkHardwareAddr net.HardwareAddr
		wantPresent      bool
	}{
		{
			name:             "nil hardware address",
			linkHardwareAddr: nil,
			wantPresent:      false,
		},
		{
			name:             "all-zero hardware address",
			linkHardwareAddr: net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			wantPresent:      false,
		},
		{
			name:             "short tunnel-style hardware address",
			linkHardwareAddr: net.HardwareAddr{0x0a, 0x00, 0x00, 0x01},
			wantPresent:      false,
		},
		{
			name:             "normal EUI-48 hardware address",
			linkHardwareAddr: net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
			wantPresent:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table := neigh.NewNeighTable()
			source, err := table.CreateSource("kernel", 100, true)
			require.NoError(t, err)

			link := &netlink.Device{
				LinkAttrs: netlink.LinkAttrs{
					Index:        1,
					Name:         "eth0",
					HardwareAddr: tt.linkHardwareAddr,
				},
			}
			kernelNeigh := netlink.Neigh{
				LinkIndex:    1,
				IP:           nexthop.AsSlice(),
				HardwareAddr: neighbourMAC,
				State:        netlink.NUD_REACHABLE,
			}

			fake := fakeKernelTable{
				links:  []netlink.Link{link},
				neighs: []netlink.Neigh{kernelNeigh},
			}

			neigh.NewNeighMonitor(table, source,
				neigh.WithKernelTable(fake),
				neigh.WithLog(zap.NewNop()),
			)

			entry, ok := table.View().Lookup(nexthop)
			require.Equal(t, tt.wantPresent, ok)
			if tt.wantPresent {
				require.Equal(t, [6]byte(tt.linkHardwareAddr), entry.HardwareRoute.SourceMAC)
				require.Equal(t, [6]byte(neighbourMAC), entry.HardwareRoute.DestinationMAC)
			}
		})
	}
}

// TestNeighMonitorClassifiesMissingVsMalformedDestinationMAC verifies how a
// destination hardware address is classified by its length.
//
// A neighbour with no hardware address is skipped without a warning, while a
// present but non-EUI-48 address still warns. Both are excluded from the
// resolved nexthop cache either way.
func TestNeighMonitorClassifiesMissingVsMalformedDestinationMAC(t *testing.T) {
	nexthop := netip.MustParseAddr("10.0.0.2")
	linkHardwareAddr := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}

	tests := []struct {
		name              string
		neighHardwareAddr net.HardwareAddr
		wantWarn          bool
	}{
		{
			name:              "unresolved neighbour has no hardware address",
			neighHardwareAddr: nil,
			wantWarn:          false,
		},
		{
			name:              "malformed non-empty hardware address",
			neighHardwareAddr: net.HardwareAddr{0x0a, 0x00, 0x00, 0x01},
			wantWarn:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table := neigh.NewNeighTable()
			source, err := table.CreateSource("kernel", 100, true)
			require.NoError(t, err)

			link := &netlink.Device{
				LinkAttrs: netlink.LinkAttrs{
					Index:        1,
					Name:         "eth0",
					HardwareAddr: linkHardwareAddr,
				},
			}
			kernelNeigh := netlink.Neigh{
				LinkIndex:    1,
				IP:           nexthop.AsSlice(),
				HardwareAddr: tt.neighHardwareAddr,
				State:        netlink.NUD_INCOMPLETE,
			}

			fake := fakeKernelTable{
				links:  []netlink.Link{link},
				neighs: []netlink.Neigh{kernelNeigh},
			}

			core, logs := observer.New(zapcore.DebugLevel)
			neigh.NewNeighMonitor(table, source,
				neigh.WithKernelTable(fake),
				neigh.WithLog(zap.New(core)),
			)

			_, ok := table.View().Lookup(nexthop)
			require.False(t, ok, "entry with a bad destination MAC must never enter the cache")

			gotWarn := false
			for _, entry := range logs.All() {
				if entry.Level == zapcore.WarnLevel {
					gotWarn = true
				}
			}
			require.Equal(t, tt.wantWarn, gotWarn)
		})
	}
}
