#pragma once

#include "common/network.h"

#include <stddef.h>
#include <stdint.h>

/**
 * Virtual service feature flags.
 *
 * These flags control various aspects of virtual service behavior including
 * encapsulation method, packet modifications, and routing mode.
 */

/**
 * Pure Layer 3 routing mode flag.
 *
 * When set, the virtual service matches ALL traffic with the specified IP
 * address and transport protocol, regardless of destination port.
 *
 * BEHAVIOR:
 * - Virtual service port MUST be 0 (configuration rejected otherwise)
 * - Matches traffic to ANY port for the specified IP and protocol
 * - Packets are forwarded to reals using the client's original destination port
 * - No two pure L3 services can have the same (IP, protocol) combination
 *
 * USE CASES:
 * - Load balancing all traffic to an IP regardless of port
 * - Transparent proxying scenarios
 * - When port-based routing is not needed
 *
 * STANDARD MODE (flag not set):
 * - Virtual service port can be any valid value (1-65535)
 * - Matches traffic to the specific (IP, port, protocol) combination
 * - Packets are forwarded to reals using the virtual service port
 */
#define VS_PURE_L3_FLAG ((uint8_t)(1ull << 0))

/**
 * Fix TCP MSS (Maximum Segment Size) option flag.
 *
 * When set, the balancer adjusts the TCP MSS option in SYN packets to
 * account for encapsulation overhead (IPIP or GRE), preventing packet
 * fragmentation.
 *
 * BEHAVIOR:
 * - Inspects TCP SYN packets for MSS option
 * - Reduces MSS value by encapsulation overhead:
 *   * IPIP: 20 bytes (IPv4) or 40 bytes (IPv6)
 *   * GRE: 24 bytes (IPv4) or 44 bytes (IPv6)
 * - Ensures end-to-end MTU compatibility
 *
 * RECOMMENDATION:
 * - Enable when using tunneling (IPIP or GRE)
 * - Prevents fragmentation issues
 * - Improves TCP performance
 */
#define VS_FIX_MSS_FLAG ((uint8_t)(1ull << 1))

/**
 * Use GRE encapsulation flag.
 *
 * When set, packets are tunneled to real servers using GRE (Generic
 * Routing Encapsulation) instead of IPIP (IP-in-IP).
 *
 * COMPARISON:
 * - GRE: More flexible, can carry additional metadata, 4 extra bytes overhead
 * - IPIP: Simpler, lower overhead, less flexible
 *
 * OVERHEAD:
 * - GRE adds 24 bytes (IPv4) or 44 bytes (IPv6) to packet size
 * - IPIP adds 20 bytes (IPv4) or 40 bytes (IPv6) to packet size
 *
 * RECOMMENDATION:
 * - Use GRE when you need protocol flexibility
 * - Use IPIP (flag not set) for lower overhead
 */
#define VS_GRE_FLAG ((uint8_t)(1ull << 2))

/**
 * One Packet Scheduling (OPS) mode flag.
 *
 * When set, each packet is independently scheduled to a real server
 * without creating or tracking sessions. This is useful for stateless
 * protocols or when session tracking is not needed.
 *
 * BEHAVIOR WHEN SET:
 * - No session table entries created
 * - Each packet scheduled independently
 * - Scheduler algorithm still applies (source_hash or round_robin)
 * - Lower memory usage (no session state)
 * - Lower CPU usage (no session lookups)
 *
 * BEHAVIOR WHEN NOT SET:
 * - Sessions are created and tracked
 * - All packets of a connection go to the same real server
 * - Session table memory required
 * - Session lookup overhead per packet
 *
 * USE CASES:
 * - Stateless protocols (e.g., DNS queries)
 * - When session affinity is not required
 * - High packet rate, short-lived connections
 * - Memory-constrained environments
 *
 * LIMITATIONS:
 * - No session affinity (same client may hit different reals)
 * - Cannot track connection state
 * - May cause issues with stateful protocols
 */
#define VS_OPS_FLAG ((uint8_t)(1ull << 3))

/**
 * Identifier of a virtual service.
 *
 * Uniquely identifies a load-balanced service by its network address,
 * transport protocol, and destination port. This combination defines
 * which traffic will be matched and load-balanced.
 *
 * PORT SEMANTICS:
 * - Standard mode: port specifies the exact service port (1-65535)
 * - Pure L3 mode (VS_PURE_L3_FLAG): port MUST be 0, matches all ports
 */
