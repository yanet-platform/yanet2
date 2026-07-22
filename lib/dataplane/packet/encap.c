#include "encap.h"

#include <string.h>

#include <rte_ether.h>
#include <rte_gre.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "common/checksum.h"
#include "common/network.h"
#include "lib/dataplane/packet/data.h"

int
packet_prepend(struct packet *packet, const void *header, const size_t size) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (rte_pktmbuf_prepend(mbuf, size) == NULL)
		return -1;
	memcpy(rte_pktmbuf_mtod(mbuf, char *), header, size);

	packet->network_header.offset += size;
	packet->transport_header.offset += size;
	packet_refresh_data_len(packet);

	return 0;
}

static int
packet_network_prepend(
	struct packet *packet,
	uint16_t type,
	const void *header,
	const size_t size
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (rte_pktmbuf_prepend(mbuf, size) == NULL)
		return -1;
	memmove(rte_pktmbuf_mtod(mbuf, char *),
		rte_pktmbuf_mtod_offset(mbuf, char *, size),
		packet->network_header.offset);
	memcpy(rte_pktmbuf_mtod_offset(
		       mbuf, char *, packet->network_header.offset
	       ),
	       header,
	       size);

	packet->network_header.type = type;

	packet->transport_header.offset += size;
	packet_refresh_data_len(packet);

	// FIXME previous header type (ex: vlan)
	uint16_t *next_hdr_type = rte_pktmbuf_mtod_offset(
		mbuf, uint16_t *, packet->network_header.offset - 2
	);
	*next_hdr_type = type;

	return 0;
}

/*
 * Fill the inner-derived fields of an outer IPv4 header from the packet's
 * current inner network header: type_of_service, packet_id, fragment_offset,
 * time_to_live and next_proto_id (set to IPIP / IPV6 based on inner type;
 * the caller may overwrite it for tunnel protocols such as GRE).
 *
 * Returns the size in bytes of the inner packet starting at its network
 * header (so the caller can compute total_length as
 * sizeof(outer) + return-value), or -1 if the inner network type is
 * unsupported. Address fields, version_ihl, total_length and hdr_checksum
 * are left to the caller.
 */
static int
fill_outer_ip4_from_inner(struct rte_ipv4_hdr *outer, struct packet *inner) {
	struct rte_mbuf *mbuf = packet_to_mbuf(inner);

	if (inner->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *inner_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			inner->network_header.offset
		);
		outer->type_of_service = inner_hdr->type_of_service;
		outer->packet_id = inner_hdr->packet_id;
		outer->fragment_offset = inner_hdr->fragment_offset;
		outer->time_to_live = inner_hdr->time_to_live;
		outer->next_proto_id = IPPROTO_IPIP;
		return rte_be_to_cpu_16(inner_hdr->total_length);
	}

	if (inner->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *inner_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			inner->network_header.offset
		);
		outer->type_of_service =
			(rte_be_to_cpu_32(inner_hdr->vtc_flow) >> 20) & 0xFF;
		outer->packet_id = rte_cpu_to_be_16(0x01);
		outer->fragment_offset = 0;
		outer->time_to_live = inner_hdr->hop_limits;
		outer->next_proto_id = IPPROTO_IPV6;
		return sizeof(struct rte_ipv6_hdr) +
		       rte_be_to_cpu_16(inner_hdr->payload_len);
	}

	return -1;
}

/*
 * IPv6 counterpart of fill_outer_ipv4_from_inner. Fills vtc_flow,
 * hop_limits and proto (IPIP / IPV6) from the inner header.
 *
 * Returns the size in bytes of the inner packet starting at its network
 * header (so the caller can compute payload_len), or -1 if the inner
 * network type is unsupported. Address fields and payload_len are left to
 * the caller.
 */
