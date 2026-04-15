package balancer

import (
	"bytes"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

func validateFilter(filter *balancerpb.Filter) error {
	if filter == nil {
		return nil
	}
	if filter.Proto != nil {
		switch *filter.Proto {
		case balancerpb.TransportProto_TCP, balancerpb.TransportProto_UDP:
		default:
			return NewError("invalid filter proto: %v", *filter.Proto)
		}
	}
	return nil
}

type filterMatcher struct {
	vip      []byte
	vsPort   *uint32
	proto    *balancerpb.TransportProto
	realIP   []byte
	realPort *uint32

	hasVsFilter   bool
	hasRealFilter bool
}

func newFilterMatcher(filter *balancerpb.Filter) filterMatcher {
	if filter == nil {
		return filterMatcher{}
	}
	m := filterMatcher{}
	if filter.Vip != nil {
		m.vip = filter.Vip
		m.hasVsFilter = true
	}
	if filter.VsPort != nil {
		m.vsPort = filter.VsPort
		m.hasVsFilter = true
	}
	if filter.Proto != nil {
		m.proto = filter.Proto
		m.hasVsFilter = true
	}
	if filter.RealIp != nil {
		m.realIP = filter.RealIp
		m.hasRealFilter = true
	}
	if filter.RealPort != nil {
		m.realPort = filter.RealPort
		m.hasRealFilter = true
	}
	return m
}

func (m *filterMatcher) matchVsID(id *balancerpb.VsIdentifier) bool {
	if m.vip != nil && !bytes.Equal(m.vip, id.Addr) {
		return false
	}
	if m.vsPort != nil && *m.vsPort != id.Port {
		return false
	}
	if m.proto != nil && *m.proto != id.Proto {
		return false
	}
	return true
}

func (m *filterMatcher) matchRealID(id *balancerpb.RelativeRealIdentifier) bool {
	if m.realIP != nil && !bytes.Equal(m.realIP, id.Ip) {
		return false
	}
	if m.realPort != nil && *m.realPort != id.Port {
		return false
	}
	return true
}
