package route

import (
	"context"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/modules/route/controlplane/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
)

// NeighbourService implements the Neighbour service for retrieving neighbor information.
type NeighbourService struct {
	routepb.UnimplementedNeighbourServer

	neighCache *neigh.NexthopCache
	log        *zap.SugaredLogger
}

// NewNeighbourService creates a new NeighbourService.
func NewNeighbourService(neighCache *neigh.NexthopCache, log *zap.SugaredLogger) *NeighbourService {
	return &NeighbourService{
		neighCache: neighCache,
		log:        log,
	}
}

// List returns all current neighbors.
func (m *NeighbourService) List(ctx context.Context, req *routepb.ListNeighboursRequest) (*routepb.ListNeighboursResponse, error) {
	// Get a view of the current neighbor cache
	view := m.neighCache.View()
	entries, size := view.Entries()

	// Convert all entries to proto format
	protoEntries := make([]*routepb.NeighbourEntry, 0, size)

	for entry := range entries {
		protoEntry := &routepb.NeighbourEntry{
			NextHop:      entry.NextHop.String(),
			LinkAddr:     routepb.NewMACAddressEUI48(entry.HardwareRoute.DestinationMAC),
			HardwareAddr: routepb.NewMACAddressEUI48(entry.HardwareRoute.SourceMAC),
			State:        routepb.NeighbourState(entry.State),
			UpdatedAt:    entry.UpdatedAt.Unix(),
		}
		protoEntries = append(protoEntries, protoEntry)
	}

	return &routepb.ListNeighboursResponse{
		Neighbours: protoEntries,
	}, nil
}
