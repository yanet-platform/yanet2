#pragma once

#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

#define PACKET_HEADER_TYPE_UNKNOWN 0

#define PACKET_RECIRC_LIMIT_DEFAULT UINT16_C(64)
#define PACKET_RECIRC_LIMIT_MIN UINT16_C(4)
#define PACKET_RECIRC_LIMIT_MAX UINT16_C(256)

enum packet_flag {
	PACKET_FLAG_FRAGMENTED,
	PACKET_FLAG_FWSTATE_SYNC_INTERNAL,
};

struct rte_mbuf;

struct packet_header {
	uint16_t type;
	uint16_t ext_type;
	uint16_t offset;
	uint16_t pad;
};

struct network_header {
	uint16_t type;
	uint16_t offset;
};

struct transport_header {
	uint16_t type;
	uint16_t offset;
};

struct packet {
	struct packet *next;

	struct rte_mbuf *mbuf;

	uint32_t hash;

	uint16_t rx_device_id;
	uint16_t tx_device_id;
	uint16_t module_device_id;

	uint16_t flags;
	uint16_t vlan;

	uint16_t recirc_remaining;
	uint8_t recirc_initialized;

	uint32_t flow_label; // 12 unused bits + 20 bits of the label

	uint16_t fragment_offset;

	// Cached first-segment byte length; mirrors the mbuf so packet_front
	// can sum bytes without touching DPDK.
	uint16_t data_len;

	struct network_header network_header;
	struct transport_header transport_header;
};

// Initialize a packet lineage's redirect credits once. Lazy because packets
// enter a pipeline before its module execution context supplies the configured
// limit, and the field is treated as "full budget" by every reader until the
// first explicit consumption. Callers must invoke this before the first
// redirect consumption on a freshly allocated packet, including after a
// successful clone.
static inline void
packet_recirc_init(struct packet *packet, uint16_t limit) {
	if (!packet->recirc_initialized) {
		packet->recirc_remaining = limit;
		packet->recirc_initialized = 1;
	}
}

// Consume one redirect from the packet lineage's assigned credits.
static inline bool
packet_recirc_try_redirect(struct packet *packet, uint16_t limit) {
	packet_recirc_init(packet, limit);

	if (packet->recirc_remaining == 0) {
		return false;
	}

	packet->recirc_remaining -= 1;
	return true;
}

struct packet_list {
	struct packet *first;
	struct packet **last;
};

static inline void
packet_list_init(struct packet_list *list) {
	list->first = NULL;
	list->last = NULL;
}

static inline void
packet_list_add(struct packet_list *list, struct packet *packet) {
	packet->next = NULL;
	if (list->last == NULL) {
		list->last = &list->first;
	}
	*list->last = packet;
	list->last = &packet->next;
}

static inline struct packet *
packet_list_first(struct packet_list *list) {
	return list->first;
}

// Move every packet from src into dst, leaving src empty.
static inline void
packet_list_concat(struct packet_list *dst, struct packet_list *src) {
	// Nothing to do if src is empty
	if (src->first == NULL) {
		return;
	}

	// Replace dst with src if dst is empty
	if (dst->first == NULL) {
		*dst = *src;
		packet_list_init(src);
		return;
	}

	*dst->last = packet_list_first(src);
	dst->last = src->last;

	packet_list_init(src);
}

static inline struct packet *
packet_list_pop(struct packet_list *packets) {
	struct packet *res = packets->first;
	if (res == NULL) {
		return res;
	}

	packets->first = res->next;
	if (packets->first == NULL) {
		packets->last = NULL;
	}

	return res;
}

static inline uint64_t
packet_list_count(struct packet_list *packets) {
	uint64_t count = 0;
	for (struct packet *pkt = packets->first; pkt != NULL;
	     pkt = pkt->next) {
		count += 1;
	}
	return count;
}

static inline uint64_t
packet_list_bytes(struct packet_list *packets) {
	uint64_t bytes = 0;

	for (struct packet *pkt = packets->first; pkt != NULL;
	     pkt = pkt->next) {
		bytes += pkt->data_len;
	}

	return bytes;
}
int
parse_ipv4_header(struct packet *packet, uint16_t *type, uint16_t *offset);

int
parse_ipv6_header(struct packet *packet, uint16_t *type, uint16_t *offset);

int
parse_packet(struct packet *packet);

void
packet_list_print(struct packet_list *list);

/**
 * @brief Print contents of an rte_mbuf packet in a detailed format if
 * ENABLE_TRACE_LOG is defined
 *
 * Prints detailed information about DPDK mbuf packet contents using LOG_TRACE
 * including:
 * - Ethernet header fields (MAC addresses, ether type)
 * - ARP header fields (if packet is ARP)
 * - IP header fields (v4 or v6, including addresses, protocol, TTL/hop limit)
 * - Protocol header fields:
 *   - UDP (ports, length, checksum)
 *   - TCP (ports, sequence numbers, flags, window)
 *   - ICMP (type, code, checksum)
 *   - ICMPv6 (type, code, checksum)
 * - Final packet data length
 *
 * Used for detailed packet inspection during debugging, development and
 * verification of packet processing.
 *
 * @param mbuf Pointer to the DPDK mbuf structure containing packet to print
 */
void
logtrace_rte_mbuf(struct rte_mbuf *mbuf);

struct yanet_ipv6_ext_2byte {
	uint8_t next_header;
	uint8_t extension_length;
} __attribute__((__packed__));

struct yanet_ipv6_ext_fragment {
	uint8_t next_header;
	uint8_t reserved;
	uint16_t offset_flag;
	uint32_t identification;
} __attribute__((__packed__));
