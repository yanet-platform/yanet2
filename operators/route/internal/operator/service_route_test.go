package operator

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

func TestInsertRoute_NonStaticMultipleNexthops_InvalidArgument(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	req := &operatorpb.InsertRouteRequest{
		Name:   "route0",
		Prefix: "10.0.0.0/24",
		NexthopAddrs: []*commonpb.IPAddress{
			commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.1")),
			commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.2")),
		},
		SourceId: operatorpb.RouteSourceID_ROUTE_SOURCE_ID_BIRD,
	}

	_, err := svc.InsertRoute(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}
