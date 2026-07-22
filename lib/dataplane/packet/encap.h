#pragma once

#include "dscp.h"
#include "packet.h"

/**
 * @brief Encapsulate a packet in an outer IPv4 header (IP-in-IP tunneling).
 *
 * Prepends an IPv4 header to the packet, deriving TOS, TTL and total length
 * from the existing inner IPv4 or IPv6 header. The outer protocol is set to
 * IPIP for an IPv4 inner and IPV6 for an IPv6 inner; the header checksum is
 * computed before prepending.
 *
 * The inner DSCP reaches the outer header only when dscp_flag is
 * DSCP_MARK_ALWAYS; the other values leave the outer DSCP at zero. The ECN
 * bits are copied either way.
 *
 * @param packet Packet whose current network header is IPv4 or IPv6.
 * @param dst Outer destination address (NET4_LEN bytes, network order).
 * @param src Outer source address (NET4_LEN bytes, network order).
 * @param dscp_flag One of the DSCP_MARK_* values.
 * @return 0 on success, -1 if the inner network type is unsupported or the
 *         mbuf cannot be extended.
 */
int
packet_ip4_encap(
	struct packet *packet,
	const uint8_t *dst,
	const uint8_t *src,
	uint8_t dscp_flag
);

/**
 * @brief Encapsulate a packet in an outer IPv6 header (IP-in-IP tunneling).
 *
 * Prepends an IPv6 header to the packet, deriving traffic class, hop limit
 * and payload length from the existing inner IPv4 or IPv6 header. The outer
 * next-header is set to IPIP for an IPv4 inner and IPV6 for an IPv6 inner.
 * The flow label is currently left unset.
 *
 * The inner DSCP reaches the outer header only when dscp_flag is
 * DSCP_MARK_ALWAYS; the other values leave the outer DSCP at zero. The ECN
 * bits are copied either way.
 *
 * @param packet Packet whose current network header is IPv4 or IPv6.
 * @param dst Outer destination address (NET6_LEN bytes, network order).
 * @param src Outer source address (NET6_LEN bytes, network order).
 * @param dscp_flag One of the DSCP_MARK_* values.
 * @return 0 on success, -1 if the inner network type is unsupported or the
 *         mbuf cannot be extended.
 */
int
packet_ip6_encap(
	struct packet *packet,
	const uint8_t *dst,
	const uint8_t *src,
	uint8_t dscp_flag
);

int
packet_mpls_encap(
	struct packet *packet,
	uint32_t label,
	uint8_t tc,
	uint8_t s,
	uint8_t ttl
);

int
packet_ip4_encap_udp(
	struct packet *packet,
	const uint8_t *src_ip,
	const uint8_t *dst_ip,
	const uint8_t *src_port,
	const uint8_t *dst_port
);

int
packet_ip6_encap_udp(
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

/**
 * @brief Copy the inner DSCP into the outer IPv4 header of a tunnel packet.
 *
 * Call after packet_ip4_encap(). The six DSCP bits of the inner header
 * replace those of the outer TOS byte; the outer ECN bits are preserved and
 * the outer header checksum is updated incrementally. Tunnel encapsulations
 * that insert a header between the two IP headers, such as GRE, are not
 * supported.
 *
 * @param packet Encapsulated packet whose network header is the outer IPv4
 *        header.
 * @return 0 on success, -1 if the payload is not an inner IP packet.
 */
int
packet_ip4_copy_inner_dscp(struct packet *packet);

/**
 * @brief Copy the inner DSCP into the outer IPv6 header of a tunnel packet.
 *
 * Call after packet_ip6_encap(). The six DSCP bits of the inner header
 * replace those of the outer traffic class; the outer ECN bits and the flow
 * label are preserved. Tunnel encapsulations that insert a header between the
 * two IP headers, such as GRE, are not supported.
 *
 * @param packet Encapsulated packet whose network header is the outer IPv6
 *        header.
 * @return 0 on success, -1 if the payload is not an inner IP packet.
 */
int
packet_ip6_copy_inner_dscp(struct packet *packet);