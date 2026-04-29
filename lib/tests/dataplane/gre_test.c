#include <netinet/in.h>
#include <stdint.h>
#include <string.h>

#include <rte_byteorder.h>
#include <rte_ether.h>
#include <rte_gre.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

#include "common/network.h"
#include "common/test_assert.h"

#include "lib/dataplane/packet/encap.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/logging/log.h"
#include "lib/utils/packet.h"

#include "snapshot.h"

static const uint8_t inner_payload[] = "GRE TEST PAYLOAD 123 111 987 TEST";

#define INNER_PAYLOAD_LEN (sizeof(inner_payload) - 1)

#define DEFAULT_HEADROOM 128
#define DEFAULT_TAILROOM 256

#define INNER_TOS 0x88
#define INNER_TTL 32
#define INNER_PACKET_ID 0xabcd
/* Bits 0..2 of the IPv4 fragment_offset field (DF set, MF clear, frag=0). */
#define INNER_FRAG_FLAGS 0x4000

static const uint8_t outer_src4[NET4_LEN] = {10, 0, 0, 1};
static const uint8_t outer_dst4[NET4_LEN] = {10, 0, 0, 2};
static const uint8_t inner_src4[NET4_LEN] = {192, 168, 1, 1};
static const uint8_t inner_dst4[NET4_LEN] = {192, 168, 1, 2};

static const uint8_t outer_src6[NET6_LEN] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
};
static const uint8_t outer_dst6[NET6_LEN] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2
};
static const uint8_t inner_src6[NET6_LEN] = {
	0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
};
static const uint8_t inner_dst6[NET6_LEN] = {
	0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2
};

/* Packet construction helpers
 *
 * The inner protocol is set to UDP, but the bytes following the IP header are
 * the inner_payload string rather than a real UDP datagram — these tests
 * exercise the GRE encap, not transport parsing. parse_packet will read the
 * first 8 bytes as a UDP header for hashing purposes; that's harmless. */

static int
build_eth_ip4(struct packet *p, uint16_t headroom) {
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv4_hdr) + INNER_PAYLOAD_LEN;
	memset(p, 0, sizeof(*p));
	p->mbuf = alloc_mbuf(headroom, pkt_len, DEFAULT_TAILROOM);
	if (!p->mbuf) {
		return -1;
	}

	uint8_t *data = rte_pktmbuf_mtod(p->mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ip4 = (struct rte_ipv4_hdr *)(eth + 1);
	ip4->version_ihl = 0x45;
	ip4->type_of_service = INNER_TOS;
	ip4->total_length = rte_cpu_to_be_16(sizeof(*ip4) + INNER_PAYLOAD_LEN);
	ip4->packet_id = rte_cpu_to_be_16(INNER_PACKET_ID);
	ip4->fragment_offset = rte_cpu_to_be_16(INNER_FRAG_FLAGS);
	ip4->time_to_live = INNER_TTL;
	ip4->next_proto_id = IPPROTO_UDP;
	memcpy(&ip4->src_addr, inner_src4, NET4_LEN);
	memcpy(&ip4->dst_addr, inner_dst4, NET4_LEN);
	ip4->hdr_checksum = 0;
	ip4->hdr_checksum = rte_ipv4_cksum(ip4);

	memcpy(ip4 + 1, inner_payload, INNER_PAYLOAD_LEN);

	return parse_packet(p);
}

static int
build_eth_ip6(struct packet *p, uint16_t headroom) {
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv6_hdr) + INNER_PAYLOAD_LEN;
	memset(p, 0, sizeof(*p));
	p->mbuf = alloc_mbuf(headroom, pkt_len, DEFAULT_TAILROOM);
	if (!p->mbuf) {
		return -1;
	}

	uint8_t *data = rte_pktmbuf_mtod(p->mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	struct rte_ipv6_hdr *ip6 = (struct rte_ipv6_hdr *)(eth + 1);
	ip6->vtc_flow =
		rte_cpu_to_be_32((0x6u << 28) | ((uint32_t)INNER_TOS << 20));
	ip6->payload_len = rte_cpu_to_be_16(INNER_PAYLOAD_LEN);
	ip6->proto = IPPROTO_UDP;
	ip6->hop_limits = INNER_TTL;
	memcpy(ip6->src_addr, inner_src6, NET6_LEN);
	memcpy(ip6->dst_addr, inner_dst6, NET6_LEN);

	memcpy(ip6 + 1, inner_payload, INNER_PAYLOAD_LEN);

	return parse_packet(p);
}

