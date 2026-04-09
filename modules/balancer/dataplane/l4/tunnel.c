#include <netinet/in.h>

#include <immintrin.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

#include "common/checksum.h"
#include "common/network.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/encap.h"

#include "gre.h"
#include "mss.h"
#include "packet.h"
#include "tunnel.h"

/*
 * Embed client source IP into the outer IPv6 tunnel source address
 * using SSE2 SIMD when the client address is a full 16-byte IPv6.
 *
 * The operation per byte is:
 *   outer_src[i] |= client_src[i] & ~mask[i]
 *
 * For 16-byte (IPv6) clients this maps to three 128-bit instructions:
 *   andnot, or, store.
 *
 * For 4-byte (IPv4) clients a scalar fallback is used.
 */
static inline void
embed_client_ip6(
	struct rte_mbuf *mbuf,
	uint16_t network_offset,
	const uint8_t *client_src,
	uint8_t client_len,
	const struct net6 *src_net
) {
	struct rte_ipv6_hdr *outer = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, network_offset
	);
	uint8_t *dst = outer->src_addr;

	if (client_len == NET6_LEN) {
		__m128i v_dst = _mm_loadu_si128((__m128i *)dst);
		__m128i v_cli = _mm_loadu_si128((const __m128i *)client_src);
		__m128i v_mask =
			_mm_loadu_si128((const __m128i *)src_net->mask);
		/* v_cli & ~v_mask */
		__m128i v_bits = _mm_andnot_si128(v_mask, v_cli);
		v_dst = _mm_or_si128(v_dst, v_bits);
		_mm_storeu_si128((__m128i *)dst, v_dst);
	} else {
		for (uint8_t i = 0; i < client_len; i++) {
			dst[i] |= client_src[i] & ~src_net->mask[i];
		}
	}
}

/*
 * Embed client source IP into the outer IPv4 tunnel source address
 * and recompute the IPv4 header checksum.
 *
 * Uses a single 32-bit word operation instead of a byte loop:
 *   src_addr = (client & ~mask) | net_addr
 */
static inline void
embed_client_ip4(
	struct rte_mbuf *mbuf,
	uint16_t network_offset,
	const uint8_t *client_src,
	const struct net4 *src_net
) {
	struct rte_ipv4_hdr *outer = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, network_offset
	);

	uint32_t client, mask, addr;
	__builtin_memcpy(&client, client_src, NET4_LEN);
	__builtin_memcpy(&mask, src_net->mask, NET4_LEN);
	__builtin_memcpy(&addr, src_net->addr, NET4_LEN);

	uint32_t old_src = outer->src_addr;
	outer->src_addr = (client & ~mask) | addr;

	uint16_t cksum = ~outer->hdr_checksum;
	cksum = csum_minus(cksum, (uint16_t)old_src);
	cksum = csum_minus(cksum, (uint16_t)(old_src >> 16));
	cksum = csum_plus(cksum, (uint16_t)outer->src_addr);
	cksum = csum_plus(cksum, (uint16_t)(outer->src_addr >> 16));
	outer->hdr_checksum = (cksum == 0xffff) ? cksum : ~cksum;
}

/*
 * Encapsulate an inner-IPv4 packet in an IP tunnel and embed the
 * client source address into the outer header.
 */
static void
encapsulate_ipv4(struct packet *packet, struct balancer_real *real) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	bool is_outer_ipv6 = real->flags & balancer_real_ipv6;

	uint16_t inner_offset = packet->network_header.offset;

	if (is_outer_ipv6) {
		packet_ip6_encap(
			packet, real->addr.v6.bytes, real->src.v6.addr
		);

		uint16_t outer_size = sizeof(struct rte_ipv6_hdr);
		struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv4_hdr *, inner_offset + outer_size
		);
		const uint8_t *client_src = (const uint8_t *)&inner->src_addr;

		embed_client_ip6(
			mbuf,
			packet->network_header.offset,
			client_src,
			NET4_LEN,
			&real->src.v6
		);
	} else {
		packet_ip4_encap(
			packet, real->addr.v4.bytes, real->src.v4.addr
		);

		uint16_t outer_size = sizeof(struct rte_ipv4_hdr);
		struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv4_hdr *, inner_offset + outer_size
		);
		const uint8_t *client_src = (const uint8_t *)&inner->src_addr;

		embed_client_ip4(
			mbuf,
			packet->network_header.offset,
			client_src,
			&real->src.v4
		);
	}
}

