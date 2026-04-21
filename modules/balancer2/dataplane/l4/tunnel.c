#include <netinet/in.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

#include "common/network.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/encap.h"

#include "packet.h"
#include "tunnel.h"

/*
 * Build an outer IPv4 tunnel source by embedding client source bits into
 * the unmasked positions of the configured src network address:
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
	for (uint8_t i = 0; i < client_len; ++i) {
		out[i] |= client_src[i] & ~src_net->mask[i];
	}
}

/*
 * Encapsulate an inner-IPv4 packet in an IP tunnel and embed the
 * client source address into the outer header.
 */
static int
encapsulate_ipv4(struct packet *packet, struct balancer_real *real) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	bool is_outer_ipv6 = real->flags & balancer_real_ipv6;

	struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);
	const uint8_t *client_src = (const uint8_t *)&inner->src_addr;

	if (is_outer_ipv6) {
		uint8_t outer_src[NET6_LEN];
		build_outer_src6(
			outer_src, client_src, NET4_LEN, &real->src.v6
		);
		return packet_ip6_encap(packet, real->addr.v6.bytes, outer_src);
	} else {
		uint8_t outer_src[NET4_LEN];
		build_outer_src4(outer_src, client_src, &real->src.v4);
		return packet_ip4_encap(packet, real->addr.v4.bytes, outer_src);
	}
}

static int
encapsulate_ipv6(struct packet *packet, struct balancer_real *real) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	bool is_outer_ipv6 = real->flags & balancer_real_ipv6;

	struct rte_ipv6_hdr *inner = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	const uint8_t *client_src = (const uint8_t *)inner->src_addr;

	if (is_outer_ipv6) {
		uint8_t outer_src[NET6_LEN];
		build_outer_src6(
			outer_src, client_src, NET6_LEN, &real->src.v6
		);
		return packet_ip6_encap(packet, real->addr.v6.bytes, outer_src);
	} else {
		uint8_t outer_src[NET4_LEN];
		build_outer_src4(outer_src, client_src, &real->src.v4);
		return packet_ip4_encap(packet, real->addr.v4.bytes, outer_src);
	}
}

/*
 * Tunnel a packet whose inner layer is IPv4.
 */
int
tunnel_ip4_packet(struct packet_context *pkt_ctx) {
	struct packet *packet = pkt_ctx->packet;
	struct balancer_real *real = pkt_ctx->selected_real;

	/* No MSS clamping for inner IPv4. */

	/* TODO: check GRE encapsulation */

	return encapsulate_ipv4(packet, real);
}

/*
 * Tunnel a packet whose inner layer is IPv6.
 *
 * MSS clamping is applied when the VS has balancer_vs_fix_mss set.
 */
int
tunnel_ip6_packet(struct packet_context *pkt_ctx) {
	struct packet *packet = pkt_ctx->packet;
	struct balancer_real *real = pkt_ctx->selected_real;

	/* TODO: fix mss */
	/* On error, update corresponding VS counter and continue (no drop). */

	/* TODO: check GRE encapsulation */

	return encapsulate_ipv6(packet, real);
}