/* Packet whose ether type is QinQ (0x88a8) — neither IPv4 nor IPv6 — so
 * parse_packet leaves network_header.type set to that ether type and the
 * encap functions must reject it. */
static int
build_eth_unknown(struct packet *p) {
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) + 8;
	memset(p, 0, sizeof(*p));
	p->mbuf = alloc_mbuf(DEFAULT_HEADROOM, pkt_len, DEFAULT_TAILROOM);
	if (!p->mbuf) {
		return -1;
	}
	uint8_t *data = rte_pktmbuf_mtod(p->mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_QINQ);
	return parse_packet(p);
}

/* Accessors */

static struct rte_ether_hdr *
pkt_eth(struct packet *p) {
	return rte_pktmbuf_mtod(p->mbuf, struct rte_ether_hdr *);
}

static struct rte_ipv4_hdr *
pkt_outer_ip4(struct packet *p) {
	return rte_pktmbuf_mtod_offset(
		p->mbuf, struct rte_ipv4_hdr *, p->network_header.offset
	);
}

static struct rte_ipv6_hdr *
pkt_outer_ip6(struct packet *p) {
	return rte_pktmbuf_mtod_offset(
		p->mbuf, struct rte_ipv6_hdr *, p->network_header.offset
	);
}

static struct rte_gre_hdr *
pkt_gre_after_ip4(struct packet *p) {
	return rte_pktmbuf_mtod_offset(
		p->mbuf,
		struct rte_gre_hdr *,
		p->network_header.offset + sizeof(struct rte_ipv4_hdr)
	);
}

static struct rte_gre_hdr *
pkt_gre_after_ip6(struct packet *p) {
	return rte_pktmbuf_mtod_offset(
		p->mbuf,
		struct rte_gre_hdr *,
		p->network_header.offset + sizeof(struct rte_ipv6_hdr)
	);
}

/* Verifiers */

static int
assert_gre_flags_zero(struct rte_gre_hdr *gre) {
	TEST_ASSERT_EQUAL(gre->c, 0, "GRE c flag must be 0");
	TEST_ASSERT_EQUAL(gre->k, 0, "GRE k flag must be 0");
	TEST_ASSERT_EQUAL(gre->s, 0, "GRE s flag must be 0");
	TEST_ASSERT_EQUAL(gre->ver, 0, "GRE version must be 0");
	TEST_ASSERT_EQUAL(gre->res1, 0, "GRE res1 must be 0");
	TEST_ASSERT_EQUAL(gre->res2, 0, "GRE res2 must be 0");
	TEST_ASSERT_EQUAL(gre->res3, 0, "GRE res3 must be 0");
	return TEST_SUCCESS;
}

static int
assert_outer_ip4_cksum_ok(struct rte_ipv4_hdr *outer) {
	uint16_t saved = outer->hdr_checksum;
	outer->hdr_checksum = 0;
	uint16_t expected = rte_ipv4_cksum(outer);
	outer->hdr_checksum = saved;
	TEST_ASSERT_EQUAL(saved, expected, "outer IPv4 checksum mismatch");
	return TEST_SUCCESS;
}

static int
assert_post_encap_state(
	struct packet *p,
	uint16_t outer_ether_be,
	uint16_t expected_transport_offset
) {
	TEST_ASSERT_EQUAL(
		p->network_header.offset,
		sizeof(struct rte_ether_hdr),
		"network_header.offset"
	);
	TEST_ASSERT_EQUAL(
		p->network_header.type, outer_ether_be, "network_header.type"
	);
	TEST_ASSERT_EQUAL(
		p->transport_header.offset,
		expected_transport_offset,
		"transport_header.offset"
	);
	return TEST_SUCCESS;
}

