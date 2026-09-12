package neighbour_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/hwroute"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// recordingPublicationService captures decoded requests without receiver state.
type recordingPublicationService struct {
	operatorpb.UnimplementedNeighbourServiceServer
	Requests chan *operatorpb.ReplaceNeighboursRequest
}

func (m *recordingPublicationService) ReplaceNeighbours(ctx context.Context, request *operatorpb.ReplaceNeighboursRequest) (*operatorpb.ReplaceNeighboursResponse, error) {
	select {
	case m.Requests <- request:
		return &operatorpb.ReplaceNeighboursResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// newPublicationClient records wire payloads using default gRPC message limits.
func newPublicationClient(t *testing.T) (<-chan *operatorpb.ReplaceNeighboursRequest, operatorpb.NeighbourServiceClient) {
	t.Helper()
	service := &recordingPublicationService{Requests: make(chan *operatorpb.ReplaceNeighboursRequest, 1)}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	operatorpb.RegisterNeighbourServiceServer(server, service)
	var group errgroup.Group
	group.Go(func() error {
		err := server.Serve(listener)
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return err
	})
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		require.NoError(t, group.Wait())
	})
	connection, err := grpc.NewClient("passthrough:///publication",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) { return listener.DialContext(ctx) }),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	return service.Requests, operatorpb.NewNeighbourServiceClient(connection)
}

// publicationConfig supplies a stable namespace identity for all transports.
func publicationConfig() neighbour.PublicationConfig {
	return neighbour.PublicationConfig{TableName: "netlink-dataplane-default", DefaultPriority: 100, Timeout: 5 * time.Second}
}

// newPublisherTarget provides an alternate connection without table ownership.
func newPublisherTarget(name string, client neighbour.Client) neighbour.GatewayTarget {
	return neighbour.GatewayTarget{Name: name, Client: client}
}

// testDesiredEntry carries a complete observed identity in the publisher namespace.
func testDesiredEntry(nextHop, device string) neighbour.Entry {
	return neighbour.Entry{
		NextHop:       netip.MustParseAddr(nextHop),
		HardwareRoute: hwroute.HardwareRoute{SourceMAC: [6]byte{2, 0, 0, 0, 0, 1}, DestinationMAC: [6]byte{2, 0, 0, 0, 0, 2}, Device: device},
	}
}

// wireEntry excludes receiver-generated metadata from the expected payload.
func wireEntry(entry neighbour.Entry) *operatorpb.NeighbourEntry {
	return &operatorpb.NeighbourEntry{
		NextHop:      commonpb.NewIPAddressFromAddr(entry.NextHop.Unmap()),
		HardwareAddr: commonpb.NewMACAddressEUI48(entry.HardwareRoute.SourceMAC),
		LinkAddr:     commonpb.NewMACAddressEUI48(entry.HardwareRoute.DestinationMAC),
		Device:       entry.HardwareRoute.Device, State: operatorpb.NeighbourState_NUD_PERMANENT,
	}
}

// Test_Publish_WireRequest verifies canonical IP ordering, complete payloads
// without receiver-owned metadata, and preservation of caller input.
func Test_Publish_WireRequest(t *testing.T) {
	ipv4 := testDesiredEntry("::ffff:192.0.2.1", "logical0")
	ipv6 := testDesiredEntry("2001:db8::1", "logical1")
	linkLocal := testDesiredEntry("fe80::1", "logical0")
	for _, tc := range []struct {
		name     string
		entries  []neighbour.Entry
		expected []*operatorpb.NeighbourEntry
	}{
		{
			name:     "canonical IP ordering",
			entries:  []neighbour.Entry{linkLocal, ipv6, ipv4},
			expected: []*operatorpb.NeighbourEntry{wireEntry(ipv4), wireEntry(ipv6), wireEntry(linkLocal)},
		},
		{name: "empty replacement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests, client := newPublicationClient(t)
			config := publicationConfig()
			original := slices.Clone(tc.entries)
			targets := []neighbour.GatewayTarget{newPublisherTarget("first", client)}
			require.NoError(t, neighbour.Publish(t.Context(), tc.entries, targets, config))
			require.Equal(t, original, tc.entries)
			expected := &operatorpb.ReplaceNeighboursRequest{
				Table: config.TableName, DefaultPriority: config.DefaultPriority,
				Entries: tc.expected,
			}
			require.Len(t, requests, 1)
			require.True(t, proto.Equal(expected, <-requests))
		})
	}
}

// Test_Publish_InvalidSnapshot verifies that invalid payloads and duplicate
// canonical IPs are rejected before any transport call.
func Test_Publish_InvalidSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]neighbour.Entry) []neighbour.Entry
	}{
		{name: "duplicate IP", mutate: func(entries []neighbour.Entry) []neighbour.Entry { return append(entries, entries[0]) }},
		{name: "duplicate IP on another device", mutate: func(entries []neighbour.Entry) []neighbour.Entry {
			duplicate := entries[0]
			duplicate.HardwareRoute.Device = "logical1"
			return append(entries, duplicate)
		}},
		{name: "mapped duplicate", mutate: func(entries []neighbour.Entry) []neighbour.Entry {
			duplicate := entries[0]
			duplicate.NextHop = netip.MustParseAddr("::ffff:192.0.2.1")
			return append(entries, duplicate)
		}},
		{name: "mapped duplicate on another device", mutate: func(entries []neighbour.Entry) []neighbour.Entry {
			return append(entries, testDesiredEntry("::ffff:192.0.2.1", "logical1"))
		}},
		{name: "invalid address", mutate: func(entries []neighbour.Entry) []neighbour.Entry { entries[0].NextHop = netip.Addr{}; return entries }},
		{name: "zoned address", mutate: func(entries []neighbour.Entry) []neighbour.Entry {
			entries[0].NextHop = netip.MustParseAddr("fe80::1%kni0")
			return entries
		}},
		{name: "overlong device", mutate: func(entries []neighbour.Entry) []neighbour.Entry {
			entries[0].HardwareRoute.Device = strings.Repeat("d", 80)
			return entries
		}},
		{name: "zero source MAC", mutate: func(entries []neighbour.Entry) []neighbour.Entry {
			entries[0].HardwareRoute.SourceMAC = [6]byte{}
			return entries
		}},
		{name: "zero destination MAC", mutate: func(entries []neighbour.Entry) []neighbour.Entry {
			entries[0].HardwareRoute.DestinationMAC = [6]byte{}
			return entries
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &unavailableClient{}
			entries := tc.mutate([]neighbour.Entry{testDesiredEntry("192.0.2.1", "logical0")})
			require.Error(t, neighbour.Publish(t.Context(), entries, []neighbour.GatewayTarget{newPublisherTarget("first", client)}, publicationConfig()))
			require.Zero(t, client.Calls)
		})
	}
}
