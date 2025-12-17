#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#define FW_STATE_ADDR_TYPE_IP4 4
#define FW_STATE_ADDR_TYPE_IP6 6

#define FW_STATE_SYNC_THRESHOLD (uint64_t)8e9 // nanoseconds
#define FW_STATE_DEFAULT_TIMEOUT (uint64_t)120e9

struct fw4_state_key {
	uint16_t proto;
	uint16_t src_port;
	uint16_t dst_port;
	uint16_t _;
	uint32_t src_addr;
	uint32_t dst_addr;
};

// FIXME: ensure that during map allocations keys are aligned on u64 boundary
struct fw6_state_key {
	uint16_t proto;
	uint16_t src_port;
	uint16_t dst_port;
	uint16_t _; // Align stride addr and src/dst addrs on u64 boundary
	uint8_t src_addr[16];
	uint8_t dst_addr[16];
};

enum fw_state_tcp_flags {
	// NOLINTBEGIN(readability-identifier-naming)
	FWSTATE_FIN = 0x01,
	FWSTATE_SYN = 0x02,
	FWSTATE_RST = 0x04,
	FWSTATE_ACK = 0x08,
	// NOLINTEND(readability-identifier-naming)
};

static inline uint8_t
fwstate_flags_from_tcp(uint8_t tcp_flags) {
	//
	// RTE_TCP_ACK_FLAG 0x10
	// RTE_TCP_PSH_FLAG 0x08
	// RTE_TCP_RST_FLAG 0x04
	// RTE_TCP_SYN_FLAG 0x02
	// RTE_TCP_FIN_FLAG 0x01
	//
	// https://datatracker.ietf.org/doc/html/rfc9293#name-header-format
	//  0                   1                   2                   3
	//  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
	//  ...
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	// |  Data |       |C|E|U|A|P|R|S|F|                               |
	// | Offset| Reserv|W|C|R|C|S|S|Y|I|            Window             |
	// |       |       |R|E|G|K|H|T|N|N|                               |
	// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	// ...                    + - ^ ^ ^
	// fwstate stores only ACK, RST, SYN, and FIN flags; PSH is replaced
	// with ACK.

	return (tcp_flags & 7) | ((tcp_flags >> 1) & FWSTATE_ACK);
}

struct fw_state_flags {
	uint8_t src : 4;
	uint8_t dst : 4;
};

union fw_state_flags_u {
	struct fw_state_flags tcp;
	uint8_t raw;
};

/**
 * Firewall state value structure.
 * Stores the state information for a firewall connection.
 */
struct fw_state_value {
	bool external; // State ownership (internal/external)
	uint8_t type;  // Transport protocol type (TCP/UDP)
	union fw_state_flags_u flags;
	// Number of packets since last sync
	uint32_t packets_since_last_sync;
	// Timestamp when the last sync packet was emitted
	uint64_t last_sync;
	// Number of backward packets matching this state
	uint64_t packets_backward;
	// Number of forward packets matching this state
	uint64_t packets_forward;
};

/**
 * Firewall state synchronization frame structure.
 * From FreeBSD `sys/netinet/ip_fw.h`.
 *
 * Note that all fields except IPv6 addresses are little-endian.
 * This structure is used as the payload of UDP packets for
 * synchronizing firewall states between YANET instances.
 */
struct fw_state_sync_frame {
	uint32_t dst_ip;   // IPv4 destination (little-endian)
	uint32_t src_ip;   // IPv4 source (little-endian)
	uint16_t dst_port; // Destination port (little-endian)
	uint16_t src_port; // Source port (little-endian)
	uint8_t fib;	   // FIB/VRF identifier
	uint8_t proto;	   // Protocol (TCP/UDP/etc)
	union fw_state_flags_u
		flags;	     // Protocol-specific flags (e.g., TCP flags)
	uint8_t addr_type;   // 4=IPv4, 6=IPv6
	uint8_t dst_ip6[16]; // IPv6 destination (network byte order)
	uint8_t src_ip6[16]; // IPv6 source (network byte order)
	uint32_t flow_id6;   // IPv6 flow label
	uint32_t extra;	     // Reserved for future use
} __attribute__((__packed__));
