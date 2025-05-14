package neigh

import (
	"github.com/vishvananda/netlink"
)

// NeighbourState is a type wrapper for Neighbor Cache Entry State.
//
// Used to provide state's string representation.
type NeighbourState int

// String returns string representation of this state.
func (m NeighbourState) String() string {
	switch m {
	case netlink.NUD_NONE:
		return "NONE"
	case netlink.NUD_INCOMPLETE:
		return "INCOMPLETE"
	case netlink.NUD_REACHABLE:
		return "REACHABLE"
	case netlink.NUD_STALE:
		return "STALE"
	case netlink.NUD_DELAY:
		return "DELAY"
	case netlink.NUD_PROBE:
		return "PROBE"
	case netlink.NUD_FAILED:
		return "FAILED"
	case netlink.NUD_NOARP:
		return "NOARP"
	case netlink.NUD_PERMANENT:
		return "PERMANENT"
	default:
		return "UNKNOWN"
	}
}
