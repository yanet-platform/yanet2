package neigh_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

func Test_NeighTable_SwapSourceContext_RejectedContextPreservesObservation(t *testing.T) {
	for _, test := range []struct {
		name    string
		context func(*testing.T) context.Context
		err     error
	}{
		{
			name: "cancelled",
			context: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			err: context.Canceled,
		},
		{
			name: "expired deadline",
			context: func(t *testing.T) context.Context {
				ctx, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
				t.Cleanup(cancel)
				return ctx
			},
			err: context.DeadlineExceeded,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			changes := 0
			table := neigh.NewNeighTable(neigh.WithTableOnChanged(func() { changes++ }))
			_, err := table.CreateSource("publisher", 100, false)
			require.NoError(t, err)
			entry := neigh.NeighbourEntry{
				NextHop: netip.MustParseAddr("192.0.2.1"),
				HardwareRoute: neigh.HardwareRoute{
					SourceMAC:      [6]byte{2, 0, 0, 0, 0, 1},
					DestinationMAC: [6]byte{2, 0, 0, 0, 0, 2},
					Device:         "eth0",
				},
				Priority: 100,
			}
			require.NoError(t, table.Add("publisher", []neigh.NeighbourEntry{entry}))
			require.Equal(t, 1, changes)

			err = table.SwapSourceContext(test.context(t), "publisher", map[netip.Addr]neigh.NeighbourEntry{})
			require.ErrorIs(t, err, test.err)
			view, ok := table.SourceView("publisher")
			require.True(t, ok)
			actual, ok := view.Lookup(entry.NextHop)
			require.True(t, ok, "rejected replacement must retain the accepted neighbour")
			require.Equal(t, entry, actual)
			merged, ok := table.View().Lookup(entry.NextHop)
			require.True(t, ok)
			require.Equal(t, entry.HardwareRoute, merged.HardwareRoute)
			require.Equal(t, 1, changes, "rejected replacement must not wake reconciliation")
		})
	}
}
