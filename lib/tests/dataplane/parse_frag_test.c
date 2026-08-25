#include <netinet/in.h>
#include <stdbool.h>
#include <stdint.h>
#include <string.h>

#include <rte_byteorder.h>
#include <rte_ether.h>
#include <rte_gre.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_udp.h>

#include "common/network.h"
#include "common/test_assert.h"

#include "lib/dataplane/packet/decap.h"
#include "lib/dataplane/packet/encap.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/logging/log.h"
#include "lib/utils/packet.h"

#define DEFAULT_HEADROOM 128
#define DEFAULT_TAILROOM 256

// Room for a UDP header plus a few bytes, so parse_packet reaches the
// transport layer instead of bailing out on a truncated header.
#define PAYLOAD_LEN (sizeof(struct rte_udp_hdr) + 8)

#define TTL 64

// Bytes that packet_decap strips off a GRE-in-IPv4 frame, namely the outer
// IPv4 header and a GRE header carrying none of the optional fields.
#define TUNNEL_HDRS_LEN                                                        \
	(sizeof(struct rte_ipv4_hdr) + sizeof(struct rte_gre_hdr))

static const uint8_t src4[NET4_LEN] = {192, 168, 1, 1};
static const uint8_t dst4[NET4_LEN] = {192, 168, 1, 2};

static const uint8_t outer_src4[NET4_LEN] = {10, 0, 0, 1};
static const uint8_t outer_dst4[NET4_LEN] = {10, 0, 0, 2};

static const uint8_t src6[NET6_LEN] = {
	0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
};
static const uint8_t dst6[NET6_LEN] = {
	0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2
};

// Builds eth + IPv4 + UDP-sized payload carrying the given raw fragment field
// and runs parse_packet on it.
static int
build_ip4(struct packet *p, uint16_t fragment_offset_host) {
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv4_hdr) + PAYLOAD_LEN;
	memset(p, 0, sizeof(*p));
	p->mbuf = alloc_mbuf(DEFAULT_HEADROOM, pkt_len, DEFAULT_TAILROOM);
	if (p->mbuf == NULL) {
		return -1;
	}

	struct rte_ether_hdr *eth =
		rte_pktmbuf_mtod(p->mbuf, struct rte_ether_hdr *);
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ip4 = (struct rte_ipv4_hdr *)(eth + 1);
	ip4->version_ihl = 0x45;
	ip4->total_length = rte_cpu_to_be_16(sizeof(*ip4) + PAYLOAD_LEN);
	ip4->fragment_offset = rte_cpu_to_be_16(fragment_offset_host);
	ip4->time_to_live = TTL;
	ip4->next_proto_id = IPPROTO_UDP;
	memcpy(&ip4->src_addr, src4, NET4_LEN);
	memcpy(&ip4->dst_addr, dst4, NET4_LEN);
	ip4->hdr_checksum = 0;
	ip4->hdr_checksum = rte_ipv4_cksum(ip4);

	return parse_packet(p);
}

// Builds eth + IPv6 + optional fragment extension + UDP-sized payload and runs
// parse_packet on it.
static int
build_ip6(struct packet *p, bool with_fragment, uint16_t offset_flag_host) {
	uint16_t ext_len =
		with_fragment ? sizeof(struct yanet_ipv6_ext_fragment) : 0;
	uint16_t payload_len = ext_len + PAYLOAD_LEN;
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv6_hdr) + payload_len;
	memset(p, 0, sizeof(*p));
	p->mbuf = alloc_mbuf(DEFAULT_HEADROOM, pkt_len, DEFAULT_TAILROOM);
	if (p->mbuf == NULL) {
		return -1;
	}

	struct rte_ether_hdr *eth =
		rte_pktmbuf_mtod(p->mbuf, struct rte_ether_hdr *);
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	struct rte_ipv6_hdr *ip6 = (struct rte_ipv6_hdr *)(eth + 1);
	ip6->vtc_flow = rte_cpu_to_be_32(0x6u << 28);
	ip6->payload_len = rte_cpu_to_be_16(payload_len);
	ip6->proto = with_fragment ? IPPROTO_FRAGMENT : IPPROTO_UDP;
	ip6->hop_limits = TTL;
	memcpy(ip6->src_addr, src6, NET6_LEN);
	memcpy(ip6->dst_addr, dst6, NET6_LEN);

	if (with_fragment) {
		struct yanet_ipv6_ext_fragment *ext =
			(struct yanet_ipv6_ext_fragment *)(ip6 + 1);
		ext->next_header = IPPROTO_UDP;
		ext->reserved = 0;
		ext->offset_flag = rte_cpu_to_be_16(offset_flag_host);
		ext->identification = rte_cpu_to_be_32(0xdeadbeef);
	}

	return parse_packet(p);
}

