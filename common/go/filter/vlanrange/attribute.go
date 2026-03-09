package vlanrange

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/filterpb"
)

type VlanRange struct {
	From uint16
	To   uint16
}

type VlanRanges []VlanRange

func FromVlanRanges(vlanRanges []*filterpb.VlanRange) ([]VlanRange, error) {
	result := make([]VlanRange, len(vlanRanges))

	for idx := range vlanRanges {
		if vlanRanges[idx].From > 4095 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"VLAN 'from' value %d exceeds maximum 4095",
				vlanRanges[idx].From,
			)
		}
		if vlanRanges[idx].To > 4095 {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"VLAN 'to' value %d exceeds maximum 4095",
				vlanRanges[idx].To,
			)
		}
		result[idx] = VlanRange{
			From: uint16(vlanRanges[idx].From),
			To:   uint16(vlanRanges[idx].To),
		}
	}

	return result, nil
}
