#pragma once

#include <stdint.h>
#include <stdlib.h>

#define PACKET_HEADER_TYPE_UNKNOWN 0

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

struct pipeline;

struct packet {
	struct packet *next;

	struct rte_mbuf *mbuf;

	uint32_t pipeline_idx;

	uint32_t hash;

	uint16_t rx_device_id;
	uint16_t tx_device_id;

	uint16_t tx_result;

	uint16_t flags;
	uint16_t vlan;

	uint32_t flow_label; // 12 unused bits + 20 bits of the label

	struct network_header network_header;
	struct transport_header transport_header;
};

struct packet_list {
	struct packet *first;
	struct packet **last;
};

static inline void
packet_list_init(struct packet_list *list) {
	list->first = NULL;
	list->last = &list->first;
}

static inline void
packet_list_add(struct packet_list *list, struct packet *packet) {
	*list->last = packet;
	packet->next = NULL;
	list->last = &packet->next;
}

static inline struct packet *
packet_list_first(struct packet_list *list) {
	return list->first;
}

static inline void
packet_list_concat(struct packet_list *dst, struct packet_list *src) {
	// Nothing to do if src is empty
	if (src->first == NULL)
		return;

	// Replace dst with src if dst is empty
	if (dst->first == NULL) {
		*dst = *src;
		return;
	}

	*dst->last = packet_list_first(src);
	dst->last = src->last;
}

static inline struct packet *
packet_list_pop(struct packet_list *packets) {
	struct packet *res = packets->first;
	if (res == NULL)
		return res;

	packets->first = res->next;
	if (packets->first == NULL)
		packets->last = &packets->first;

	return res;
}

int
parse_ipv4_header(struct packet *packet, uint16_t *type, uint16_t *offset);

int
parse_ipv6_header(struct packet *packet, uint16_t *type, uint16_t *offset);

int
parse_packet(struct packet *packet);

static inline struct rte_mbuf *
packet_to_mbuf(const struct packet *packet) {
	return packet->mbuf;
}

struct packet *
mbuf_to_packet(struct rte_mbuf *mbuf);

void
packet_list_print(struct packet_list *list);

/**
 * @brief Count number of packets in a packet list
 *
 * Traverses the linked list of packets and counts total number.
 *
 * @param list Pointer to packet list structure to count
 * @return Total number of packets in the list
 */
int
packet_list_counter(struct packet_list *list);

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

struct ipv6_ext_2byte {
	uint8_t next_type;
	uint8_t size;
} __attribute__((__packed__));

struct ipv6_ext_fragment {
	uint8_t next_type;
	uint8_t reserved;
	uint16_t offset_flag;
	uint32_t identification;
} __attribute__((__packed__));
