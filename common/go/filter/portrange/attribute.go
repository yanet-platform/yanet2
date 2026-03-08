package portrange

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/filterpb"
)

type PortRange struct {
	From uint16
	To   uint16
}

type PortRanges []PortRange

func FromPortRanges(portRanges []*filterpb.PortRange) ([]PortRange, error) {
	result := make([]PortRange, len(portRanges))

	for idx := range portRanges {
		// Port is 16 bits, so valid range is 0-65535
		if portRanges[idx].From > 65535 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Port 'from' value %d exceeds maximum 65535",
				portRanges[idx].From,
			)
		}
		if portRanges[idx].To > 65535 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Port 'to' value %d exceeds maximum 65535",
				portRanges[idx].To,
			)
		}
		if portRanges[idx].From > portRanges[idx].To {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Port 'from' value %d is greater than 'to' value %d",
				portRanges[idx].From,
				portRanges[idx].To,
			)
		}

		result[idx] = PortRange{
			From: uint16(portRanges[idx].From),
			To:   uint16(portRanges[idx].To),
		}
	}

	return result, nil
}
