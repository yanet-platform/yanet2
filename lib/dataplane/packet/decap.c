#include "decap.h"

#include <string.h>

#include <rte_ether.h>

int
packet_decap(struct packet *packet) {
	(void)packet;

	/*
		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			struct rte_ipv4_hdr *ipv4Header =
	   rte_pktmbuf_mtod_offset( mbuf, struct rte_ipv4_hdr *,
				packet->network_header.offset
			);
		    if (ipv4Header->proto = IPPROTO_IPIP) {

		    }
		} else {
			return -1;
		}
	*/

	return 0;
}