// Builds a GRE-in-IPv4 packet carrying an IPv4 or an IPv6 inner packet, parsed
// down to its outer headers.
//
// Prepending the tunnel leaves transport_header describing the inner UDP
// header, so the frame is parsed again to make transport_header name the outer
// GRE header, which is what packet_decap and pkt_gre read.
static int
build_gre_encapped(struct packet *p, bool inner_v6) {
	int rc = inner_v6 ? build_ip6(p, false, 0) : build_ip4(p, 0);
	if (rc != 0) {
		return -1;
	}
	if (packet_ip4_encap_gre(p, outer_dst4, outer_src4) != 0) {
		return -1;
	}
	return parse_packet(p);
}

static struct rte_gre_hdr *
pkt_gre(struct packet *p) {
	return rte_pktmbuf_mtod_offset(
		p->mbuf, struct rte_gre_hdr *, p->transport_header.offset
	);
}

static int
assert_fragment_state(
	const struct packet *p, int expect_fragmented, uint16_t expect_offset
) {
	TEST_ASSERT_EQUAL(
		(p->flags >> PACKET_FLAG_FRAGMENTED) & 1,
		expect_fragmented,
		"PACKET_FLAG_FRAGMENTED"
	);
	TEST_ASSERT_EQUAL(p->fragment_offset, expect_offset, "fragment_offset");
	return TEST_SUCCESS;
}

static int
run_ip4_case(
	uint16_t fragment_offset_host,
	int expect_fragmented,
	uint16_t expect_offset
) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_ip4(&p, fragment_offset_host), "build");
	TEST_ASSERT_SUCCESS(
		assert_fragment_state(&p, expect_fragmented, expect_offset),
		"fragment state"
	);
	free_packet(&p);
	return TEST_SUCCESS;
}

static int
run_ip6_case(
	bool with_fragment,
	uint16_t offset_flag_host,
	int expect_fragmented,
	uint16_t expect_offset
) {
	struct packet p;
	TEST_ASSERT_SUCCESS(
		build_ip6(&p, with_fragment, offset_flag_host), "build"
	);
	TEST_ASSERT_SUCCESS(
		assert_fragment_state(&p, expect_fragmented, expect_offset),
		"fragment state"
	);
	free_packet(&p);
	return TEST_SUCCESS;
}

// Verifies that an IPv4 packet with an empty fragment field is not fragmented.
static int
test_ip4_plain(void) {
	return run_ip4_case(0x0000, 0, 0);
}

// Verifies that the IPv4 Don't Fragment bit alone does not mark a packet as
// fragmented, which is the ordinary case for most traffic.
static int
test_ip4_df_only(void) {
	return run_ip4_case(RTE_IPV4_HDR_DF_FLAG, 0, 0);
}

// Verifies that the IPv4 More Fragments bit alone marks a packet as fragmented
// at offset zero.
static int
test_ip4_mf_only(void) {
	return run_ip4_case(RTE_IPV4_HDR_MF_FLAG, 1, 0);
}

// Verifies that a nonzero IPv4 fragment offset marks a packet as fragmented and
// is reported in the header's own eight-byte units.
static int
test_ip4_offset_only(void) {
	return run_ip4_case(185, 1, 185);
}

// Verifies that the IPv4 Don't Fragment bit is excluded from the reported
// fragment offset.
static int
test_ip4_df_and_offset(void) {
	return run_ip4_case(RTE_IPV4_HDR_DF_FLAG | 185, 1, 185);
}

// Verifies that an IPv6 packet without a fragment extension is not fragmented.
static int
test_ip6_no_extension(void) {
	return run_ip6_case(false, 0x0000, 0, 0);
}

// Verifies that the IPv6 More Fragments bit alone marks a packet as fragmented
// at offset zero.
static int
test_ip6_mf_only(void) {
	return run_ip6_case(true, RTE_IPV6_EHDR_MF_MASK, 1, 0);
}

// Verifies that a nonzero IPv6 fragment offset marks a packet as fragmented and
// is reported shifted in place, as a byte offset.
static int
test_ip6_offset_only(void) {
	return run_ip6_case(true, 0x0BD0, 1, 0x0BD0);
}

// Verifies that the IPv6 fragment extension reserved bits are masked out, so a
// packet carrying only those is not fragmented.
static int
test_ip6_reserved_only(void) {
	return run_ip6_case(true, 0x0006, 0, 0);
}

