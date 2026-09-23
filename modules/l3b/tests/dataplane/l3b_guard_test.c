// Verifies the length guards of the l3b packet readers that answer or
// rewrite in place: the echo predicate may classify only packets carrying a
// complete ICMP header (its reply path rewrites the whole header), and the
// MSS clamp may walk declared options only while they stay inside the frame.

#include <netinet/in.h>
#include <netinet/ip_icmp.h>
#include <stdint.h>
#include <string.h>

#include <rte_byteorder.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>

#include "common/test_assert.h"

#include "lib/dataplane/packet/packet.h"
#include "lib/logging/log.h"
#include "lib/utils/packet.h"

#include "modules/l3b/dataplane/process.h"

#define DEFAULT_HEADROOM 128
#define DEFAULT_TAILROOM 128

static int
build_ip4_packet_tailroom(
	struct packet *p,
	uint8_t proto,
	uint16_t transport_bytes,
	uint16_t tailroom
) {
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv4_hdr) + transport_bytes;
	memset(p, 0, sizeof(*p));
	p->mbuf = alloc_mbuf(DEFAULT_HEADROOM, pkt_len, tailroom);
	TEST_ASSERT_NOT_NULL(p->mbuf, "alloc_mbuf failed");

	uint8_t *data = rte_pktmbuf_mtod(p->mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ip4 = (struct rte_ipv4_hdr *)(eth + 1);
	ip4->version_ihl = 0x45;
	ip4->total_length = rte_cpu_to_be_16(sizeof(*ip4) + transport_bytes);
	ip4->time_to_live = 64;
	ip4->next_proto_id = proto;
	uint8_t sip[NET4_LEN] = {10, 0, 0, 1};
	uint8_t dip[NET4_LEN] = {192, 168, 1, 1};
	memcpy(&ip4->src_addr, sip, NET4_LEN);
	memcpy(&ip4->dst_addr, dip, NET4_LEN);
	return TEST_SUCCESS;
}

static int
build_ip4_packet(struct packet *p, uint8_t proto, uint16_t transport_bytes) {
	return build_ip4_packet_tailroom(
		p, proto, transport_bytes, DEFAULT_TAILROOM
	);
}

// Verifies that an echo request whose transport region holds only the type
// byte is not classified as an echo: the reply path rewrites the whole
// eight-byte header, so a one-byte read would feed it bytes beyond the frame.
static int
test_icmp_type_refuses_truncated_header(void) {
	struct packet p;
	build_ip4_packet(&p, IPPROTO_ICMP, 1);
	TEST_ASSERT_SUCCESS(parse_packet(&p), "parse");
	uint8_t *icmp = rte_pktmbuf_mtod_offset(
		p.mbuf, uint8_t *, p.transport_header.offset
	);
	icmp[0] = ICMP_ECHO;
	TEST_ASSERT_EQUAL(l3b_packet_icmp_type(&p), -1, "truncated icmp type");
	TEST_ASSERT(
		l3b_packet_is_icmp_echo(&p) == false,
		"truncated echo request classified as echo"
	);
	free_packet(&p);
	return TEST_SUCCESS;
}

// Verifies that a complete echo request header is still classified, so the
// guard does not over-refuse the ordinary echo path.
static int
test_icmp_type_accepts_full_header(void) {
	struct packet p;
	build_ip4_packet(&p, IPPROTO_ICMP, 8);
	TEST_ASSERT_SUCCESS(parse_packet(&p), "parse");
	uint8_t *icmp = rte_pktmbuf_mtod_offset(
		p.mbuf, uint8_t *, p.transport_header.offset
	);
	icmp[0] = ICMP_ECHO;
	icmp[1] = 0;
	icmp[2] = 0;
	icmp[3] = 0;
	icmp[4] = 0x12;
	icmp[5] = 0x34;
	icmp[6] = 0;
	icmp[7] = 7;
	TEST_ASSERT_EQUAL(
		l3b_packet_icmp_type(&p), ICMP_ECHO, "full header type"
	);
	TEST_ASSERT(
		l3b_packet_is_icmp_echo(&p) == true,
		"complete echo request not classified as echo"
	);
	free_packet(&p);
	return TEST_SUCCESS;
}

