package neighbour_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// Test_Publish_UnarySnapshotLimits verifies that the exact entry limit fits one
// default-sized RPC even with maximum-width payloads, and overflow is not sent.
func Test_Publish_UnarySnapshotLimits(t *testing.T) {
	require.Equal(t, 15252, operatorpb.NeighbourSnapshotEntries)
	require.Equal(t, 4*1024*1024, operatorpb.NeighbourSnapshotBytes)
	requests, client := newPublicationClient(t)
	entry := testDesiredEntry("2001:db8::1", strings.Repeat("d", 79))
	entry.HardwareRoute.SourceMAC = [6]byte{255, 255, 255, 255, 255, 255}
	entry.HardwareRoute.DestinationMAC = entry.HardwareRoute.SourceMAC
	entries := make([]neighbour.Entry, operatorpb.NeighbourSnapshotEntries)
	for idx := range entries {
		entries[idx] = entry
		entry.NextHop = entry.NextHop.Next()
	}
	target := newPublisherTarget("first", client)
	config := publicationConfig()
	config.TableName = "netlink-dataplane-" + strings.Repeat("t", 128-len("netlink-dataplane-"))
	config.DefaultPriority = math.MaxUint32
	config.Timeout = 30 * time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	require.NoError(t, neighbour.Publish(ctx, entries, []neighbour.GatewayTarget{target}, config))
	require.Len(t, requests, 1)
	request := <-requests
	require.Len(t, request.GetEntries(), operatorpb.NeighbourSnapshotEntries)
	require.LessOrEqual(t, proto.Size(request), operatorpb.NeighbourSnapshotBytes)
	require.Equal(t, config.TableName, request.GetTable())
	require.Equal(t, config.DefaultPriority, request.GetDefaultPriority())
	for idx, wire := range request.GetEntries() {
		require.True(t, proto.Equal(wireEntry(entries[idx]), wire))
	}

	unneeded := &unavailableClient{}
	err := neighbour.Publish(ctx, append(entries, entry), []neighbour.GatewayTarget{newPublisherTarget("oversized", unneeded)}, config)
	require.ErrorContains(t, err, "entry limit")
	require.Zero(t, unneeded.Calls)
}
