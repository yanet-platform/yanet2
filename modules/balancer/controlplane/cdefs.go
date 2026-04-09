//go:generate sh -c "go tool cgo -godefs -- -I../../../ -I../../../filter -I../../../lib -I../../../modules/balancer/dataplane -I../../../modules/balancer/dataplane/types cdefs.go > ctypes.go"

//go:build ignore

package balancer

/*
#include "filter/rule.h"
#include "common/rcu.h"

#include "modules/balancer/dataplane/types/vs.h"
#include "modules/balancer/dataplane/types/stats.h"
#include "modules/balancer/dataplane/types/selector.h"
#include "modules/balancer/dataplane/types/real.h"
#include "modules/balancer/dataplane/types/sessions_tracker.h"
#include "modules/balancer/dataplane/types/session.h"
#include "modules/balancer/dataplane/dataplane.h"
#include "modules/balancer/controlplane/helpers/sessions.h"
*/
import "C"

// Network types.
type (
	Net4Addr C.struct_net4_addr
	Net6Addr C.struct_net6_addr
	NetAddr  C.struct_net_addr
	Net4     C.struct_net4
	Net6     C.struct_net6
	Net      C.struct_net
)

// Filter.
type (
	Filter C.struct_filter
)

// RCU.
type (
	RCU C.rcu_t
)

// Balancer core types.
type (
	VS                  C.struct_balancer_vs
	Real                C.struct_balancer_real
	AllowedSource       C.struct_balancer_vs_allowed_source
	SessionTimeouts     C.struct_balancer_session_timeouts
	PacketHandler       C.struct_balancer_packet_handler
	SessionTrackerShard C.struct_balancer_sessions_tracker_shard
	IntervalCounter     C.struct_balancer_interval_counter
	SessionTable        C.struct_balancer_session_table
	RealSelector        C.struct_balancer_real_selector
)

// Stats
type (
	L4Stats     C.struct_balancer_l4_stats
	IcmpStats   C.struct_balancer_icmp_stats
	CommonStats C.struct_balancer_common_stats
	VsStats     C.struct_balancer_vs_stats
	RealStats   C.struct_balancer_real_stats
)

// VS flags
const (
	VSFlagPureL3     = C.balancer_vs_pure_l3
	VSFlagFixMSS     = C.balancer_vs_fix_mss
	VSFlagGRE        = C.balancer_vs_gre
	VSFlagOPS        = C.balancer_vs_ops
	VSFlagWLC        = C.balancer_vs_wlc
	VSFlagRemoved    = C.balancer_vs_removed
	VSFlagRoundRobin = C.balancer_vs_round_robin
)

// Real flags
const (
	RealFlagEnabled = C.balancer_real_enabled
	RealFlagRemoved = C.balancer_real_removed
	RealFlagIPv6    = C.balancer_real_ipv6
)

const (
	MaxSessionTimeout         = uint32(C.balancer_max_session_timeout)
	AllowedSourceMaxTagLength = uint32(C.balancer_vs_acl_max_tag_len)
)

// Session iteration types.
type (
	SessionID        C.struct_balancer_session_id
	SessionState     C.struct_balancer_session_state
	SessionEntry     C.struct_balancer_session_entry
	SessionTableIter C.struct_balancer_session_table_iter
)

// Filter types.
type PortRange C.struct_filter_port_range
