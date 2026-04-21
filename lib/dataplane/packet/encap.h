#pragma once

#include "packet.h"

int
packet_ip4_encap(struct packet *packet, const uint8_t *dst, const uint8_t *src);

int
packet_ip6_encap(struct packet *packet, const uint8_t *dst, const uint8_t *src);

int
packet_mpls_encap(
	struct packet *packet,
	uint32_t label,
	uint8_t tc,
	uint8_t s,
	uint8_t ttl
);

int
packet_encap_ip4_udp(
	struct packet *packet,
	const uint8_t *src_ip,
	const uint8_t *dst_ip,
	const uint8_t *src_port,
	const uint8_t *dst_port
);

int
packet_encap_ip6_udp(
	struct packet *packet,
	const uint8_t *src_addr,
	const uint8_t *dst_addr,
	const uint8_t *src_port,
	const uint8_t *dst_port
);

/**
 * @brief Encapsulate a packet into an outer IPv4 + GRE tunnel
 *
 * Prepends an outer IPv4 header (protocol = GRE) and a GRE header in front
 * of the existing inner IPv4 or IPv6 packet. Inner DSCP/TTL are copied into
 * the outer header; the GRE protocol field is set from the inner EtherType.
 * The outer IPv4 header checksum is computed.
 *
 * @param packet Packet whose current network header is IPv4 or IPv6
 * @param dst Outer IPv4 destination address (4 bytes)
 * @param src Outer IPv4 source address (4 bytes)
 * @return 0 on success, -1 if the inner network type is unsupported or
 *         prepending the outer header failed
 */
int
packet_ip4_encap_gre(
	struct packet *packet, const uint8_t *dst, const uint8_t *src
);

/**
 * @brief Encapsulate a packet into an outer IPv6 + GRE tunnel
 *
 * Prepends an outer IPv6 header (next header = GRE) and a GRE header in
 * front of the existing inner IPv4 or IPv6 packet. Inner traffic
 * class/hop-limit are copied into the outer header; the GRE protocol field
 * is set from the inner EtherType.
 *
 * @param packet Packet whose current network header is IPv4 or IPv6
 * @param dst Outer IPv6 destination address (16 bytes)
 * @param src Outer IPv6 source address (16 bytes)
 * @return 0 on success, -1 if the inner network type is unsupported or
 *         prepending the outer header failed
 */
int
packet_ip6_encap_gre(
	struct packet *packet, const uint8_t *dst, const uint8_t *src
);