static int
fill_outer_ip6_from_inner(struct rte_ipv6_hdr *outer, struct packet *inner) {
	struct rte_mbuf *mbuf = packet_to_mbuf(inner);

	if (inner->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *inner_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			inner->network_header.offset
		);
		outer->vtc_flow = rte_cpu_to_be_32(
			(0x6 << 28) | (inner_hdr->type_of_service << 20)
		); // TODO: flow label?
		outer->proto = IPPROTO_IPIP;
		outer->hop_limits = inner_hdr->time_to_live;
		return rte_be_to_cpu_16(inner_hdr->total_length);
	}

	if (inner->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *inner_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			inner->network_header.offset
		);
		outer->vtc_flow = inner_hdr->vtc_flow;
		outer->proto = IPPROTO_IPV6;
		outer->hop_limits = inner_hdr->hop_limits;
		return sizeof(struct rte_ipv6_hdr) +
		       rte_be_to_cpu_16(inner_hdr->payload_len);
	}

	return -1;
}

int
packet_ip4_encap(
	struct packet *packet,
	const uint8_t *dst,
	const uint8_t *src,
	uint8_t dscp_flag
) {
	struct rte_ipv4_hdr outer_hdr;
	rte_memcpy(&outer_hdr.src_addr, src, NET4_LEN);
	rte_memcpy(&outer_hdr.dst_addr, dst, NET4_LEN);
	outer_hdr.version_ihl = 0x45;

	int inner_size = fill_outer_ip4_from_inner(&outer_hdr, packet);
	if (inner_size < 0) {
		return -1;
	}
	if (dscp_flag != DSCP_MARK_ALWAYS) {
		outer_hdr.type_of_service &= DSCP_ECN_MASK;
	}
	outer_hdr.total_length =
		rte_cpu_to_be_16(sizeof(outer_hdr) + inner_size);

	outer_hdr.hdr_checksum = 0;
	outer_hdr.hdr_checksum = rte_ipv4_cksum(&outer_hdr);

	return packet_network_prepend(
		packet,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
		&outer_hdr,
		sizeof(outer_hdr)
	);
}

int
packet_ip6_encap(
	struct packet *packet,
	const uint8_t *dst,
	const uint8_t *src,
	uint8_t dscp_flag
) {
	struct rte_ipv6_hdr outer_hdr;
	rte_memcpy(&outer_hdr.src_addr, src, NET6_LEN);
	rte_memcpy(&outer_hdr.dst_addr, dst, NET6_LEN);

	int inner_size = fill_outer_ip6_from_inner(&outer_hdr, packet);
	if (inner_size < 0) {
		return -1;
	}
	if (dscp_flag != DSCP_MARK_ALWAYS) {
		outer_hdr.vtc_flow &= ~rte_cpu_to_be_32(
			(uint32_t)DSCP_MARK_MASK << RTE_IPV6_HDR_TC_SHIFT
		);
	}
	outer_hdr.payload_len = rte_cpu_to_be_16(inner_size);

	return packet_network_prepend(
		packet,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
		&outer_hdr,
		sizeof(outer_hdr)
	);
}

int
packet_mpls_encap(
	struct packet *packet,
	uint32_t label,
	uint8_t tc,
	uint8_t s,
	uint8_t ttl
) {
	label = htobe32((label << 12) | (tc << 9) | (s << 8) | ttl);

	if (packet_network_prepend(
		    packet, rte_cpu_to_be_16(RTE_ETHER_TYPE_MPLS), &label, 4
	    ))
		return -1;

	return 0;
}

