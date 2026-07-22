#include <netinet/in.h>
#include <stdint.h>
#include <string.h>

#include <rte_byteorder.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

#include "common/network.h"
#include "common/test_assert.h"

#include "lib/dataplane/packet/dscp.h"
#include "lib/dataplane/packet/encap.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/logging/log.h"
#include "lib/utils/packet.h"

#include "snapshot.h"

static const uint8_t inner_payload[] = "DSCP TEST PAYLOAD 123 111 987 TEST";

#define INNER_PAYLOAD_LEN (sizeof(inner_payload) - 1)

#define DEFAULT_HEADROOM 128
#define DEFAULT_TAILROOM 256

#define INNER_TTL 32

/*
 * TOS / traffic class bytes as DSCP << 2 | ECN.
 *
 * The inner and outer values differ in both halves so a copy that took the
 * whole byte, or that dropped the outer ECN, would produce something other
 * than EXPECTED_TOS.
 */
#define INNER_TOS 0xBA	  /* DSCP 46 (EF), ECN 0b10 */
#define OUTER_TOS 0x23	  /* DSCP 8 (CS1), ECN 0b11 */
#define EXPECTED_TOS 0xBB /* inner DSCP 46, outer ECN 0b11 */
#define NEVER_TOS 0x02	  /* DSCP cleared, inner ECN 0b10 kept */

#define OUTER_FLOW_LABEL 0x12345

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

/*
 * The inner protocol is set to UDP, but the bytes following the IP header are
 * the inner_payload string rather than a real UDP datagram — these tests
 * exercise the DSCP copy, not transport parsing.
 */
static int
build_eth_ip4(struct packet *p) {
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv4_hdr) + INNER_PAYLOAD_LEN;
	memset(p, 0, sizeof(*p));
	p->mbuf = alloc_mbuf(DEFAULT_HEADROOM, pkt_len, DEFAULT_TAILROOM);
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
	ip4->packet_id = 0;
	ip4->fragment_offset = 0;
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
build_eth_ip6(struct packet *p) {
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv6_hdr) + INNER_PAYLOAD_LEN;
	memset(p, 0, sizeof(*p));
	p->mbuf = alloc_mbuf(DEFAULT_HEADROOM, pkt_len, DEFAULT_TAILROOM);
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

static int
assert_outer_ip4_cksum_ok(struct rte_ipv4_hdr *outer) {
	uint16_t saved = outer->hdr_checksum;
	outer->hdr_checksum = 0;
	uint16_t expected = rte_ipv4_cksum(outer);
	outer->hdr_checksum = saved;
	TEST_ASSERT_EQUAL(saved, expected, "outer IPv4 checksum mismatch");
	return TEST_SUCCESS;
}

/*
 * Encapsulating with DSCP_MARK_ALWAYS copies the whole inner TOS byte into the
 * outer header, so every test first rewrites the outer TOS to a value that
 * differs from the inner one in both the DSCP and the ECN half. The checksum is
 * recomputed so the incremental update under test starts from a valid one.
 */
static void
set_outer_ip4_tos(struct packet *p, uint8_t tos) {
	struct rte_ipv4_hdr *outer = pkt_outer_ip4(p);
	outer->type_of_service = tos;
	outer->hdr_checksum = 0;
	outer->hdr_checksum = rte_ipv4_cksum(outer);
}

static void
set_outer_ip6_tc(struct packet *p, uint8_t tc, uint32_t flow_label) {
	struct rte_ipv6_hdr *outer = pkt_outer_ip6(p);
	outer->vtc_flow = rte_cpu_to_be_32(
		(0x6u << 28) | ((uint32_t)tc << 20) | flow_label
	);
}

static int
assert_outer_ip6_tc(struct packet *p, uint8_t tc, uint32_t flow_label) {
	uint32_t vtc_flow = rte_be_to_cpu_32(pkt_outer_ip6(p)->vtc_flow);

	TEST_ASSERT_EQUAL((vtc_flow >> 28) & 0xF, 6, "version must stay 6");
	TEST_ASSERT_EQUAL(
		(vtc_flow >> 20) & 0xFF, tc, "outer traffic class mismatch"
	);
	TEST_ASSERT_EQUAL(
		vtc_flow & 0xFFFFF, flow_label, "flow label must be preserved"
	);
	return TEST_SUCCESS;
}

static int
test_v4_in_v4(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip4(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip4_encap(&p, outer_dst4, outer_src4, DSCP_MARK_ALWAYS),
		"encap"
	);
	set_outer_ip4_tos(&p, OUTER_TOS);

	TEST_ASSERT_SUCCESS(packet_ip4_copy_inner_dscp(&p), "rc");

	struct rte_ipv4_hdr *outer = pkt_outer_ip4(&p);
	TEST_ASSERT_EQUAL(
		outer->type_of_service,
		EXPECTED_TOS,
		"inner DSCP copied, outer ECN preserved"
	);
	TEST_ASSERT_EQUAL(
		outer->next_proto_id, IPPROTO_IPIP, "outer proto untouched"
	);
	TEST_ASSERT_SUCCESS(assert_outer_ip4_cksum_ok(outer), "cksum");

	struct rte_ipv4_hdr *inner = (struct rte_ipv4_hdr *)(outer + 1);
	TEST_ASSERT_EQUAL(
		inner->type_of_service, INNER_TOS, "inner TOS untouched"
	);

	free_packet(&p);
	return TEST_SUCCESS;
}

