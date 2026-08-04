package route_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// mustRange builds a validated IPRange from two address strings.
func mustRange(t *testing.T, start, end string) *commonpb.IPRange {
	t.Helper()

	r, err := commonpb.NewIPRange(netip.MustParseAddr(start), netip.MustParseAddr(end))
	require.NoError(t, err)
	return r
}

// rangeEntry builds a FIBEntry with no nexthops.
func rangeEntry(t *testing.T, start, end string) *routepb.FIBEntry {
	t.Helper()

	return &routepb.FIBEntry{Range: mustRange(t, start, end)}
}

// TestUpdateFIBInvalidRangesRejected verifies that a nil range, an inverted
// range, and a family-mismatched range are all rejected as InvalidArgument
// before reaching the backend.
func TestUpdateFIBInvalidRangesRejected(t *testing.T) {
	v4 := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1"))
	v6 := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("2001:db8::1"))

	tests := []struct {
		name  string
		entry *routepb.FIBEntry
	}{
		{
			name:  "nil range",
			entry: &routepb.FIBEntry{},
		},
		{
			name: "inverted range",
			entry: &routepb.FIBEntry{
				Range: &commonpb.IPRange{
					Start: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.255")),
					End:   commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.0")),
				},
			},
		},
		{
			name: "family mismatch",
			entry: &routepb.FIBEntry{
				Range: &commonpb.IPRange{Start: v4, End: v6},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeBackend()
			service := route.NewRouteService(backend)

			_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
				ModuleName: "cfg",
				Entries:    []*routepb.FIBEntry{test.entry},
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Empty(t, backend.updateCalls)
		})
	}
}

// TestUpdateFIBPreservesOrder verifies that UpdateFIB forwards entries to the
// backend in the exact order the request carried, since order is a
// documented wire contract a sort or dedup would silently break.
func TestUpdateFIBPreservesOrder(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	// Start addresses are strictly descending, so a sort of any kind (by
	// start, by end, or by range) would visibly reorder this input.
	//
	// The expected order is captured into independent strings below,
	// rather than compared against the input slice itself: this call
	// runs in-process, so req.GetEntries() aliases the same backing
	// array as entries, and an in-place sort in the service would
	// silently reorder the "want" side along with the "got" side.
	entries := []*routepb.FIBEntry{
		rangeEntry(t, "10.0.2.0", "10.0.2.255"),
		rangeEntry(t, "10.0.1.0", "10.0.1.255"),
		rangeEntry(t, "10.0.0.0", "10.0.0.255"),
	}
	wantStarts := []string{"10.0.2.0", "10.0.1.0", "10.0.0.0"}
	wantEnds := []string{"10.0.2.255", "10.0.1.255", "10.0.0.255"}

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    entries,
	})
	require.NoError(t, err)
	require.Len(t, backend.updateCalls, 1)

	got := backend.updateCalls[0]
	require.Len(t, got, len(wantStarts))
	for idx := range wantStarts {
		gotStart, gotEnd, err := got[idx].GetRange().ToRange()
		require.NoError(t, err)
		require.Equal(t, wantStarts[idx], gotStart.String(), "entry %d start", idx)
		require.Equal(t, wantEnds[idx], gotEnd.String(), "entry %d end", idx)
	}
}
