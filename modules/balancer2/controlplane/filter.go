package balancer2

import (
	"bytes"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

type stateFilter struct {
	vip      []byte
	vsPort   *uint32
	proto    *balancerpb.TransportProto
	realIP   []byte
	realPort *uint32
}

func newStateFilter(filter *balancerpb.Filter) stateFilter {
	if filter == nil {
		return stateFilter{}
	}
	return stateFilter{
		vip:      filter.Vip,
		vsPort:   filter.VsPort,
		proto:    filter.Proto,
		realIP:   filter.RealIp,
		realPort: filter.RealPort,
	}
}

func (m stateFilter) matchVs(id *balancerpb.VsIdentifier) bool {
	if id == nil {
		return false
	}
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

func (m stateFilter) matchReal(id *balancerpb.RelativeRealIdentifier) bool {
	if id == nil {
		return false
	}
	if m.realIP != nil && !bytes.Equal(m.realIP, id.Ip) {
		return false
	}
	if m.realPort != nil && *m.realPort != id.Port {
		return false
	}
	return true
}
