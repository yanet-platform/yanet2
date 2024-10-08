#include "encap.h"

void
packet_network_prepend(
	struct packet *packet,
	const void *header,
	const size_t size,
	uint16_t type
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	rte_pktmbuf_prepend(mbuf, size);
	rte_memcpy(
		rte_pktmbuf_mtod(mbuf, char*),
		rte_pktmbuf_mtod_offset(mbuf, char*, size),
		packet->network_headers[0].offset);

	rte_memmove(
		packet->network_headers + 1,
		packet->network_headers,
		sizeof(struct network_header) * (NETWORK_HEADER_COUNT - 1));
	
}

int
packet_ip_encap(
	struct packet *packet,
	struct ip_addr *dst,
	struct ip_addr *src
)
{
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr* ipv4HeaderInner = nullptr;
	struct rte_ipv6_hdr* ipv6HeaderInner = nullptr;

	if (packet->network_headers[0].type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		ipv4HeaderInner = rte_pktmbuf_mtod_offset(mbuf, rte_ipv4_hdr*, metadata->network_headerOffset);
	} else {
		ipv6HeaderInner = rte_pktmbuf_mtod_offset(mbuf, rte_ipv6_hdr*, metadata->network_headerOffset);
	}

	if (ip_addr_is_6(dst)) {
		assert(ip_addr_is_6(src));

		struct rte_ipv6_hdr header;
		rte_memcpy(&header.src_addr, src->data, 16);
		rte_memcpy(&header.dst_addr, dst->data, 16);

		if (ipv4HeaderInner != NULL) {
			header.vtc_flow = rte_cpu_to_be_32((0x6 << 28) | (ipv4HeaderInner->type_of_service << 20)); ///< @todo: flow label
			header.payload_len = ipv4HeaderInner->total_length;
			header.proto = IPPROTO_IPIP;
			header.hop_limits = ipv4HeaderInner->time_to_live;
		} else {
			header.vtc_flow = ipv6HeaderInner->vtc_flow;
			header.payload_len = rte_cpu_to_be_16(sizeof(rte_ipv6_hdr) + rte_be_to_cpu_16(ipv6HeaderInner->payload_len));
			header.proto = IPPROTO_IPV6;
			header.hop_limits = ipv6HeaderInner->hop_limits;
		}

		packet_network_prepend(packet, &header, sizeof(header));


		rte_pktmbuf_prepend(mbuf, sizeof(rte_ipv6_hdr));
		rte_memcpy(rte_pktmbuf_mtod(mbuf, char*),
		           rte_pktmbuf_mtod_offset(mbuf, char*, sizeof(rte_ipv6_hdr)),
		           packet->network_headers[0].offset);


	} else {
		assert(ip_addr_is_4(src);

		rte_pktmbuf_prepend(mbuf, sizeof(rte_ipv4_hdr));
		rte_memcpy(rte_pktmbuf_mtod(mbuf, char*),
		           rte_pktmbuf_mtod_offset(mbuf, char*, sizeof(rte_ipv4_hdr)),
		           packet->network_headers[0].offset);

	}

	return 0;
}

int
packet_gre_decap(
	struct packet *packet,
	struct ip_addr *dst,
	struct ip_addr *src
)
{
	return 0;
}