int
packet_ip4_encap_udp(
	struct packet *packet,
	const uint8_t *src_ip,
	const uint8_t *dst_ip,
	const uint8_t *src_port,
	const uint8_t *dst_port
) {

	struct rte_ipv4_hdr ip_header;
	ip_header.version_ihl = 0x45;
	ip_header.type_of_service = 0;
	ip_header.total_length =
		htobe16(sizeof(struct rte_ipv4_hdr) +
			sizeof(struct rte_udp_hdr) + packet_data_len(packet) -
			packet->network_header.offset);
	ip_header.packet_id = 0;
	ip_header.fragment_offset = 0;
	// Default ttl
	ip_header.time_to_live = 64;
	ip_header.next_proto_id = IPPROTO_UDP;
	ip_header.hdr_checksum = 0;
	memcpy(&ip_header.src_addr, src_ip, 4);
	memcpy(&ip_header.dst_addr, dst_ip, 4);
	ip_header.hdr_checksum = csum(&ip_header, sizeof(ip_header));

	struct rte_udp_hdr udp_header;
	memcpy(&udp_header.src_port, src_port, 2);
	memcpy(&udp_header.dst_port, dst_port, 2);
	udp_header.dgram_len =
		htobe16(sizeof(struct rte_udp_hdr) + packet_data_len(packet) -
			packet->network_header.offset);
	udp_header.dgram_cksum = 0;

	uint16_t ip_proto_csum = 0;
	memcpy(&ip_proto_csum, &ip_header.next_proto_id, 1);
	uint32_t ip_hdr_cksum = csum(src_ip, 4) + csum(dst_ip, 4) +
				csum(&ip_header.total_length, 2) +
				ip_proto_csum;
	uint32_t udp_hdr_cksum = csum(&udp_header, sizeof(udp_header));
	// FIXME: should we track a csum for the entire packet payload?
	uint32_t payload_cksum =
		csum(packet_data(packet) + packet->network_header.offset,
		     packet_data_len(packet) - packet->network_header.offset);

	uint16_t cksum =
		~csum_reduce(ip_hdr_cksum + udp_hdr_cksum + payload_cksum);
	cksum -= !cksum;
	udp_header.dgram_cksum = cksum;

	if (packet_network_prepend(
		    packet, 0, &udp_header, sizeof(struct rte_udp_hdr)
	    ))
		return -1;

	if (packet_network_prepend(
		    packet,
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
		    &ip_header,
		    sizeof(struct rte_ipv4_hdr)
	    ))
		return -1;

	return 0;
}

int
packet_ip6_encap_udp(
	struct packet *packet,
	const uint8_t *src_ip,
	const uint8_t *dst_ip,
	const uint8_t *src_port,
	const uint8_t *dst_port
) {

	struct rte_ipv6_hdr ip_header;
	ip_header.vtc_flow = htobe32(0x6 << 28);
	ip_header.payload_len =
		htobe16(sizeof(struct rte_udp_hdr) + packet_data_len(packet) -
			packet->network_header.offset);
	ip_header.proto = IPPROTO_UDP;
	// Default hop limit
	ip_header.hop_limits = 64;
	memcpy(&ip_header.src_addr, src_ip, 16);
	memcpy(&ip_header.dst_addr, dst_ip, 16);

	struct rte_udp_hdr udp_header;
	memcpy(&udp_header.src_port, src_port, 2);
	memcpy(&udp_header.dst_port, dst_port, 2);
	udp_header.dgram_len =
		htobe16(sizeof(struct rte_udp_hdr) + packet_data_len(packet) -
			packet->network_header.offset);
	udp_header.dgram_cksum = 0;

	uint16_t ip_proto_csum = 0;
	memcpy(&ip_proto_csum, &ip_header.proto, 1);
	uint32_t ip_hdr_cksum = csum(src_ip, 16) + csum(dst_ip, 16) +
				csum(&ip_header.payload_len, 2) + ip_proto_csum;
	uint32_t udp_hdr_cksum = csum(&udp_header, sizeof(udp_header));
	// FIXME: should we track the entire packet payload?
	uint32_t payload_cksum =
		csum(packet_data(packet) + packet->network_header.offset,
		     packet_data_len(packet) - packet->network_header.offset);

	uint16_t cksum =
		~csum_reduce(ip_hdr_cksum + udp_hdr_cksum + payload_cksum);
	cksum -= !cksum;
	udp_header.dgram_cksum = cksum;

	if (packet_network_prepend(
		    packet, 0, &udp_header, sizeof(struct rte_udp_hdr)
	    ))
		return -1;

	if (packet_network_prepend(
		    packet,
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
		    &ip_header,
		    sizeof(struct rte_ipv6_hdr)
	    ))
		return -1;

	return 0;
}

int
packet_ip4_encap_gre(
	struct packet *packet, const uint8_t *dst, const uint8_t *src
) {
	struct {
		struct rte_ipv4_hdr ip;
		struct rte_gre_hdr gre;
	} __rte_packed outer;

	memset(&outer.gre, 0, sizeof(outer.gre));

	rte_memcpy(&outer.ip.src_addr, src, NET4_LEN);
	rte_memcpy(&outer.ip.dst_addr, dst, NET4_LEN);
	outer.ip.version_ihl = 0x45;

	int inner_size = fill_outer_ip4_from_inner(&outer.ip, packet);
	if (inner_size < 0) {
		return -1;
	}
	outer.ip.total_length = rte_cpu_to_be_16(sizeof(outer) + inner_size);
	outer.ip.next_proto_id = IPPROTO_GRE;

	outer.gre.proto = packet->network_header.type;

	outer.ip.hdr_checksum = 0;
	outer.ip.hdr_checksum = rte_ipv4_cksum(&outer.ip);

	return packet_network_prepend(
		packet,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
		&outer,
		sizeof(outer)
	);
}

