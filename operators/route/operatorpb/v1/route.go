package operatorpb

import (
	"errors"
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

func (m *ShowRoutesRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}
	if m.GetIpv4Only() && m.GetIpv6Only() {
		return errors.New("ipv4_only and ipv6_only must not both be set")
	}

	return nil
}

func (m *LookupRouteRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}

	return nil
}

func (m *InsertRouteRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}
	if len(m.GetNexthopAddrs()) == 0 {
		return errors.New("nexthop_addrs is required")
	}
	if err := validateSourceID(m.GetSourceId()); err != nil {
		return err
	}
	if m.GetSourceId() == RouteSourceID_ROUTE_SOURCE_ID_BIRD && len(m.GetNexthopAddrs()) > 1 {
		return errors.New("nexthop_addrs must contain at most one address for non-static source")
	}

	return nil
}

func (m *DeleteRouteRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}
	if len(m.GetNexthopAddrs()) == 0 {
		return errors.New("nexthop_addrs is required")
	}
	if err := validateSourceID(m.GetSourceId()); err != nil {
		return err
	}

	return nil
}

func (m *FlushRoutesRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}

	return nil
}

func (m *Update) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}

	return nil
}

func validateSourceID(sourceID RouteSourceID) error {
	if sourceID == RouteSourceID_ROUTE_SOURCE_ID_UNKNOWN {
		return errors.New("source_id is required")
	}
	if _, ok := RouteSourceID_name[int32(sourceID)]; !ok {
		return fmt.Errorf("source_id unknown value %d", sourceID)
	}

	return nil
}
