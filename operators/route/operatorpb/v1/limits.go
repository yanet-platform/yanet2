package operatorpb

import (
	"errors"

	"github.com/yanet-platform/yanet2/modules/route/controlplane/hwroute"
)

// Neighbour replacement limits bound both publisher batches and receiver staging.
const (
	NeighbourChunkEntries      = 1000
	NeighbourChunkBytes        = 256 * 1024
	NeighbourSnapshotEntries   = 1_000_000
	NeighbourSnapshotBytes     = 128 * 1024 * 1024
	NeighbourConcurrentStreams = 4
	NeighbourTableNameBytes    = 128
)

// Neighbour list limits bound each response, including source metadata.
const (
	NeighbourListChunkEntries = 1000
	NeighbourListChunkBytes   = 256 * 1024
	NeighbourListUnaryBytes   = 512 * 1024 * 1024
)

// ValidateNeighbourDevice checks a logical egress name at the publishing edge.
func ValidateNeighbourDevice(device string) error {
	if device == "" {
		return errors.New("device name is required")
	}
	return hwroute.ValidateDevice(device)
}