/*
 * Encapsulate an inner-IPv6 packet in an IP tunnel and embed the
 * client source address into the outer header.
 */
static void
encapsulate_ipv6(struct packet *packet, struct balancer_real *real) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	bool is_outer_ipv6 = real->flags & balancer_real_ipv6;

	uint16_t inner_offset = packet->network_header.offset;

	if (is_outer_ipv6) {
		packet_ip6_encap(
			packet, real->addr.v6.bytes, real->src.v6.addr
		);

		uint16_t outer_size = sizeof(struct rte_ipv6_hdr);
		struct rte_ipv6_hdr *inner = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv6_hdr *, inner_offset + outer_size
		);
		const uint8_t *client_src = (const uint8_t *)inner->src_addr;

		embed_client_ip6(
			mbuf,
			packet->network_header.offset,
			client_src,
			NET6_LEN,
			&real->src.v6
		);
	} else {
		packet_ip4_encap(
			packet, real->addr.v4.bytes, real->src.v4.addr
		);

		uint16_t outer_size = sizeof(struct rte_ipv4_hdr);
		struct rte_ipv6_hdr *inner = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv6_hdr *, inner_offset + outer_size
		);
		const uint8_t *client_src = (const uint8_t *)inner->src_addr;

		embed_client_ip4(
			mbuf,
			packet->network_header.offset,
			client_src,
			&real->src.v4
		);
	}
}

/*
 * Update per-VS and per-real forwarding counters.
 */
static inline void
update_tunnel_stats(struct l4_packet_context *pkt_ctx) {
	struct balancer_vs_stats *vs_stats = pkt_ctx->matched_vs_stats;
	struct balancer_real_stats *real_stats = pkt_ctx->resolved_real_stats;
	uint64_t pkt_bytes = pkt_ctx->packet->mbuf->pkt_len;

	vs_stats->outgoing_packets += 1;
	vs_stats->outgoing_bytes += pkt_bytes;
	real_stats->packets += 1;
	real_stats->bytes += pkt_bytes;
}

/*
 * Tunnel a packet whose inner layer is IPv4.
 */
void
tunnel_ipv4_packet(struct l4_packet_context *pkt_ctx) {
	struct packet *packet = pkt_ctx->packet;
	struct balancer_real *real = pkt_ctx->resolved_real;
	uint8_t vs_flags = pkt_ctx->matched_vs->flags;

	/* No MSS clamping for inner IPv4. */

	encapsulate_ipv4(packet, real);

	if (unlikely(vs_flags & balancer_vs_gre)) {
		bool is_outer_ipv6 = real->flags & balancer_real_ipv6;
		insert_gre_header(packet, is_outer_ipv6, true);
	}

	update_tunnel_stats(pkt_ctx);
}

/*
 * Tunnel a packet whose inner layer is IPv6.
 *
 * MSS clamping is applied when the VS has balancer_vs_fix_mss set.
 */
void
tunnel_ipv6_packet(struct l4_packet_context *pkt_ctx) {
	struct packet *packet = pkt_ctx->packet;
	struct balancer_real *real = pkt_ctx->resolved_real;
	uint8_t vs_flags = pkt_ctx->matched_vs->flags;

	/* Clamp MSS for IPv6 SYN packets before encapsulation. */
	if (unlikely(vs_flags & balancer_vs_fix_mss)) {
		fix_mss_ipv6(packet);
	}

	encapsulate_ipv6(packet, real);

	if (unlikely(vs_flags & balancer_vs_gre)) {
		bool is_outer_ipv6 = real->flags & balancer_real_ipv6;
		insert_gre_header(packet, is_outer_ipv6, false);
	}

	update_tunnel_stats(pkt_ctx);
}
