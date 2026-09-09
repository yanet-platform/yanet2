package operatorpb

import (
	"errors"
	"strings"
)

// Neighbour replacement limits bound both publisher batches and receiver staging.
const (
	NeighbourChunkEntries      = 1000
	NeighbourChunkBytes        = 256 * 1024
	NeighbourSnapshotEntries   = 1_000_000
	NeighbourSnapshotBytes     = 128 * 1024 * 1024
	NeighbourConcurrentStreams = 4
	NeighbourNameBytes         = 128
)

// ValidateNeighbourDevice checks a logical egress name at the publishing edge.
func ValidateNeighbourDevice(device string) error {
	if len(device) == 0 || len(device) > NeighbourNameBytes {
		return errors.New("device name must contain 1..128 bytes")
	}
	if strings.ContainsAny(device, "\x00 \t\n\r\v\f") {
		return errors.New("device name contains whitespace or a NUL byte")
	}
	return nil
}
