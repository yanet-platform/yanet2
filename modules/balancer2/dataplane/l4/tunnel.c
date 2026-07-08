#include <netinet/in.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

#include "common/network.h"

#include "dataplane/packet/mss.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/encap.h"

#include "packet.h"
#include "real.h"
#include "tunnel.h"
#include "types/stats.h"
#include "vs.h"

#define CLAMP_MSS 1220
#define INSERT_MSS 576

/*
 * Builds the outer IPv4 tunnel source address by embedding client source
 * bits into the unmasked positions of the configured source network.
 */
static inline void
build_outer_src4(
	uint8_t *out, const uint8_t *client_src, const struct net4 *src_net
) {
	memcpy(out, src_net->addr, NET4_LEN);
	for (size_t i = 0; i < NET4_LEN; ++i) {
		out[i] |= client_src[i] & ~src_net->mask[i];
	}
}

static inline void
build_outer_src6(
	uint8_t *out,
	const uint8_t *client_src,
	uint8_t client_len,
	const struct net6 *src_net
) {
	memcpy(out, src_net->addr, NET6_LEN);
	uint8_t offset = NET6_LEN - client_len;
	for (uint8_t i = 0; i < client_len; ++i) {
		out[offset + i] |= client_src[i] & ~src_net->mask[offset + i];
	}
}

/*
 * Shared encapsulation logic for both inner-IPv4 and inner-IPv6 packets.
 * client_src points to the client source address in the inner header;
 * client_src_len is its byte length (4 for IPv4, 16 for IPv6).
 */
static inline int
encapsulate(
	struct packet *packet,
	struct real *real,
	bool gre,
	const uint8_t *client_src,
	uint8_t client_src_len
) {
	bool is_outer_ipv6 = real_flags(real) & real_ip6;

	if (is_outer_ipv6) {
		uint8_t outer_src[NET6_LEN];
		build_outer_src6(
			outer_src, client_src, client_src_len, &real->src.v6
		);
		if (!gre) {
			return packet_ip6_encap(
				packet, real->addr.v6.bytes, outer_src
			);
		} else {
			return packet_ip6_encap_gre(
				packet, real->addr.v6.bytes, outer_src
			);
		}
	} else {
		uint8_t outer_src[NET4_LEN];
		build_outer_src4(outer_src, client_src, &real->src.v4);
		if (!gre) {
			return packet_ip4_encap(
				packet, real->addr.v4.bytes, outer_src
			);
		} else {
			return packet_ip4_encap_gre(
				packet, real->addr.v4.bytes, outer_src
			);
		}
	}
}

static int
encapsulate_ipv4(struct packet *packet, struct real *real, bool gre) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);
	return encapsulate(
		packet, real, gre, (const uint8_t *)&inner->src_addr, NET4_LEN
	);
}

static int
encapsulate_ipv6(struct packet *packet, struct real *real, bool gre) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv6_hdr *inner = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	return encapsulate(
		packet, real, gre, (const uint8_t *)inner->src_addr, NET6_LEN
	);
}

/*
 * Tunnel a packet whose inner layer is IPv4.
 */
int
tunnel_ip4_packet(struct packet_context *pkt_ctx) {
	struct packet *packet = pkt_ctx->packet;
	struct real *real = pkt_ctx->selected_real;
	struct virtual_service *vs = pkt_ctx->matched_vs;

	/* No MSS clamping for inner IPv4. */

	return encapsulate_ipv4(packet, real, vs->flags & vs_gre);
}

/*
 * Tunnel a packet whose inner layer is IPv6.
 *
 * MSS clamping is applied when the VS has fix mss flag set.
 */
int
tunnel_ip6_packet(struct packet_context *pkt_ctx) {
	struct packet *packet = pkt_ctx->packet;
	struct real *real = pkt_ctx->selected_real;
	struct virtual_service *vs = pkt_ctx->matched_vs;
	struct balancer_vs_stats *vs_stats = pkt_ctx->matched_vs_stats;

	if (vs->flags & vs_fix_mss) {
		switch (packet_set_mss(packet, CLAMP_MSS, INSERT_MSS)) {
		case packet_set_mss_ok:
			break;
		case packet_set_mss_malformed:
			vs_stats->mss_malformed_packet += 1;
			break;
		case packet_set_mss_no_headroom:
			vs_stats->mss_no_headroom += 1;
			break;
		}
	}

	return encapsulate_ipv6(packet, real, vs->flags & vs_gre);
}
