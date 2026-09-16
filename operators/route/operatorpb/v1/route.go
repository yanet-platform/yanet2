package operatorpb

import "errors"

func (m *ShowRoutesRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}
	if m.GetIpv4Only() && m.GetIpv6Only() {
		return errors.New("ipv4_only and ipv6_only must not both be set")
	}

	return nil
}

func (m *LookupRouteRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *InsertRouteRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}
	if len(m.GetNexthopAddrs()) == 0 {
		return errors.New("nexthop_addrs is required")
	}
	if m.GetSourceId() == RouteSourceID_ROUTE_SOURCE_ID_BIRD && len(m.GetNexthopAddrs()) > 1 {
		return errors.New("nexthop_addrs must contain at most one address for non-static source")
	}

	return nil
}

func (m *DeleteRouteRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}
	if len(m.GetNexthopAddrs()) == 0 {
		return errors.New("nexthop_addrs is required")
	}

	return nil
}

func (m *FlushRoutesRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *Update) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}