static int
assert_inner_payload_preserved(
	struct packet *p, size_t outer_size, size_t inner_ip_size
) {
	uint8_t *got = rte_pktmbuf_mtod_offset(
		p->mbuf,
		uint8_t *,
		p->network_header.offset + outer_size + inner_ip_size
	);
	TEST_ASSERT(
		memcmp(got, inner_payload, INNER_PAYLOAD_LEN) == 0,
		"inner payload corrupted"
	);
	return TEST_SUCCESS;
}

/* Test cases */

static int
test_v4_in_v4(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip4(&p, DEFAULT_HEADROOM), "build");
	uint32_t pkt_len_before = rte_pktmbuf_pkt_len(p.mbuf);
	uint16_t tp_off_before = p.transport_header.offset;

	TEST_ASSERT_EQUAL(
		packet_ip4_encap_gre(&p, outer_dst4, outer_src4), 0, "rc"
	);

	uint32_t added =
		sizeof(struct rte_ipv4_hdr) + sizeof(struct rte_gre_hdr);
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_pkt_len(p.mbuf),
		pkt_len_before + added,
		"pkt_len grew by outer ip4 + gre"
	);
	TEST_ASSERT_SUCCESS(
		assert_post_encap_state(
			&p,
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
			tp_off_before + added
		),
		"post-encap state"
	);
	TEST_ASSERT_EQUAL(
		pkt_eth(&p)->ether_type,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
		"ether type"
	);

	struct rte_ipv4_hdr *outer = pkt_outer_ip4(&p);
	TEST_ASSERT_EQUAL(outer->version_ihl, 0x45, "version_ihl");
	TEST_ASSERT_EQUAL(
		outer->next_proto_id, IPPROTO_GRE, "next_proto_id GRE"
	);
	TEST_ASSERT_EQUAL(
		outer->type_of_service, INNER_TOS, "TOS copied from inner"
	);
	TEST_ASSERT_EQUAL(outer->time_to_live, INNER_TTL, "TTL copied");
	TEST_ASSERT_EQUAL(
		outer->packet_id,
		rte_cpu_to_be_16(INNER_PACKET_ID),
		"packet_id copied from inner"
	);
	TEST_ASSERT_EQUAL(
		outer->fragment_offset,
		rte_cpu_to_be_16(INNER_FRAG_FLAGS),
		"fragment_offset copied from inner"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(outer->total_length),
		(uint16_t)(added + sizeof(struct rte_ipv4_hdr) +
			   INNER_PAYLOAD_LEN),
		"total_length"
	);
	TEST_ASSERT(
		memcmp(&outer->src_addr, outer_src4, NET4_LEN) == 0, "outer src"
	);
	TEST_ASSERT(
		memcmp(&outer->dst_addr, outer_dst4, NET4_LEN) == 0, "outer dst"
	);
	TEST_ASSERT_SUCCESS(assert_outer_ip4_cksum_ok(outer), "cksum");

	struct rte_gre_hdr *gre = pkt_gre_after_ip4(&p);
	TEST_ASSERT_EQUAL(
		gre->proto,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
		"GRE proto = inner ether type"
	);
	TEST_ASSERT_SUCCESS(assert_gre_flags_zero(gre), "GRE flags zero");

	struct rte_ipv4_hdr *inner = (struct rte_ipv4_hdr *)(gre + 1);
	TEST_ASSERT(
		memcmp(&inner->src_addr, inner_src4, NET4_LEN) == 0,
		"inner src preserved"
	);
	TEST_ASSERT(
		memcmp(&inner->dst_addr, inner_dst4, NET4_LEN) == 0,
		"inner dst preserved"
	);
	TEST_ASSERT_SUCCESS(
		assert_inner_payload_preserved(
			&p, added, sizeof(struct rte_ipv4_hdr)
		),
		"inner payload"
	);

	free_packet(&p);
	return TEST_SUCCESS;
}

