#pragma once

#include <stdint.h>
#include <stdlib.h>

#include <rte_mbuf.h>

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

struct packet {
	struct packet *next;

	struct rte_mbuf *mbuf;

	uint32_t hash;

	uint16_t rx_device_id;
	uint16_t device_id;

	uint16_t module_device_id;

	uint16_t tx_result;

	uint16_t vlan;

	uint32_t flow_label; // 12 unused bits + 20 bits of the label

	struct network_header network_header;
	struct transport_header transport_header;
};

static inline struct rte_mbuf *
packet_to_mbuf(const struct packet *packet) {
	return packet->mbuf;
}

static inline struct packet *
mbuf_to_packet(struct rte_mbuf *mbuf) {
	return (struct packet *)((void *)mbuf->buf_addr);
}

static inline uint16_t
packet_data_len(struct packet *packet) {
	return rte_pktmbuf_data_len(packet_to_mbuf(packet));
}

int
parse_ipv4_header(struct packet *packet, uint16_t *type, uint16_t *offset);

int
parse_ipv6_header(struct packet *packet, uint16_t *type, uint16_t *offset);

int
parse_packet(struct packet *packet);

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