static int
test_v6_in_v4(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip6(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip4_encap(&p, outer_dst4, outer_src4, DSCP_MARK_ALWAYS),
		"encap"
	);
	set_outer_ip4_tos(&p, OUTER_TOS);

	TEST_ASSERT_SUCCESS(packet_ip4_copy_inner_dscp(&p), "rc");

	struct rte_ipv4_hdr *outer = pkt_outer_ip4(&p);
	TEST_ASSERT_EQUAL(
		outer->type_of_service,
		EXPECTED_TOS,
		"inner traffic class DSCP copied, outer ECN preserved"
	);
	TEST_ASSERT_EQUAL(
		outer->next_proto_id, IPPROTO_IPV6, "outer proto untouched"
	);
	TEST_ASSERT_SUCCESS(assert_outer_ip4_cksum_ok(outer), "cksum");

	struct rte_ipv6_hdr *inner = (struct rte_ipv6_hdr *)(outer + 1);
	TEST_ASSERT_EQUAL(
		(rte_be_to_cpu_32(inner->vtc_flow) >> 20) & 0xFF,
		INNER_TOS,
		"inner traffic class untouched"
	);

	free_packet(&p);
	return TEST_SUCCESS;
}

static int
test_v4_in_v6(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip4(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip6_encap(&p, outer_dst6, outer_src6, DSCP_MARK_ALWAYS),
		"encap"
	);
	set_outer_ip6_tc(&p, OUTER_TOS, OUTER_FLOW_LABEL);

	TEST_ASSERT_SUCCESS(packet_ip6_copy_inner_dscp(&p), "rc");

	TEST_ASSERT_SUCCESS(
		assert_outer_ip6_tc(&p, EXPECTED_TOS, OUTER_FLOW_LABEL),
		"inner DSCP copied, outer ECN and flow label preserved"
	);
	TEST_ASSERT_EQUAL(
		pkt_outer_ip6(&p)->proto, IPPROTO_IPIP, "outer proto untouched"
	);

	struct rte_ipv4_hdr *inner =
		(struct rte_ipv4_hdr *)(pkt_outer_ip6(&p) + 1);
	TEST_ASSERT_EQUAL(
		inner->type_of_service, INNER_TOS, "inner TOS untouched"
	);

	free_packet(&p);
	return TEST_SUCCESS;
}

static int
test_v6_in_v6(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip6(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip6_encap(&p, outer_dst6, outer_src6, DSCP_MARK_ALWAYS),
		"encap"
	);
	set_outer_ip6_tc(&p, OUTER_TOS, OUTER_FLOW_LABEL);

	TEST_ASSERT_SUCCESS(packet_ip6_copy_inner_dscp(&p), "rc");

	TEST_ASSERT_SUCCESS(
		assert_outer_ip6_tc(&p, EXPECTED_TOS, OUTER_FLOW_LABEL),
		"inner DSCP copied, outer ECN and flow label preserved"
	);
	TEST_ASSERT_EQUAL(
		pkt_outer_ip6(&p)->proto, IPPROTO_IPV6, "outer proto untouched"
	);

	struct rte_ipv6_hdr *inner =
		(struct rte_ipv6_hdr *)(pkt_outer_ip6(&p) + 1);
	TEST_ASSERT_EQUAL(
		(rte_be_to_cpu_32(inner->vtc_flow) >> 20) & 0xFF,
		INNER_TOS,
		"inner traffic class untouched"
	);

	free_packet(&p);
	return TEST_SUCCESS;
}

/*
 * Encapsulating with DSCP_MARK_ALWAYS already copies the inner TOS, so the
 * outer DSCP matches the inner one before the call and the packet must come
 * back byte for byte identical — in particular the checksum must not drift.
 */
static int
test_already_equal_v4(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip4(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip4_encap(&p, outer_dst4, outer_src4, DSCP_MARK_ALWAYS),
		"encap"
	);

	struct pkt_snapshot s;
	snapshot(&p, &s);

	TEST_ASSERT_SUCCESS(packet_ip4_copy_inner_dscp(&p), "rc");
	TEST_ASSERT_SUCCESS(
		assert_unchanged(&p, &s), "packet must be untouched"
	);

	free_packet(&p);
	return TEST_SUCCESS;
}

/*
 * The flag gates only the DSCP half of the byte: DSCP_MARK_ALWAYS carries the
 * inner class across, every other value leaves the outer DSCP at zero, and the
 * ECN bits ride along either way.
 */
