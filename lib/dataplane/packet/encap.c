#include "encap.h"

#include <rte_ether.h>
#include <rte_ip.h>

static void
packet_network_prepend(
	struct packet *packet,
	uint16_t type,
	const void *header,
	const size_t size
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	rte_pktmbuf_prepend(mbuf, size);
	memmove(rte_pktmbuf_mtod(mbuf, char *),
		rte_pktmbuf_mtod_offset(mbuf, char *, size),
		packet->network_header.offset);
	memcpy(rte_pktmbuf_mtod_offset(
		       mbuf, char *, packet->network_header.offset
	       ),
	       header,
	       size);

	packet->transport_header.offset += size;

	// FIXME previos heade type (ex: vlan)
	uint16_t *nextHeaderType = rte_pktmbuf_mtod_offset(
		mbuf, uint16_t *, packet->network_header.offset - 2
	);
	*nextHeaderType = type;
}

int
packet_ip4_encap(
	struct packet *packet, const uint8_t *dst, const uint8_t *src
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4HeaderInner = NULL;
	struct rte_ipv6_hdr *ipv6HeaderInner = NULL;

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		ipv4HeaderInner = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		ipv6HeaderInner = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
	} else {
		return -1;
	}

	struct rte_ipv4_hdr header;
	rte_memcpy(&header.src_addr, src, 4);
	rte_memcpy(&header.dst_addr, dst, 4);
	header.version_ihl = 0x45;

	if (ipv4HeaderInner) {
		header.type_of_service = ipv4HeaderInner->type_of_service;
		header.total_length = rte_cpu_to_be_16(
			sizeof(struct rte_ipv4_hdr) +
			rte_be_to_cpu_16(ipv4HeaderInner->total_length)
		);

		header.packet_id = ipv4HeaderInner->packet_id;
		header.fragment_offset = ipv4HeaderInner->fragment_offset;
		header.time_to_live = ipv4HeaderInner->time_to_live;
		header.next_proto_id = IPPROTO_IPIP;
	} else {
		header.type_of_service =
			(rte_be_to_cpu_32(ipv6HeaderInner->vtc_flow) >> 20) &
			0xFF;
		header.total_length = rte_cpu_to_be_16(
			sizeof(struct rte_ipv4_hdr) +
			sizeof(struct rte_ipv6_hdr) +
			rte_be_to_cpu_16(ipv6HeaderInner->payload_len)
		);

		header.packet_id = rte_cpu_to_be_16(0x01);
		header.fragment_offset = 0;
		header.time_to_live = ipv6HeaderInner->hop_limits;
		header.next_proto_id = IPPROTO_IPV6;
	}

	header.hdr_checksum = 0;
	rte_ipv4_cksum(&header);

	packet_network_prepend(
		packet,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
		&header,
		sizeof(header)
	);

	return 0;
}

int
packet_ip6_encap(
	struct packet *packet, const uint8_t *dst, const uint8_t *src
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4HeaderInner = NULL;
	struct rte_ipv6_hdr *ipv6HeaderInner = NULL;

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		ipv4HeaderInner = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		ipv6HeaderInner = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
	} else {
		return -1;
	}

	struct rte_ipv6_hdr header;
	rte_memcpy(&header.src_addr, src, 16);
	rte_memcpy(&header.dst_addr, dst, 16);

	if (ipv4HeaderInner != NULL) {
		header.vtc_flow = rte_cpu_to_be_32(
			(0x6 << 28) | (ipv4HeaderInner->type_of_service << 20)
		); ///< @todo: flow label
		header.payload_len = ipv4HeaderInner->total_length;
		header.proto = IPPROTO_IPIP;
		header.hop_limits = ipv4HeaderInner->time_to_live;
	} else if (ipv6HeaderInner != NULL) {
		header.vtc_flow = ipv6HeaderInner->vtc_flow;
		header.payload_len = rte_cpu_to_be_16(
			sizeof(struct rte_ipv6_hdr) +
			rte_be_to_cpu_16(ipv6HeaderInner->payload_len)
		);
		header.proto = IPPROTO_IPV6;
		header.hop_limits = ipv6HeaderInner->hop_limits;
	}

	packet_network_prepend(
		packet,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
		&header,
		sizeof(header)
	);

	return 0;
}
