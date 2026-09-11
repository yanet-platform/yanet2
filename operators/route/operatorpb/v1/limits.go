package operatorpb

import (
	"errors"

	"github.com/yanet-platform/yanet2/modules/route/controlplane/hwroute"
)

// Neighbour replacement limits bound one complete request before compression.
// The entry cap is a technical bound, not a deployment inventory estimate.
const (
	NeighbourSnapshotEntries = 15_252
	NeighbourSnapshotBytes   = 4 * 1024 * 1024
	NeighbourTableNameBytes  = 128
)

// Neighbour list limits bound each response, including source metadata.
const (
	NeighbourListUnaryBytes = 4 * 1024 * 1024
)

// ValidateNeighbourDevice checks a logical egress name at the publishing edge.
func ValidateNeighbourDevice(device string) error {
	if device == "" {
		return errors.New("device name is required")
	}
	return hwroute.ValidateDevice(device)
}
