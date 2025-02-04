#include <netinet/in.h>
#include <rte_ether.h>
#include <rte_gre.h>

#include "decap.h"
#include "packet.h"

// (first u32 + count of optional u32) * 4
#define decap_gre_header_size(byte0)                                           \
	(1 + __builtin_popcount((byte0) & 0xb0)) << 2

/// GRE Header:
///                      1                   2                   3
///  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
/// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
/// |C| |K|S| Reserved0       | Ver |         Protocol Type         |
/// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
/// |      Checksum (optional)      |       Reserved1 (Optional)    |
/// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
/// |                         Key (optional)                        |
/// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
/// |                 Sequence Number (optional)                    |
/// +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
/// https://datatracker.ietf.org/doc/html/rfc2890#section-2
static int
packet_skip_gre(struct packet *packet, uint16_t *type, uint16_t *offset) {
	struct rte_gre_hdr *gre_hdr = rte_pktmbuf_mtod_offset(
		packet->mbuf,
		struct rte_gre_hdr *,
		packet->transport_header.offset
	);

	if (((*(uint32_t *)gre_hdr) & 0x0000FF4F) != 0x00000000) {
		// If any reserved bits or a version is set
		return -1;
	}

	if (gre_hdr->proto == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		*type = IPPROTO_IPIP;
	} else if (gre_hdr->proto == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		*type = IPPROTO_IPV6;
	} else { // error case
		return -1;
	}
	*offset += decap_gre_header_size(*(uint8_t *)gre_hdr);
	return 0;
}

int
packet_decap(struct packet *packet) {
	uint16_t next_transport = packet->transport_header.type;
	uint16_t next_offset = packet->transport_header.offset;
	uint16_t next_ether_type = packet->network_header.type;

	if (next_transport == IPPROTO_GRE) {
		if (packet_skip_gre(packet, &next_transport, &next_offset)) {
			return -1;
		}
	}
	uint16_t tun_hdrs_size = next_offset - packet->network_header.offset;

	if (next_transport == IPPROTO_IPIP) {
		next_ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
		if (parse_ipv4_header(packet, &next_transport, &next_offset)) {
			return -1;
		}
	} else if (next_transport == IPPROTO_IPV6) {
		next_ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
		if (parse_ipv6_header(packet, &next_transport, &next_offset)) {
			return -1;
		}
	} else {
		// unknown tunnel
		return -1;
	}

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	// Remove tunnel headers
	uint8_t *prev_start = rte_pktmbuf_mtod(mbuf, uint8_t *);
	uint8_t *new_start = (uint8_t *)rte_pktmbuf_adj(mbuf, tun_hdrs_size);
	if (unlikely(new_start == NULL)) {
		return -1;
	}
	/* Copy ether header (and vlans) over rather than moving whole packet */
	memmove(new_start, prev_start, packet->network_header.offset);

	// Update ether type
	uint16_t *prev_eth_type = rte_pktmbuf_mtod_offset(
		mbuf,
		uint16_t *,
		packet->network_header.offset - sizeof(uint16_t)
	);
	*prev_eth_type = next_ether_type;
	// And transport_header meta
	packet->transport_header.type = next_transport;
	packet->transport_header.offset = next_offset - tun_hdrs_size;

	return 0;
}
