#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

#include "lib/statemap/fwmap.h"

#include "lookup.h"

void
l3s_key_of_packet(struct packet *packet, struct l3s_key *key) {
	memset(key, 0, sizeof(*key));

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	uint16_t src_port = 0;
	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		src_port = rte_be_to_cpu_16(tcp_hdr->src_port);
	} else {
		struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);
		src_port = rte_be_to_cpu_16(udp_hdr->src_port);
	}
	key->src_port = src_port;

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		key->family = 4;
		memcpy(key->src_addr, &ipv4_hdr->src_addr, 4);
	} else {
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		key->family = 6;
		memcpy(key->src_addr, ipv6_hdr->src_addr, 16);
	}
}