static int
test_v6_in_v4(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip6(&p, DEFAULT_HEADROOM), "build");
	uint32_t pkt_len_before = rte_pktmbuf_pkt_len(p.mbuf);
	uint16_t tp_off_before = p.transport_header.offset;

	TEST_ASSERT_EQUAL(
		packet_ip4_encap_gre(&p, outer_dst4, outer_src4), 0, "rc"
	);

	uint32_t added =
		sizeof(struct rte_ipv4_hdr) + sizeof(struct rte_gre_hdr);
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_pkt_len(p.mbuf),
		pkt_len_before + added,
		"pkt_len grew by outer ip4 + gre"
	);
	TEST_ASSERT_SUCCESS(
		assert_post_encap_state(
			&p,
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
			tp_off_before + added
		),
		"post-encap state"
	);
	TEST_ASSERT_EQUAL(
		pkt_eth(&p)->ether_type,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
		"ether type"
	);

	struct rte_ipv4_hdr *outer = pkt_outer_ip4(&p);
	TEST_ASSERT_EQUAL(
		outer->next_proto_id, IPPROTO_GRE, "next_proto_id GRE"
	);
	TEST_ASSERT_EQUAL(
		outer->type_of_service,
		INNER_TOS,
		"TOS copied from inner traffic class"
	);
	TEST_ASSERT_EQUAL(
		outer->time_to_live, INNER_TTL, "TTL from hop_limits"
	);
	TEST_ASSERT_EQUAL(
		outer->packet_id,
		rte_cpu_to_be_16(0x01),
		"packet_id set when inner has no equivalent"
	);
	TEST_ASSERT_EQUAL(outer->fragment_offset, 0, "fragment_offset zeroed");
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(outer->total_length),
		(uint16_t)(added + sizeof(struct rte_ipv6_hdr) +
			   INNER_PAYLOAD_LEN),
		"total_length"
	);
	TEST_ASSERT_SUCCESS(assert_outer_ip4_cksum_ok(outer), "cksum");

	struct rte_gre_hdr *gre = pkt_gre_after_ip4(&p);
	TEST_ASSERT_EQUAL(
		gre->proto,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
		"GRE proto = inner ether type"
	);
	TEST_ASSERT_SUCCESS(assert_gre_flags_zero(gre), "GRE flags zero");

	struct rte_ipv6_hdr *inner = (struct rte_ipv6_hdr *)(gre + 1);
	TEST_ASSERT(
		memcmp(inner->src_addr, inner_src6, NET6_LEN) == 0,
		"inner src preserved"
	);
	TEST_ASSERT(
		memcmp(inner->dst_addr, inner_dst6, NET6_LEN) == 0,
		"inner dst preserved"
	);
	TEST_ASSERT_SUCCESS(
		assert_inner_payload_preserved(
			&p, added, sizeof(struct rte_ipv6_hdr)
		),
		"inner payload"
	);

	free_packet(&p);
	return TEST_SUCCESS;
}