// Verifies that the MSS clamp never walks TCP options declared beyond the
// frame: a SYN with data_off 0xF on a frame carrying a thirty-byte transport
// region must be left untouched, and no option byte may be read past the
// packet. The frame is sixty-four bytes, so the buffer allocation ends
// exactly with the packet and any unguarded option read lands beyond the
// allocation and trips the sanitizer.
static int
test_fix_mss_options_beyond_frame(void) {
	struct packet p;
	build_ip4_packet_tailroom(&p, IPPROTO_TCP, 30, 0);
	TEST_ASSERT_SUCCESS(parse_packet(&p), "parse");
	uint8_t *tcp_bytes = rte_pktmbuf_mtod_offset(
		p.mbuf, uint8_t *, p.transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_pkt_len(packet_to_mbuf(&p)),
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr) + 30,
		"frame size"
	);
	struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)tcp_bytes;
	tcp->src_port = rte_cpu_to_be_16(40000);
	tcp->dst_port = rte_cpu_to_be_16(443);
	tcp->tcp_flags = RTE_TCP_SYN_FLAG;
	tcp->data_off = 0xF0;

	uint8_t snapshot[30];
	memcpy(snapshot, tcp_bytes, sizeof(snapshot));

	l3b_fix_mss(&p);

	TEST_ASSERT(
		memcmp(tcp_bytes, snapshot, sizeof(snapshot)) == 0,
		"MSS clamp touched a packet with options beyond the frame"
	);
	free_packet(&p);
	return TEST_SUCCESS;
}

// Verifies that the MSS clamp still rewrites a real MSS option inside the
// frame: the guard must not over-refuse the ordinary clamp path.
static int
test_fix_mss_still_clamps_in_frame_options(void) {
	struct packet p;
	uint8_t opts[4] = {2, 4, 0xE1, 0x18}; // MSS 57368
	build_ip4_packet(&p, IPPROTO_TCP, sizeof(opts) + 20);
	TEST_ASSERT_SUCCESS(parse_packet(&p), "parse");
	uint8_t *tcp_bytes = rte_pktmbuf_mtod_offset(
		p.mbuf, uint8_t *, p.transport_header.offset
	);
	struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)tcp_bytes;
	tcp->src_port = rte_cpu_to_be_16(40000);
	tcp->dst_port = rte_cpu_to_be_16(443);
	tcp->tcp_flags = RTE_TCP_SYN_FLAG;
	tcp->data_off = (uint8_t)((20 + sizeof(opts)) / 4 << 4);
	memcpy(tcp_bytes + 20, opts, sizeof(opts));

	l3b_fix_mss(&p);

	TEST_ASSERT_EQUAL(
		tcp_bytes[20 + 2],
		(L3B_FIX_MSS_SIZE >> 8) & 0xFF,
		"MSS high byte"
	);
	TEST_ASSERT_EQUAL(
		tcp_bytes[20 + 3], L3B_FIX_MSS_SIZE & 0xFF, "MSS low byte"
	);
	free_packet(&p);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	// This test exercises the in-place readers only; the query declares
	// carried in by process.h stay unreferenced by design.
	(void)l3b_source_filter_ip4;
	(void)l3b_source_filter_ip6;
	(void)l3b_destination_filter_ip4;
	(void)l3b_destination_filter_ip6;

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"icmp_type_refuses_truncated_header",
		 test_icmp_type_refuses_truncated_header},
		{"icmp_type_accepts_full_header",
		 test_icmp_type_accepts_full_header},
		{"fix_mss_options_beyond_frame",
		 test_fix_mss_options_beyond_frame},
		{"fix_mss_still_clamps_in_frame_options",
		 test_fix_mss_still_clamps_in_frame_options},
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

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
