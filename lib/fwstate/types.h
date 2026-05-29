#pragma once

#include <netinet/in.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "config.h"

#define FW_STATE_ADDR_TYPE_IP4 4
#define FW_STATE_ADDR_TYPE_IP6 6

#define FW_STATE_SYNC_THRESHOLD (uint64_t)8e9 // nanoseconds
#define FW_STATE_DEFAULT_TIMEOUT (uint64_t)120e9

// Nanosecond TTL stored in fw_state_value::last_ttl (48-bit, little-endian).
#define FWSTATE_TTL48_MAX ((uint64_t)((1ULL << 48) - 1))

/**
 * Common header shared by all fw_state key types.
 * Must be the first member so that any key pointer can be safely cast
 * to fw_state_key_hdr to access the 5-tuple transport fields.
 */
struct fw_state_key_hdr {
	uint16_t proto;
	uint16_t src_port;
	uint16_t dst_port;
	uint16_t _; // padding to align addresses on u64 boundary
};

struct fw4_state_key {
	struct fw_state_key_hdr hdr;
	uint32_t src_addr;
	uint32_t dst_addr;
};

// FIXME: ensure that during map allocations keys are aligned on u64 boundary
struct fw6_state_key {
	struct fw_state_key_hdr hdr;
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

/**
 * Per-connection TCP flag pair stored as a single byte.
 *
 * Layout (LSB first):
 *   bits [3:0] — src flags  (raw & 0x0F)
 *   bits [7:4] — dst flags  ((raw >> 4) & 0x0F)
 *
 * Each 4-bit nibble uses the fw_state_tcp_flags encoding:
 *   FIN = 0x01, SYN = 0x02, RST = 0x04, ACK = 0x08
 */
struct fw_state_flags {
	uint8_t src : 4;
	uint8_t dst : 4;
};

// Protocol-specific flags (e.g., TCP flags)
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
	union fw_state_flags_u flags;
	// TTL (ns) from the last put/sync frame; expiry is updated_at +
	// last_ttl.
	uint8_t last_ttl[6];
	// Timestamp when the state was created
	uint64_t created_at;
	// Timestamp when the last sync packet was emitted
	uint64_t updated_at;
	// Number of backward packets matching this state
	uint64_t packets_backward;
	// Number of forward packets matching this state
	uint64_t packets_forward;
};

/**
 * Firewall state synchronization frame structure.
 * From FreeBSD `sys/netinet/ip_fw.h`.
 *
 * This structure is used as the payload of UDP packets for
 * synchronizing firewall states between YANET instances.
 */
struct fw_state_sync_frame {
	uint32_t dst_ip;   // IPv4 destination address (big-endian)
	uint32_t src_ip;   // IPv4 source address (big-endian)
	uint16_t dst_port; // Destination port (little-endian)
	uint16_t src_port; // Source port (little-endian)
	uint8_t fib;	   // FIB/VRF identifier
	uint8_t proto;	   // Protocol (TCP/UDP/etc)
	union fw_state_flags_u flags;
	uint8_t addr_type;   // 4=IPv4, 6=IPv6
	uint8_t dst_ip6[16]; // IPv6 destination (big-endian)
	uint8_t src_ip6[16]; // IPv6 source (big-endian)
	uint32_t flow_id6;   // IPv6 flow label
	uint32_t extra;	     // Reserved for future use
} __attribute__((__packed__));

static inline void
fwstate_ttl48_store(uint8_t out[6], uint64_t ttl_ns) {
	out[0] = (uint8_t)(ttl_ns);
	out[1] = (uint8_t)(ttl_ns >> 8);
	out[2] = (uint8_t)(ttl_ns >> 16);
	out[3] = (uint8_t)(ttl_ns >> 24);
	out[4] = (uint8_t)(ttl_ns >> 32);
	out[5] = (uint8_t)(ttl_ns >> 40);
}

static inline uint64_t
fwstate_ttl48_load(const uint8_t in[6]) {
	return (uint64_t)in[0] | ((uint64_t)in[1] << 8) |
	       ((uint64_t)in[2] << 16) | ((uint64_t)in[3] << 24) |
	       ((uint64_t)in[4] << 32) | ((uint64_t)in[5] << 40);
}

static inline void
fwstate_value_set_last_ttl(struct fw_state_value *value, uint64_t ttl_ns) {
	// Configured timeouts must fit in last_ttl (see FWSTATE_TTL48_MAX).
	fwstate_ttl48_store(value->last_ttl, ttl_ns);
}

static inline uint64_t
fwstate_value_expires_at(const struct fw_state_value *value) {
	return value->updated_at + fwstate_ttl48_load(value->last_ttl);
}

static inline bool
fwstate_value_is_expired(const struct fw_state_value *value, uint64_t now) {
	if (value->updated_at == 0) {
		return true;
	}
	return fwstate_value_expires_at(value) <= now;
}

/// Select TTL for a frame from protocol and TCP flags.
/// Ported from yanet/dataplane/slow_worker.cpp:496.
static inline uint64_t
fwstate_entry_ttl(
	uint16_t proto, uint8_t raw_flags, const struct fwstate_timeouts *t
) {
	union fw_state_flags_u flags = {.raw = raw_flags};

	uint64_t ttl = t->default_;
	if (proto == IPPROTO_UDP) {
		ttl = t->udp;
	} else if (proto == IPPROTO_TCP) {
		ttl = t->tcp;

		uint8_t tcp_flags = flags.tcp.src | flags.tcp.dst;
		if (tcp_flags & FWSTATE_ACK) {
			ttl = t->tcp_syn_ack;
		} else if (tcp_flags & FWSTATE_SYN) {
			ttl = t->tcp_syn;
		}
		if (tcp_flags & FWSTATE_FIN) {
			ttl = t->tcp_fin;
		}
	}

	return ttl;
}