static int
test_v4_in_v6(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip4(&p, DEFAULT_HEADROOM), "build");
	uint32_t pkt_len_before = rte_pktmbuf_pkt_len(p.mbuf);
	uint16_t tp_off_before = p.transport_header.offset;

	TEST_ASSERT_EQUAL(
		packet_ip6_encap_gre(&p, outer_dst6, outer_src6), 0, "rc"
	);

	uint32_t added =
		sizeof(struct rte_ipv6_hdr) + sizeof(struct rte_gre_hdr);
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_pkt_len(p.mbuf),
		pkt_len_before + added,
		"pkt_len grew by outer ip6 + gre"
	);
	TEST_ASSERT_SUCCESS(
		assert_post_encap_state(
			&p,
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
			tp_off_before + added
		),
		"post-encap state"
	);
	TEST_ASSERT_EQUAL(
		pkt_eth(&p)->ether_type,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
		"ether type"
	);

	struct rte_ipv6_hdr *outer = pkt_outer_ip6(&p);
	TEST_ASSERT_EQUAL(outer->proto, IPPROTO_GRE, "next header GRE");
	TEST_ASSERT_EQUAL(outer->hop_limits, INNER_TTL, "hop_limits");
	TEST_ASSERT_EQUAL(
		(rte_be_to_cpu_32(outer->vtc_flow) >> 28) & 0xF, 6, "version 6"
	);
	TEST_ASSERT_EQUAL(
		(rte_be_to_cpu_32(outer->vtc_flow) >> 20) & 0xFF,
		INNER_TOS,
		"traffic class copied from inner TOS"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_32(outer->vtc_flow) & 0xFFFFF,
		0,
		"flow label zeroed when synthesised from v4 inner"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(outer->payload_len),
		(uint16_t)(sizeof(struct rte_gre_hdr) +
			   sizeof(struct rte_ipv4_hdr) + INNER_PAYLOAD_LEN),
		"payload_len = gre + inner"
	);
	TEST_ASSERT(
		memcmp(outer->src_addr, outer_src6, NET6_LEN) == 0, "outer src"
	);
	TEST_ASSERT(
		memcmp(outer->dst_addr, outer_dst6, NET6_LEN) == 0, "outer dst"
	);

	struct rte_gre_hdr *gre = pkt_gre_after_ip6(&p);
	TEST_ASSERT_EQUAL(
		gre->proto,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
		"GRE proto = inner ether type"
	);
	TEST_ASSERT_SUCCESS(assert_gre_flags_zero(gre), "GRE flags zero");

	struct rte_ipv4_hdr *inner = (struct rte_ipv4_hdr *)(gre + 1);
	TEST_ASSERT(
		memcmp(&inner->src_addr, inner_src4, NET4_LEN) == 0,
		"inner src preserved"
	);
	TEST_ASSERT(
		memcmp(&inner->dst_addr, inner_dst4, NET4_LEN) == 0,
		"inner dst preserved"
	);
	TEST_ASSERT_SUCCESS(
		assert_inner_payload_preserved(
			&p, added, sizeof(struct rte_ipv4_hdr)
		),
		"inner payload"
	);

	free_packet(&p);
	return TEST_SUCCESS;
}

static int
test_v6_in_v6(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip6(&p, DEFAULT_HEADROOM), "build");
	uint32_t pkt_len_before = rte_pktmbuf_pkt_len(p.mbuf);
	uint16_t tp_off_before = p.transport_header.offset;

	TEST_ASSERT_EQUAL(
		packet_ip6_encap_gre(&p, outer_dst6, outer_src6), 0, "rc"
	);

	uint32_t added =
		sizeof(struct rte_ipv6_hdr) + sizeof(struct rte_gre_hdr);
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_pkt_len(p.mbuf),
		pkt_len_before + added,
		"pkt_len grew by outer ip6 + gre"
	);
	TEST_ASSERT_SUCCESS(
		assert_post_encap_state(
			&p,
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
			tp_off_before + added
		),
		"post-encap state"
	);

	struct rte_ipv6_hdr *outer = pkt_outer_ip6(&p);
	TEST_ASSERT_EQUAL(outer->proto, IPPROTO_GRE, "next header GRE");
	TEST_ASSERT_EQUAL(outer->hop_limits, INNER_TTL, "hop_limits");
	TEST_ASSERT_EQUAL(
		outer->vtc_flow,
		rte_cpu_to_be_32((0x6u << 28) | ((uint32_t)INNER_TOS << 20)),
		"vtc_flow copied verbatim from inner"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(outer->payload_len),
		(uint16_t)(sizeof(struct rte_gre_hdr) +
			   sizeof(struct rte_ipv6_hdr) + INNER_PAYLOAD_LEN),
		"payload_len = gre + inner"
	);
	TEST_ASSERT(
		memcmp(outer->src_addr, outer_src6, NET6_LEN) == 0, "outer src"
	);
	TEST_ASSERT(
		memcmp(outer->dst_addr, outer_dst6, NET6_LEN) == 0, "outer dst"
	);

	struct rte_gre_hdr *gre = pkt_gre_after_ip6(&p);
	TEST_ASSERT_EQUAL(
		gre->proto,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
		"GRE proto = inner ether type"
	);
	TEST_ASSERT_SUCCESS(assert_gre_flags_zero(gre), "GRE flags zero");

	struct rte_ipv6_hdr *inner = (struct rte_ipv6_hdr *)(gre + 1);
	TEST_ASSERT(
		memcmp(inner->src_addr, inner_src6, NET6_LEN) == 0,
		"inner src preserved"
	);
	TEST_ASSERT(
		memcmp(inner->dst_addr, inner_dst6, NET6_LEN) == 0,
		"inner dst preserved"
	);
	TEST_ASSERT_SUCCESS(
		assert_inner_payload_preserved(
			&p, added, sizeof(struct rte_ipv6_hdr)
		),
		"inner payload"
	);

	free_packet(&p);
	return TEST_SUCCESS;
}

