package route

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sidecarpb "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/sidecarpb/v1"
)

const defaultMaxConcurrentStreams = 4

// ServiceConfig controls static-route stream resource limits.
type ServiceConfig struct {
	MaxRoutes            int
	MaxConcurrentStreams int
}

// Service receives complete static-route snapshots over gRPC.
type Service struct {
	sidecarpb.UnimplementedNetlinkDataplaneServiceServer

	store       *Store
	maxRoutes   int
	streamSlots chan struct{}
	streamMutex sync.Mutex
}

// NewService creates a static-route service with a positive total route limit.
func NewService(store *Store, config ServiceConfig) (*Service, error) {
	if store == nil {
		return nil, errors.New("create static route service: store is nil")
	}
	if config.MaxRoutes <= 0 {
		return nil, fmt.Errorf(
			"create static route service: max routes must be positive, got %d",
			config.MaxRoutes,
		)
	}
	maxConcurrentStreams := config.MaxConcurrentStreams
	if maxConcurrentStreams == 0 {
		maxConcurrentStreams = defaultMaxConcurrentStreams
	}
	if maxConcurrentStreams < 0 {
		return nil, fmt.Errorf(
			"create static route service: max concurrent streams must be positive, got %d",
			config.MaxConcurrentStreams,
		)
	}
	return &Service{
		store:       store,
		maxRoutes:   config.MaxRoutes,
		streamSlots: make(chan struct{}, maxConcurrentStreams),
	}, nil
}

// UpdateStaticRoutes commits a complete stream only after a clean EOF.
func (m *Service) UpdateStaticRoutes(
	stream grpc.ClientStreamingServer[
		sidecarpb.UpdateStaticRoutesRequest,
		sidecarpb.UpdateStaticRoutesResponse,
	],
) error {
	select {
	case m.streamSlots <- struct{}{}:
		defer func() { <-m.streamSlots }()
	default:
		return status.Error(
			codes.ResourceExhausted,
			"too many static route snapshots are being staged",
		)
	}

	routes := []Route{}
	seen := map[Route]int{}
	receivedRequest := false
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if !receivedRequest {
				return status.Error(
					codes.InvalidArgument,
					"static route snapshot contains no requests",
				)
			}
			if !m.streamMutex.TryLock() {
				return status.Error(codes.Aborted, "another static route update is in progress")
			}
			defer m.streamMutex.Unlock()
			update, err := m.store.ReplaceTracked(routes)
			if err != nil {
				return status.Errorf(codes.InvalidArgument, "invalid route snapshot: %v", err)
			}
			if err := update.Wait(stream.Context()); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return status.FromContextError(err).Err()
				}
				return status.Errorf(codes.Unavailable, "apply static route snapshot: %v", err)
			}
			if err := stream.SendAndClose(&sidecarpb.UpdateStaticRoutesResponse{}); err != nil {
				return fmt.Errorf("send static route response: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("receive static route chunk: %w", err)
		}
		if request == nil {
			return status.Error(codes.InvalidArgument, "static route chunk is nil")
		}
		receivedRequest = true
		if len(request.GetRoutes()) > m.maxRoutes-len(routes) {
			return status.Errorf(
				codes.ResourceExhausted,
				"static route snapshot exceeds limit of %d routes",
				m.maxRoutes,
			)
		}

		for chunkIndex, wireRoute := range request.GetRoutes() {
			route, convertErr := routeFromProto(wireRoute)
			if convertErr != nil {
				return status.Errorf(
					codes.InvalidArgument,
					"invalid static route at chunk index %d: %v",
					chunkIndex,
					convertErr,
				)
			}
			if previous, duplicate := seen[route]; duplicate {
				return status.Errorf(
					codes.InvalidArgument,
					"static route %d duplicates route %d",
					len(routes),
					previous,
				)
			}
			seen[route] = len(routes)
			routes = append(routes, route)
		}
	}
}

// Register registers the service and returns its fully-qualified name.
func (m *Service) Register(server *grpc.Server) string {
	sidecarpb.RegisterNetlinkDataplaneServiceServer(server, m)
	return sidecarpb.NetlinkDataplaneService_ServiceDesc.ServiceName
}

func routeFromProto(wireRoute *sidecarpb.StaticRoute) (Route, error) {
	if wireRoute == nil {
		return Route{}, errors.New("route is nil")
	}
	if wireRoute.GetPrefix() == nil {
		return Route{}, errors.New("prefix is missing")
	}
	prefix, err := wireRoute.GetPrefix().ToPrefix()
	if err != nil {
		return Route{}, fmt.Errorf("convert prefix: %w", err)
	}
	if wireRoute.GetNexthop() == nil {
		return Route{}, errors.New("nexthop is missing")
	}
	nexthop, err := wireRoute.GetNexthop().ToAddr()
	if err != nil {
		return Route{}, fmt.Errorf("convert nexthop: %w", err)
	}

	route := Route{
		Prefix:    prefix,
		Nexthop:   nexthop,
		Interface: wireRoute.GetInterface(),
	}
	if err := validateRoute(route); err != nil {
		return Route{}, err
	}
	return route, nil
}

var _ sidecarpb.NetlinkDataplaneServiceServer = (*Service)(nil)
