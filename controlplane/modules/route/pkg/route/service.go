package route

import (
	"context"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/modules/route/routepb"
)

type RouteService struct {
	routepb.UnimplementedRouteServer
	log *zap.SugaredLogger
}

func NewRouteService(log *zap.SugaredLogger) *RouteService {
	return &RouteService{
		log: log,
	}
}

func (m *RouteService) InsertRoute(
	ctx context.Context,
	request *routepb.InsertRouteRequest,
) (*routepb.InsertRouteResponse, error) {
	m.log.Warnw("LOL KEK", zap.Any("request", request))

	return &routepb.InsertRouteResponse{}, nil
}
