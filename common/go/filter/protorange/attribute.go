package protorange

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/filterpb"
)

type ProtoRange struct {
	From uint16
	To   uint16
}

type ProtoRanges []ProtoRange

func FromProtoRanges(protoRanges []*filterpb.ProtoRange) ([]ProtoRange, error) {
	result := make([]ProtoRange, len(protoRanges))

	for idx := range protoRanges {
		// Protocol range is stored as uint16 in the code
		if protoRanges[idx].From > 65535 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol 'from' value %d exceeds maximum 65535",
				protoRanges[idx].From,
			)
		}
		if protoRanges[idx].To > 65535 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol 'to' value %d exceeds maximum 65535",
				protoRanges[idx].To,
			)
		}
		if protoRanges[idx].From > protoRanges[idx].To {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"Protocol 'from' value %d is greater than 'to' value %d",
				protoRanges[idx].From,
				protoRanges[idx].To,
			)
		}
		result[idx] = ProtoRange{
			From: uint16(protoRanges[idx].From),
			To:   uint16(protoRanges[idx].To),
		}
	}

	return result, nil
}