static int
test_unknown_in_v4(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_unknown(&p), "build");
	struct pkt_snapshot s;
	snapshot(&p, &s);

	TEST_ASSERT_EQUAL(
		packet_ip4_encap_gre(&p, outer_dst4, outer_src4),
		-1,
		"must reject unsupported inner network type"
	);
	TEST_ASSERT_SUCCESS(assert_unchanged(&p, &s), "packet untouched on -1");

	free_packet(&p);
	return TEST_SUCCESS;
}

static int
test_unknown_in_v6(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_unknown(&p), "build");
	struct pkt_snapshot s;
	snapshot(&p, &s);

	TEST_ASSERT_EQUAL(
		packet_ip6_encap_gre(&p, outer_dst6, outer_src6),
		-1,
		"must reject unsupported inner network type"
	);
	TEST_ASSERT_SUCCESS(assert_unchanged(&p, &s), "packet untouched on -1");

	free_packet(&p);
	return TEST_SUCCESS;
}

static int
test_short_headroom_v4(void) {
	uint16_t required =
		sizeof(struct rte_ipv4_hdr) + sizeof(struct rte_gre_hdr);
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip4(&p, required - 2), "build");
	struct pkt_snapshot s;
	snapshot(&p, &s);

	TEST_ASSERT_EQUAL(
		packet_ip4_encap_gre(&p, outer_dst4, outer_src4),
		-1,
		"must fail when headroom is below the required prepend size"
	);
	TEST_ASSERT_SUCCESS(assert_unchanged(&p, &s), "packet untouched on -1");

	free_packet(&p);
	return TEST_SUCCESS;
}

static int
test_short_headroom_v6(void) {
	uint16_t required =
		sizeof(struct rte_ipv6_hdr) + sizeof(struct rte_gre_hdr);
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip6(&p, required - 2), "build");
	struct pkt_snapshot s;
	snapshot(&p, &s);

	TEST_ASSERT_EQUAL(
		packet_ip6_encap_gre(&p, outer_dst6, outer_src6),
		-1,
		"must fail when headroom is below the required prepend size"
	);
	TEST_ASSERT_SUCCESS(assert_unchanged(&p, &s), "packet untouched on -1");

	free_packet(&p);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	LOG(INFO, "=== Starting GRE encap test suite ===");

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"v4_in_v4", test_v4_in_v4},
		{"v6_in_v4", test_v6_in_v4},
		{"v4_in_v6", test_v4_in_v6},
		{"v6_in_v6", test_v6_in_v6},
		{"unknown_in_v4", test_unknown_in_v4},
		{"unknown_in_v6", test_unknown_in_v6},
		{"short_headroom_v4", test_short_headroom_v4},
		{"short_headroom_v6", test_short_headroom_v6},
	};

	size_t total = sizeof(tests) / sizeof(tests[0]);
	size_t failed = 0;

	for (size_t i = 0; i < total; i++) {
		LOG(INFO, "[%zu/%zu] running %s...", i + 1, total, tests[i].name
		);
		if (tests[i].fn() != TEST_SUCCESS) {
			LOG(ERROR, "%s FAILED", tests[i].name);
			failed++;
		} else {
			LOG(INFO, "%s passed", tests[i].name);
		}
	}

	if (failed == 0) {
		LOG(INFO, "=== All %zu GRE tests passed! ===", total);
	} else {
		LOG(ERROR, "=== %zu/%zu GRE tests failed ===", failed, total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