struct vs_identifier {
	/**
	 * Virtual service IP address (IPv4 or IPv6).
	 *
	 * This is the address clients connect to. Traffic destined for
	 * this address will be load-balanced across real servers.
	 */
	struct net_addr addr;

	/**
	 * IP protocol version indicator.
	 *
	 * Values:
	 * - 0: IPPROTO_IP (IPv4)
	 * - 41: IPPROTO_IPV6 (IPv6)
	 *
	 * Derived from the address type and used for protocol-specific
	 * processing.
	 */
	uint8_t ip_proto;

	/**
	 * Destination port for the virtual service.
	 *
	 * STANDARD MODE (VS_PURE_L3_FLAG not set):
	 * - Valid range: 1-65535
	 * - Matches traffic to this specific port
	 * - Forwarded packets use this port (unless real has port override)
	 *
	 * PURE L3 MODE (VS_PURE_L3_FLAG set):
	 * - MUST be 0 (configuration rejected otherwise)
	 * - Matches traffic to ANY port
	 * - Forwarded packets preserve client's original destination port
	 */
	uint16_t port;

	/**
	 * Transport layer protocol.
	 *
	 * Values:
	 * - 6: IPPROTO_TCP
	 * - 17: IPPROTO_UDP
	 *
	 * Determines which transport protocol traffic will be matched
	 * and how sessions are tracked (TCP state machine vs UDP timeout).
	 */
	uint8_t transport_proto;
};

/**
 * Virtual service scheduler algorithm.
 *
 * Determines how new connections/flows are distributed across real servers.
 * The scheduler runs when a new session is created or when OPS mode is used.
 *
 * WEIGHT CONSIDERATION:
 * Both algorithms respect real server weights when making selections.
 * Higher weight reals receive proportionally more traffic.
 */
enum vs_scheduler {
	/**
	 * Source hash scheduling.
	 *
	 * Selects real server based on a hash of the client's source
	 * address and port. Provides stable, consistent routing where
	 * the same client always hits the same real server.
	 *
	 * CHARACTERISTICS:
	 * - Deterministic: Same client → same real
	 * - Session affinity across connections
	 * - Good for caching scenarios
	 * - Distribution depends on client diversity
	 *
	 * ALGORITHM:
	 * hash = hash(client_ip, client_port)
	 * real = weighted_selection(hash, reals, weights)
	 */
	source_hash = 0,

	/**
	 * Round-robin scheduling.
	 *
	 * Rotates through real servers for successive new flows,
	 * distributing load evenly regardless of client identity.
	 *
	 * CHARACTERISTICS:
	 * - Non-deterministic: Same client may hit different reals
	 * - Even distribution across reals
	 * - No session affinity across connections
	 * - Good for stateless services
	 *
	 * ALGORITHM:
	 * counter = atomic_increment(vs_counter)
	 * real = weighted_selection(counter, reals, weights)
	 */
	round_robin = 1,
};

struct named_real_config;

/**
 * Static configuration of a virtual service.
 *
 * Defines all parameters for a load-balanced service including behavior
 * flags, scheduling algorithm, real server backends, and access control.
 *
 * MEMORY MANAGEMENT:
 * - Caller allocates and manages all arrays (reals, allowed_src, peers)
 * - Arrays must remain valid for the lifetime of the configuration
 * - Use balancer_update_packet_handler() to apply changes
 */
struct vs_config {
	/**
	 * Feature flags bitmask.
	 *
	 * Combination of VS_* flags controlling virtual service behavior:
	 * - VS_PURE_L3_FLAG: Match all ports, preserve client port
	 * - VS_FIX_MSS_FLAG: Adjust TCP MSS for tunnel overhead
	 * - VS_GRE_FLAG: Use GRE encapsulation instead of IPIP
	 * - VS_OPS_FLAG: One-packet scheduling, no session tracking
	 *
	 * Multiple flags can be combined with bitwise OR.
	 */
	uint8_t flags;

	/**
	 * Scheduling algorithm for new connections.
	 *
	 * Determines how new sessions/flows are distributed across
	 * real servers. See vs_scheduler enum for details.
	 */
	enum vs_scheduler scheduler;

	/** Number of real servers in the 'reals' array */
	size_t real_count;

	/**
	 * Array of real server configurations.
	 *
	 * Each entry defines a backend server including:
	 * - Server address and port
	 * - Weight for load distribution
	 * - Source address for forwarded packets
	 *
	 * REQUIREMENTS:
	 * - At least one real server must be configured
	 * - Array length must match real_count
	 *
	 * Ownership: Caller allocates and manages this array
	 */
	struct named_real_config *reals;