int
packet_ip6_encap_gre(
	struct packet *packet, const uint8_t *dst, const uint8_t *src
) {
	struct {
		struct rte_ipv6_hdr ip;
		struct rte_gre_hdr gre;
	} __rte_packed outer;

	memset(&outer.gre, 0, sizeof(outer.gre));

	rte_memcpy(&outer.ip.src_addr, src, NET6_LEN);
	rte_memcpy(&outer.ip.dst_addr, dst, NET6_LEN);

	int inner_size = fill_outer_ip6_from_inner(&outer.ip, packet);
	if (inner_size < 0) {
		return -1;
	}
	outer.ip.payload_len = rte_cpu_to_be_16(sizeof(outer.gre) + inner_size);
	outer.ip.proto = IPPROTO_GRE;

	outer.gre.proto = packet->network_header.type;

	return packet_network_prepend(
		packet,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
		&outer,
		sizeof(outer)
	);
}

/*
 * Read the DSCP bits of the inner header of an encapsulated packet.
 *
 * proto is the next-header value of the outer header and offset the position
 * of the inner header from the start of the packet data.
 *
 * Returns the DSCP value aligned to a TOS / traffic class byte, that is
 * masked with DSCP_MARK_MASK, or -1 if the outer header does not carry an
 * inner IP packet.
 */
static int
inner_dscp(struct packet *packet, uint8_t proto, uint32_t offset) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (proto == IPPROTO_IPIP) {
		const struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv4_hdr *, offset
		);
		return inner->type_of_service & DSCP_MARK_MASK;
	}

	if (proto == IPPROTO_IPV6) {
		const struct rte_ipv6_hdr *inner = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv6_hdr *, offset
		);
		return (rte_be_to_cpu_32(inner->vtc_flow) >>
			RTE_IPV6_HDR_TC_SHIFT) &
		       DSCP_MARK_MASK;
	}

	return -1;
}

int
packet_ip4_copy_inner_dscp(struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint16_t offset = packet->network_header.offset;

	struct rte_ipv4_hdr *outer =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ipv4_hdr *, offset);

	int dscp = inner_dscp(
		packet, outer->next_proto_id, offset + rte_ipv4_hdr_len(outer)
	);
	if (dscp < 0) {
		return -1;
	}

	uint8_t old_dscp = outer->type_of_service & DSCP_MARK_MASK;
	if (dscp == old_dscp) {
		return 0;
	}

	uint16_t checksum = ~rte_be_to_cpu_16(outer->hdr_checksum);
	checksum = csum_minus(checksum, old_dscp);
	checksum = csum_plus(checksum, dscp);
	outer->hdr_checksum = ~rte_cpu_to_be_16(checksum);

	outer->type_of_service =
		dscp | (outer->type_of_service & DSCP_ECN_MASK);

	return 0;
}

int
packet_ip6_copy_inner_dscp(struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint16_t offset = packet->network_header.offset;

	struct rte_ipv6_hdr *outer =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ipv6_hdr *, offset);

	int dscp = inner_dscp(
		packet, outer->proto, offset + sizeof(struct rte_ipv6_hdr)
	);
	if (dscp < 0) {
		return -1;
	}

	uint32_t vtc_flow = rte_be_to_cpu_32(outer->vtc_flow);
	uint32_t ecn =
		vtc_flow & ((uint32_t)DSCP_ECN_MASK << RTE_IPV6_HDR_TC_SHIFT);

	vtc_flow &= ~(uint32_t)RTE_IPV6_HDR_TC_MASK;
	vtc_flow |= ((uint32_t)dscp << RTE_IPV6_HDR_TC_SHIFT) | ecn;
	outer->vtc_flow = rte_cpu_to_be_32(vtc_flow);

	return 0;
}
