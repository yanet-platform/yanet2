package croutempls

import (
	"net/netip"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
)

// Kind identifies the forwarding action for an MPLS nexthop.
type Kind uint16

const (
	// KindNone indicates a drop/no-route action.
	KindNone Kind = iota
	// KindTun indicates a tunnel encapsulation action.
	KindTun
)

// Nexthop describes a single MPLS forwarding nexthop.
type Nexthop struct {
	// Kind identifies the forwarding action.
	Kind Kind
	// Source is the tunnel source IP address.
	Source netip.Addr
	// Destination is the tunnel destination IP address.
	Destination netip.Addr
	// MPLSLabel is the outgoing MPLS label.
	MPLSLabel uint32
	// Weight is the ECMP load-balancing weight.
	Weight uint64
	// Counter is the counter name for traffic accounting.
	Counter string
}

// Rule describes a single route-mpls forwarding rule with its prefix
// match criteria and nexthop list.
type Rule struct {
	// Dst4s is the set of IPv4 destination prefixes to match.
	Dst4s filter.IPNets
	// Dst6s is the set of IPv6 destination prefixes to match.
	Dst6s filter.IPNets
	// Nexthops is the ordered list of nexthops for this rule.
	Nexthops []Nexthop
}