// Decapsulates a GRE-encapsulated frame and checks that the tunnel is gone.
//
// The transport type and the packet length both differ between the outer and
// the inner frame, so either one fails if packet_decap reports success without
// stripping the tunnel. The network type differs only when the inner packet is
// IPv6.
static int
run_gre_accepted_case(bool inner_v6, uint16_t expect_ether_type) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_gre_encapped(&p, inner_v6), "build");
	TEST_ASSERT_EQUAL(
		p.transport_header.type, IPPROTO_GRE, "outer transport is GRE"
	);
	TEST_ASSERT_EQUAL(
		p.network_header.type,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
		"outer network type is IPv4"
	);
	uint32_t pkt_len_before = rte_pktmbuf_pkt_len(p.mbuf);
	p.recirc_remaining = 57;
	p.recirc_initialized = 1;

	TEST_ASSERT_EQUAL(packet_decap(&p), 0, "rc");
	TEST_ASSERT_EQUAL(
		p.recirc_remaining,
		57,
		"successful decap preserves remaining recirculation budget"
	);
	TEST_ASSERT_EQUAL(
		p.recirc_initialized,
		1,
		"successful decap preserves recirculation initialization state"
	);
	TEST_ASSERT_EQUAL(
		p.network_header.type,
		expect_ether_type,
		"network type after decap"
	);
	TEST_ASSERT_EQUAL(
		p.transport_header.type,
		IPPROTO_UDP,
		"transport type after decap"
	);
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_pkt_len(p.mbuf),
		pkt_len_before - TUNNEL_HDRS_LEN,
		"pkt_len after decap"
	);

	free_packet(&p);
	return TEST_SUCCESS;
}

static int
assert_decap_failure_preserves_recirc(struct packet *packet) {
	packet->recirc_remaining = 57;
	packet->recirc_initialized = 1;
	TEST_ASSERT_EQUAL(packet_decap(packet), -1, "rc");
	TEST_ASSERT_EQUAL(
		packet->recirc_remaining,
		57,
		"failed decap preserves remaining recirculation budget"
	);
	TEST_ASSERT_EQUAL(
		packet->recirc_initialized,
		1,
		"failed decap preserves recirculation initialization state"
	);
	return TEST_SUCCESS;
}

// Verifies that a GRE header with cleared reserved bits and version is
// accepted, and that the encapsulated IPv4 packet is unwrapped.
static int
test_gre_accepted(void) {
	return run_gre_accepted_case(
		false, rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)
	);
}

// Verifies that an encapsulated IPv6 packet is unwrapped and that its network
// header type replaces the outer IPv4 one.
static int
test_gre_accepted_inner_v6(void) {
	return run_gre_accepted_case(
		true, rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)
	);
}

// Verifies that a GRE header carrying a nonzero version is rejected.
static int
test_gre_reject_ver(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_gre_encapped(&p, false), "build");
	pkt_gre(&p)->ver = 1;
	TEST_ASSERT_SUCCESS(
		assert_decap_failure_preserves_recirc(&p),
		"failed decap changed recirculation state"
	);
	free_packet(&p);
	return TEST_SUCCESS;
}

// Verifies that a GRE header carrying a nonzero res1 reserved bit is rejected.
static int
test_gre_reject_res1(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_gre_encapped(&p, false), "build");
	pkt_gre(&p)->res1 = 1;
	TEST_ASSERT_SUCCESS(
		assert_decap_failure_preserves_recirc(&p),
		"failed decap changed recirculation state"
	);
	free_packet(&p);
	return TEST_SUCCESS;
}

// Verifies that a GRE header carrying nonzero res2 reserved bits is rejected.
static int
test_gre_reject_res2(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_gre_encapped(&p, false), "build");
	pkt_gre(&p)->res2 = 1;
	TEST_ASSERT_SUCCESS(
		assert_decap_failure_preserves_recirc(&p),
		"failed decap changed recirculation state"
	);
	free_packet(&p);
	return TEST_SUCCESS;
}

// Verifies that a GRE header carrying nonzero res3 reserved bits is rejected.
static int
test_gre_reject_res3(void) {
	struct packet p;
	TEST_ASSERT_SUCCESS(build_gre_encapped(&p, false), "build");
	pkt_gre(&p)->res3 = 1;
	TEST_ASSERT_SUCCESS(
		assert_decap_failure_preserves_recirc(&p),
		"failed decap changed recirculation state"
	);
	free_packet(&p);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	LOG(INFO, "=== Starting fragment and GRE mask test suite ===");

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"ip4_plain", test_ip4_plain},
		{"ip4_df_only", test_ip4_df_only},
		{"ip4_mf_only", test_ip4_mf_only},
		{"ip4_offset_only", test_ip4_offset_only},
		{"ip4_df_and_offset", test_ip4_df_and_offset},
		{"ip6_no_extension", test_ip6_no_extension},
		{"ip6_mf_only", test_ip6_mf_only},
		{"ip6_offset_only", test_ip6_offset_only},
		{"ip6_reserved_only", test_ip6_reserved_only},
		{"gre_accepted", test_gre_accepted},
		{"gre_accepted_inner_v6", test_gre_accepted_inner_v6},
		{"gre_reject_ver", test_gre_reject_ver},
		{"gre_reject_res1", test_gre_reject_res1},
		{"gre_reject_res2", test_gre_reject_res2},
		{"gre_reject_res3", test_gre_reject_res3},
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
		LOG(INFO,
		    "=== All %zu fragment and GRE tests passed! ===",
		    total);
	} else {
		LOG(ERROR,
		    "=== %zu/%zu fragment and GRE tests failed ===",
		    failed,
		    total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
