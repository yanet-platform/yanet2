package neighbour_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
)

// Test_Publish_LargeSnapshotBatching verifies that maximum-width identities are
// split into bounded chunks without losing or duplicating entries.
func Test_Publish_LargeSnapshotBatching(t *testing.T) {
	service, client := newPublicationService(t)
	entry := testDesiredEntry("2001:db8::1", strings.Repeat("d", 128))
	entries := make([]neighbour.Entry, 60_000)
	for idx := range entries {
		entries[idx] = entry
		entry.NextHop = entry.NextHop.Next()
	}
	target := newPublisherTarget("first", client)
	config := publicationConfig()
	config.TableName = "netlink-dataplane-" + strings.Repeat("t", 128-len("netlink-dataplane-"))
	config.Timeout = 30 * time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	require.NoError(t, neighbour.Publish(ctx, entries, []neighbour.GatewayTarget{target}, config))
	chunks := 0
	published := 0
	for _, call := range service.Calls() {
		if call.Chunk == nil {
			continue
		}
		chunks++
		require.LessOrEqual(t, len(call.Chunk.GetEntries()), 1000)
		require.LessOrEqual(t, proto.Size(call.Chunk), 256*1024)
		require.Equal(t, config.TableName, call.Chunk.GetTable())
		require.Equal(t, config.DefaultPriority, call.Chunk.GetDefaultPriority())
		for _, wire := range call.Chunk.GetEntries() {
			require.True(t, proto.Equal(wireEntry(entries[published]), wire))
			published++
		}
	}
	require.Equal(t, 60, chunks)
	require.Equal(t, len(entries), published)
}
