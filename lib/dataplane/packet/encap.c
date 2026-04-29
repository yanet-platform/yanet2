#include "encap.h"

#include <rte_ether.h>
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

	// FIXME previous header type (ex: vlan)
	uint16_t *next_hdr_type = rte_pktmbuf_mtod_offset(
		mbuf, uint16_t *, packet->network_header.offset - 2
	);
	*next_hdr_type = type;

	return 0;
}

int
packet_ip4_encap(
	struct packet *packet, const uint8_t *dst, const uint8_t *src
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr outer_hdr;
	rte_memcpy(&outer_hdr.src_addr, src, NET4_LEN);
	rte_memcpy(&outer_hdr.dst_addr, dst, NET4_LEN);
	outer_hdr.version_ihl = 0x45;

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *inner_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		outer_hdr.type_of_service = inner_hdr->type_of_service;
		outer_hdr.total_length = rte_cpu_to_be_16(
			sizeof(struct rte_ipv4_hdr) +
			rte_be_to_cpu_16(inner_hdr->total_length)
		);

		outer_hdr.packet_id = inner_hdr->packet_id;
		outer_hdr.fragment_offset = inner_hdr->fragment_offset;
		outer_hdr.time_to_live = inner_hdr->time_to_live;
		outer_hdr.next_proto_id = IPPROTO_IPIP;
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *inner_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		outer_hdr.type_of_service =
			(rte_be_to_cpu_32(inner_hdr->vtc_flow) >> 20) & 0xFF;
		outer_hdr.total_length = rte_cpu_to_be_16(
			sizeof(struct rte_ipv4_hdr) +
			sizeof(struct rte_ipv6_hdr) +
			rte_be_to_cpu_16(inner_hdr->payload_len)
		);

		outer_hdr.packet_id = rte_cpu_to_be_16(0x01);
		outer_hdr.fragment_offset = 0;
		outer_hdr.time_to_live = inner_hdr->hop_limits;
		outer_hdr.next_proto_id = IPPROTO_IPV6;
	} else {
		return -1;
	}

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
	struct packet *packet, const uint8_t *dst, const uint8_t *src
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr outer_hdr;
	rte_memcpy(&outer_hdr.src_addr, src, NET6_LEN);
	rte_memcpy(&outer_hdr.dst_addr, dst, NET6_LEN);

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *inner_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		outer_hdr.vtc_flow = rte_cpu_to_be_32(
			(0x6 << 28) | (inner_hdr->type_of_service << 20)
		); // TODO: flow label?
		outer_hdr.payload_len = inner_hdr->total_length;
		outer_hdr.proto = IPPROTO_IPIP;
		outer_hdr.hop_limits = inner_hdr->time_to_live;
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *inner_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		outer_hdr.vtc_flow = inner_hdr->vtc_flow;
		outer_hdr.payload_len = rte_cpu_to_be_16(
			sizeof(struct rte_ipv6_hdr) +
			rte_be_to_cpu_16(inner_hdr->payload_len)
		);
		outer_hdr.proto = IPPROTO_IPV6;
		outer_hdr.hop_limits = inner_hdr->hop_limits;
	} else {
		return -1;
	}

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