	/** Number of allowed source ranges in 'allowed_src' array */
	size_t allowed_src_count;

	/**
	 * Client source address allowlist (optional).
	 *
	 * When configured, only traffic from these source address ranges
	 * will be accepted. Traffic from other sources is dropped and
	 * counted in vs_stats.packet_src_not_allowed.
	 *
	 * BEHAVIOR:
	 * - If NULL or count=0: All sources allowed (no filtering)
	 * - If configured: Only listed CIDR ranges allowed
	 * - Supports both IPv4 and IPv6 ranges
	 *
	 * USE CASES:
	 * - Restricting access to trusted networks
	 * - Implementing IP-based access control
	 * - Security hardening
	 *
	 * Ownership: Caller allocates and manages this array
	 */
	struct net_addr_range *allowed_src;

	/** Number of IPv4 peer balancers in 'peers_v4' array */
	size_t peers_v4_count;

	/**
	 * IPv4 peer balancer addresses for ICMP coordination.
	 *
	 * In multi-balancer deployments, ICMP error packets may be
	 * broadcasted to peer balancers for proper error handling
	 * and session synchronization.
	 *
	 * BEHAVIOR:
	 * - ICMP errors are cloned and sent to all peers
	 * - Peers can forward errors to appropriate real servers
	 * - Enables distributed ICMP error handling
	 *
	 * Ownership: Caller allocates and manages this array
	 */
	struct net4_addr *peers_v4;

	/** Number of IPv6 peer balancers in 'peers_v6' array */
	size_t peers_v6_count;

	/**
	 * IPv6 peer balancer addresses for ICMP coordination.
	 *
	 * Same as peers_v4 but for IPv6 deployments. See peers_v4
	 * documentation for behavior details.
	 *
	 * Ownership: Caller allocates and manages this array
	 */
	struct net6_addr *peers_v6;
};

/**
 * Virtual service configuration paired with its identifier.
 *
 * Combines the unique identifier (address, port, protocol) with the
 * complete configuration (flags, reals, scheduling, etc.) for a
 * virtual service.
 */
struct named_vs_config {
	/** Virtual service identifier (address, port, protocol) */
	struct vs_identifier identifier;

	/** Virtual service configuration (flags, reals, scheduling) */
	struct vs_config config;
};

/**
 * Per-virtual-service runtime counters.
 *
 * Tracks packet processing statistics for a specific virtual service,
 * including successful forwards, various failure conditions, and
 * session management metrics.
 */
struct vs_stats {
	/** Total packets received matching this virtual service */
	uint64_t incoming_packets;

	/** Total bytes received matching this virtual service (IP layer) */
	uint64_t incoming_bytes;

	/**
	 * Packets dropped due to source address not in allowlist.
	 *
	 * Incremented when:
	 * - vs_config.allowed_src is configured (not NULL)
	 * - Client source address doesn't match any allowed range
	 * - Packet is dropped before scheduling
	 */
	uint64_t packet_src_not_allowed;

	/**
	 * Packets that failed real server selection.
	 *
	 * Incremented when:
	 * - No real servers are configured
	 * - All real servers are disabled
	 * - All real servers have zero weight
	 * - Scheduler cannot select a valid real
	 */
	uint64_t no_reals;

	/**
	 * One-Packet Scheduling packets sent without session creation.
	 *
	 * Incremented when:
	 * - VS_OPS_FLAG is set
	 * - Packet is forwarded to a real
	 * - No session table entry is created
	 *
	 * This counter tracks stateless packet forwarding.
	 */
	uint64_t ops_packets;

	/**
	 * Session creation failures due to table capacity.
	 *
	 * Incremented when:
	 * - Session table is full (at capacity)
	 * - New session cannot be allocated
	 * - Packet is dropped
	 *
	 * MITIGATION:
	 * - Increase session table capacity
	 * - Enable auto-resize with appropriate max_load_factor
	 * - Review session timeout configuration
	 */
	uint64_t session_table_overflow;

	/**
	 * ICMP echo request/reply packets processed.
	 *
	 * Tracks ICMP echo (ping) packets that matched this virtual
	 * service and were handled by the balancer.
	 */
	uint64_t echo_icmp_packets;

	/**
	 * ICMP error packets forwarded to real servers.
	 *
	 * Tracks ICMP errors (destination unreachable, time exceeded,
	 * etc.) that were matched to sessions and forwarded to the
	 * appropriate real server.
	 */
	uint64_t error_icmp_packets;