static int
test_encap_flag_v4(void) {
	struct packet p;

	TEST_ASSERT_SUCCESS(build_eth_ip4(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip4_encap(&p, outer_dst4, outer_src4, DSCP_MARK_NEVER),
		"encap never"
	);
	TEST_ASSERT_EQUAL(
		pkt_outer_ip4(&p)->type_of_service,
		NEVER_TOS,
		"never must clear the DSCP and keep the ECN"
	);
	TEST_ASSERT_SUCCESS(
		assert_outer_ip4_cksum_ok(pkt_outer_ip4(&p)), "cksum"
	);
	free_packet(&p);

	TEST_ASSERT_SUCCESS(build_eth_ip4(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip4_encap(&p, outer_dst4, outer_src4, DSCP_MARK_DEFAULT),
		"encap default"
	);
	TEST_ASSERT_EQUAL(
		pkt_outer_ip4(&p)->type_of_service,
		NEVER_TOS,
		"default must not carry the DSCP either"
	);
	free_packet(&p);

	TEST_ASSERT_SUCCESS(build_eth_ip4(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip4_encap(&p, outer_dst4, outer_src4, DSCP_MARK_ALWAYS),
		"encap always"
	);
	TEST_ASSERT_EQUAL(
		pkt_outer_ip4(&p)->type_of_service,
		INNER_TOS,
		"always must carry the inner DSCP and ECN"
	);
	TEST_ASSERT_SUCCESS(
		assert_outer_ip4_cksum_ok(pkt_outer_ip4(&p)), "cksum"
	);
	free_packet(&p);

	return TEST_SUCCESS;
}

static int
test_encap_flag_v6(void) {
	struct packet p;

	TEST_ASSERT_SUCCESS(build_eth_ip6(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip6_encap(&p, outer_dst6, outer_src6, DSCP_MARK_NEVER),
		"encap never"
	);
	TEST_ASSERT_SUCCESS(
		assert_outer_ip6_tc(&p, NEVER_TOS, 0),
		"never must clear the DSCP and keep the ECN"
	);
	free_packet(&p);

	TEST_ASSERT_SUCCESS(build_eth_ip6(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip6_encap(&p, outer_dst6, outer_src6, DSCP_MARK_DEFAULT),
		"encap default"
	);
	TEST_ASSERT_SUCCESS(
		assert_outer_ip6_tc(&p, NEVER_TOS, 0),
		"default must not carry the DSCP either"
	);
	free_packet(&p);

	TEST_ASSERT_SUCCESS(build_eth_ip6(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip6_encap(&p, outer_dst6, outer_src6, DSCP_MARK_ALWAYS),
		"encap always"
	);
	TEST_ASSERT_SUCCESS(
		assert_outer_ip6_tc(&p, INNER_TOS, 0),
		"always must carry the inner DSCP and ECN"
	);
	free_packet(&p);

	return TEST_SUCCESS;
}

static int
test_rejects_non_ip_payload_v4(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip4(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip4_encap(&p, outer_dst4, outer_src4, DSCP_MARK_ALWAYS),
		"encap"
	);
	pkt_outer_ip4(&p)->next_proto_id = IPPROTO_UDP;

	struct pkt_snapshot s;
	snapshot(&p, &s);

	TEST_ASSERT_EQUAL(
		packet_ip4_copy_inner_dscp(&p),
		-1,
		"must reject a payload that is not an inner IP packet"
	);
	TEST_ASSERT_SUCCESS(assert_unchanged(&p, &s), "packet untouched on -1");

	free_packet(&p);
	return TEST_SUCCESS;
}

static int
test_rejects_non_ip_payload_v6(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_eth_ip6(&p), "build");
	TEST_ASSERT_SUCCESS(
		packet_ip6_encap(&p, outer_dst6, outer_src6, DSCP_MARK_ALWAYS),
		"encap"
	);
	pkt_outer_ip6(&p)->proto = IPPROTO_UDP;

	struct pkt_snapshot s;
	snapshot(&p, &s);

	TEST_ASSERT_EQUAL(
		packet_ip6_copy_inner_dscp(&p),
		-1,
		"must reject a payload that is not an inner IP packet"
	);
	TEST_ASSERT_SUCCESS(assert_unchanged(&p, &s), "packet untouched on -1");

	free_packet(&p);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	LOG(INFO, "=== Starting encap DSCP copy test suite ===");

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"v4_in_v4", test_v4_in_v4},
		{"v6_in_v4", test_v6_in_v4},
		{"v4_in_v6", test_v4_in_v6},
		{"v6_in_v6", test_v6_in_v6},
		{"already_equal_v4", test_already_equal_v4},
		{"encap_flag_v4", test_encap_flag_v4},
		{"encap_flag_v6", test_encap_flag_v6},
		{"rejects_non_ip_payload_v4", test_rejects_non_ip_payload_v4},
		{"rejects_non_ip_payload_v6", test_rejects_non_ip_payload_v6},
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
		LOG(INFO, "=== All %zu DSCP tests passed! ===", total);
	} else {
		LOG(ERROR, "=== %zu/%zu DSCP tests failed ===", failed, total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
