#include "gre.h"

#include <string.h>

#include <rte_ether.h>
#include <rte_gre.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

#include "common/checksum.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "rte_branch_prediction.h"

static void
adjust_outer_ipv6_for_gre(struct rte_mbuf *mbuf, uint16_t network_offset) {
	struct rte_ipv6_hdr *hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, network_offset
	);
	hdr->proto = IPPROTO_GRE;
	hdr->payload_len = rte_cpu_to_be_16(
		rte_be_to_cpu_16(hdr->payload_len) + sizeof(struct rte_gre_hdr)
	);
}

static void
adjust_outer_ipv4_for_gre(struct rte_mbuf *mbuf, uint16_t network_offset) {
	struct rte_ipv4_hdr *hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, network_offset
	);

	/* Save the 16-bit words that will change. */
	uint16_t old_total_length = hdr->total_length;
	uint16_t old_ttl_proto;
	memcpy(&old_ttl_proto, &hdr->time_to_live, sizeof(uint16_t));

	hdr->next_proto_id = IPPROTO_GRE;
	hdr->total_length = rte_cpu_to_be_16(
		rte_be_to_cpu_16(hdr->total_length) + sizeof(struct rte_gre_hdr)
	);

	/* Incremental checksum: only total_length and ttl|proto changed. */
	uint16_t new_ttl_proto;
	memcpy(&new_ttl_proto, &hdr->time_to_live, sizeof(uint16_t));

	uint16_t cksum = ~hdr->hdr_checksum;
	cksum = csum_minus(cksum, old_total_length);
	cksum = csum_minus(cksum, old_ttl_proto);
	cksum = csum_plus(cksum, hdr->total_length);
	cksum = csum_plus(cksum, new_ttl_proto);
	hdr->hdr_checksum = (cksum == 0xffff) ? cksum : ~cksum;
}

void
insert_gre_header(
	struct packet *packet, bool is_outer_ipv6, bool is_inner_ipv4
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	const uint16_t gre_size = sizeof(struct rte_gre_hdr);

	if (unlikely(rte_pktmbuf_prepend(mbuf, gre_size) == NULL)) {
		return;
	}

	uint16_t outer_ip_size = is_outer_ipv6 ? sizeof(struct rte_ipv6_hdr)
					       : sizeof(struct rte_ipv4_hdr);
	uint16_t prefix_len = packet->network_header.offset + outer_ip_size;

	memmove(rte_pktmbuf_mtod(mbuf, char *),
		rte_pktmbuf_mtod_offset(mbuf, char *, gre_size),
		prefix_len);

	if (is_outer_ipv6) {
		adjust_outer_ipv6_for_gre(mbuf, packet->network_header.offset);
	} else {
		adjust_outer_ipv4_for_gre(mbuf, packet->network_header.offset);
	}

	struct rte_gre_hdr *gre =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_gre_hdr *, prefix_len);
	memset(gre, 0, sizeof(struct rte_gre_hdr));
	gre->proto = rte_cpu_to_be_16(
		is_inner_ipv4 ? RTE_ETHER_TYPE_IPV4 : RTE_ETHER_TYPE_IPV6
	);

	packet->transport_header.offset += gre_size;
}