	/**
	 * Packets for sessions where the real server is disabled.
	 *
	 * Incremented when:
	 * - Session exists for a specific real
	 * - That real is currently disabled
	 * - Packet arrives for the session
	 *
	 * These packets are typically dropped or rescheduled depending
	 * on configuration.
	 */
	uint64_t real_is_disabled;

	/**
	 * Packets for sessions where the real server was removed.
	 *
	 * Incremented when:
	 * - Session exists for a specific real
	 * - That real is no longer in the configuration
	 * - Packet arrives for the session
	 *
	 * This can occur after configuration updates that remove reals.
	 * Sessions are eventually cleaned up by timeout.
	 */
	uint64_t real_is_removed;

	/**
	 * Packets that couldn't be rescheduled.
	 *
	 * Incremented when:
	 * - No existing session found
	 * - Packet doesn't start a new session (e.g., TCP non-SYN)
	 * - Packet is dropped
	 *
	 * Common for:
	 * - TCP packets without SYN flag when no session exists
	 * - Packets arriving after session timeout
	 */
	uint64_t not_rescheduled_packets;

	/**
	 * ICMP packets broadcasted to peer balancers.
	 *
	 * Incremented when:
	 * - ICMP error has this VS as source
	 * - Packet is cloned and sent to configured peers
	 * - Used for distributed ICMP error handling
	 *
	 * Requires vs_config.peers_v4 or peers_v6 to be configured.
	 */
	uint64_t broadcasted_icmp_packets;

	/**
	 * Total sessions created for this virtual service.
	 *
	 * Tracks the cumulative number of sessions created since
	 * the balancer started or statistics were reset. Does not
	 * include OPS packets (which don't create sessions).
	 */
	uint64_t created_sessions;

	/** Packets successfully forwarded to real servers */
	uint64_t outgoing_packets;

	/** Bytes successfully forwarded to real servers (IP layer) */
	uint64_t outgoing_bytes;
};

/**
 * Virtual service statistics with identifier.
 *
 * Associates statistics with a specific virtual service and includes
 * per-real statistics for all reals backing this VS.
 *
 * MEMORY MANAGEMENT:
 * - The 'reals' array is heap-allocated
 * - Must be freed by caller (typically via balancer_stats_free())
 */
struct named_vs_stats {
	/** Virtual service identifier */
	struct vs_identifier identifier;

	/** Statistics for this virtual service */
	struct vs_stats stats;

	/** Number of real servers in the 'reals' array */
	size_t reals_count;

	/**
	 * Per-real statistics for all reals backing this virtual service.
	 *
	 * Array length matches reals_count. Order corresponds to the
	 * configuration order of reals in the virtual service.
	 */
	struct named_real_stats *reals;
};

/**
 * Virtual service runtime information with identifier.
 *
 * Provides runtime information about a specific virtual service including
 * active session count, last activity, and per-real information.
 *
 * MEMORY MANAGEMENT:
 * - The 'reals' array is heap-allocated
 * - Must be freed by caller (typically via balancer_info_free())
 *
 * DATA FRESHNESS:
 * - Session counts updated during periodic refresh (if enabled)
 * - May lag behind actual current state by up to refresh_period
 * - last_packet_timestamp updated in real-time by dataplane
 */
struct named_vs_info {
	/** Virtual service identifier */
	struct vs_identifier identifier;

	/**
	 * Timestamp of the last packet processed for this virtual service.
	 *
	 * Monotonic timestamp (seconds since boot) of when any packet
	 * matched this virtual service. Updated in real-time by the
	 * dataplane.
	 *
	 * Useful for:
	 * - Detecting inactive services
	 * - Monitoring traffic patterns
	 * - Identifying stale configurations
	 */
	uint32_t last_packet_timestamp;

	/**
	 * Number of active sessions for this virtual service.
	 *
	 * This is the sum of active sessions across all real servers
	 * backing this virtual service.
	 *
	 * UPDATE FREQUENCY:
	 * - Updated asynchronously during periodic refresh
	 * - Controlled by StateConfig.refresh_period
	 * - May lag behind actual state by up to refresh_period
	 *
	 * NOTE: Represents sessions tracked by the balancer, not
	 * necessarily all active connections to real servers (which may
	 * have additional direct connections).
	 */
	size_t active_sessions;

	/** Number of real servers in the 'reals' array */
	size_t reals_count;

	/**
	 * Runtime information for each real server backing this VS.
	 *
	 * Provides per-real session counts and activity timestamps.
	 * Array length matches reals_count. Order corresponds to the
	 * configuration order of reals in the virtual service.
	 */
	struct named_real_info *reals;
};
