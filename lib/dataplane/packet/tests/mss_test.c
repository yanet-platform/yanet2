#include <netinet/in.h>
#include <stdalign.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <rte_byteorder.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "common/test_assert.h"
#include "lib/dataplane/packet/mss.h"
#include "lib/dataplane/packet/packet.h"
#include "logging/log.h"

#define TCP_OPT_KIND_EOL 0
#define TCP_OPT_KIND_NOP 1
#define TCP_OPT_KIND_MSS 2

#define DEFAULT_HEADROOM 128
#define DEFAULT_TAILROOM 256

#define SNAPSHOT_CAP 512

////////////////////////////////////////////////////////////////////////////////
// Packet construction helpers

struct test_pkt {
	struct packet packet;
	struct rte_mbuf *mbuf;
};

static void
free_test_pkt(struct test_pkt *tp) {
	free(tp->mbuf);
	tp->mbuf = NULL;
}

static int
alloc_mbuf(struct test_pkt *tp, uint16_t headroom, uint16_t pkt_len, uint16_t tailroom) {
	size_t buf_len = (size_t)headroom + pkt_len;
	size_t mbuf_size = sizeof(struct rte_mbuf) + buf_len + tailroom;
	size_t align = alignof(struct rte_mbuf);
	if (mbuf_size % align != 0) {
		mbuf_size += align - mbuf_size % align;
	}
	struct rte_mbuf *mbuf = aligned_alloc(align, mbuf_size);
	if (!mbuf) {
		return -1;
	}
	memset(mbuf, 0, sizeof(*mbuf));
	mbuf->buf_addr = (char *)mbuf + sizeof(struct rte_mbuf);
	mbuf->buf_len = buf_len;
	mbuf->data_off = headroom;
	mbuf->data_len = pkt_len;
	mbuf->pkt_len = pkt_len;
	mbuf->nb_segs = 1;
	rte_mbuf_refcnt_set(mbuf, 1);

	memset(rte_pktmbuf_mtod(mbuf, void *), 0, pkt_len);
	tp->mbuf = mbuf;
	memset(&tp->packet, 0, sizeof(tp->packet));
	tp->packet.mbuf = mbuf;
	return 0;
}

/* Build IPv6 + TCP packet. `data_off_override` = 0 means compute from opt_len,
 * otherwise write the given value verbatim (allowing malformed headers).
 * If `compute_cksum` is non-zero the TCP checksum is written; otherwise left
 * at whatever memset produced. */
static int
build_ip6_tcp(
	struct test_pkt *tp,
	uint8_t tcp_flags,
	const uint8_t *opts,
	uint16_t opt_len,
	uint8_t data_off_override,
	uint16_t headroom,
	int compute_cksum
) {
	uint16_t l4_len = sizeof(struct rte_tcp_hdr) + opt_len;
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv6_hdr) + l4_len;

	if (alloc_mbuf(tp, headroom, pkt_len, DEFAULT_TAILROOM) != 0) {
		return -1;
	}

	uint8_t *data = rte_pktmbuf_mtod(tp->mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	struct rte_ipv6_hdr *ip6 = (struct rte_ipv6_hdr *)(eth + 1);
	ip6->vtc_flow = rte_cpu_to_be_32(0x60000000);
	ip6->payload_len = rte_cpu_to_be_16(l4_len);
	ip6->proto = IPPROTO_TCP;
	ip6->hop_limits = 64;
	ip6->src_addr[0] = 0x20;
	ip6->src_addr[1] = 0x01;
	ip6->dst_addr[0] = 0x20;
	ip6->dst_addr[1] = 0x01;
	ip6->dst_addr[15] = 1;

	struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)(ip6 + 1);
	tcp->src_port = rte_cpu_to_be_16(12345);
	tcp->dst_port = rte_cpu_to_be_16(80);
	tcp->sent_seq = rte_cpu_to_be_32(1);
	tcp->recv_ack = 0;
	uint8_t data_off = data_off_override
				   ? data_off_override
				   : (uint8_t)((sizeof(*tcp) + opt_len) / 4);
	tcp->data_off = (uint8_t)(data_off << 4);
	tcp->tcp_flags = tcp_flags;
	tcp->rx_win = rte_cpu_to_be_16(65535);
	tcp->cksum = 0;
	tcp->tcp_urp = 0;

	if (opt_len > 0 && opts != NULL) {
		memcpy((uint8_t *)(tcp + 1), opts, opt_len);
	}

	if (compute_cksum) {
		tcp->cksum = rte_ipv6_udptcp_cksum(ip6, tcp);
	}

	if (parse_packet(&tp->packet) != 0) {
		free_test_pkt(tp);
		return -1;
	}
	return 0;
}

static int
build_ip6_udp(struct test_pkt *tp) {
	uint16_t l4_len = sizeof(struct rte_udp_hdr);
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv6_hdr) + l4_len;

	if (alloc_mbuf(tp, DEFAULT_HEADROOM, pkt_len, DEFAULT_TAILROOM) != 0) {
		return -1;
	}

	uint8_t *data = rte_pktmbuf_mtod(tp->mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	struct rte_ipv6_hdr *ip6 = (struct rte_ipv6_hdr *)(eth + 1);
	ip6->vtc_flow = rte_cpu_to_be_32(0x60000000);
	ip6->payload_len = rte_cpu_to_be_16(l4_len);
	ip6->proto = IPPROTO_UDP;
	ip6->hop_limits = 64;
	ip6->src_addr[0] = 0x20;
	ip6->dst_addr[0] = 0x20;
	ip6->dst_addr[15] = 1;

	struct rte_udp_hdr *udp = (struct rte_udp_hdr *)(ip6 + 1);
	udp->src_port = rte_cpu_to_be_16(12345);
	udp->dst_port = rte_cpu_to_be_16(80);
	udp->dgram_len = rte_cpu_to_be_16(sizeof(*udp));
	udp->dgram_cksum = 0;

	return parse_packet(&tp->packet);
}

static int
build_ip4_tcp_syn(struct test_pkt *tp) {
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv4_hdr) +
			   sizeof(struct rte_tcp_hdr);

	if (alloc_mbuf(tp, DEFAULT_HEADROOM, pkt_len, DEFAULT_TAILROOM) != 0) {
		return -1;
	}

	uint8_t *data = rte_pktmbuf_mtod(tp->mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ip4 = (struct rte_ipv4_hdr *)(eth + 1);
	ip4->version_ihl = 0x45;
	ip4->total_length = rte_cpu_to_be_16(
		sizeof(struct rte_ipv4_hdr) + sizeof(struct rte_tcp_hdr)
	);
	ip4->time_to_live = 64;
	ip4->next_proto_id = IPPROTO_TCP;
	ip4->src_addr = rte_cpu_to_be_32(0x01010101);
	ip4->dst_addr = rte_cpu_to_be_32(0x02020202);

	struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)(ip4 + 1);
	tcp->src_port = rte_cpu_to_be_16(12345);
	tcp->dst_port = rte_cpu_to_be_16(80);
	tcp->data_off = 5 << 4;
	tcp->tcp_flags = RTE_TCP_SYN_FLAG;
	tcp->rx_win = rte_cpu_to_be_16(65535);
	tcp->cksum = 0;

	return parse_packet(&tp->packet);
}

////////////////////////////////////////////////////////////////////////////////
// Accessors and verifiers

static struct rte_ipv6_hdr *
pkt_ip6(struct packet *p) {
	return rte_pktmbuf_mtod_offset(
		p->mbuf, struct rte_ipv6_hdr *, p->network_header.offset
	);
}

static struct rte_tcp_hdr *
pkt_tcp(struct packet *p) {
	return rte_pktmbuf_mtod_offset(
		p->mbuf, struct rte_tcp_hdr *, p->transport_header.offset
	);
}

/* Recompute the IPv6 TCP checksum from scratch and compare to the on-packet
 * value. Returns TEST_SUCCESS if they match. */
static int
verify_ip6_tcp_cksum(struct packet *p) {
	struct rte_ipv6_hdr *ip6 = pkt_ip6(p);
	struct rte_tcp_hdr *tcp = pkt_tcp(p);
	uint16_t saved = tcp->cksum;
	tcp->cksum = 0;
	uint16_t expected = rte_ipv6_udptcp_cksum(ip6, tcp);
	tcp->cksum = saved;
	TEST_ASSERT_EQUAL(
		saved,
		expected,
		"TCP checksum mismatch: on-pkt=0x%04x expected=0x%04x",
		saved,
		expected
	);
	return TEST_SUCCESS;
}

struct pkt_snapshot {
	uint16_t pkt_len;
	uint16_t data_off;
	uint8_t data[SNAPSHOT_CAP];
};

static void
snapshot(struct packet *p, struct pkt_snapshot *s) {
	uint16_t len = rte_pktmbuf_pkt_len(p->mbuf);
	s->pkt_len = len;
	s->data_off = p->mbuf->data_off;
	memcpy(s->data, rte_pktmbuf_mtod(p->mbuf, void *), len);
}

static int
assert_unchanged(struct packet *p, const struct pkt_snapshot *s) {
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_pkt_len(p->mbuf), s->pkt_len, "pkt_len changed"
	);
	TEST_ASSERT_EQUAL(p->mbuf->data_off, s->data_off, "data_off changed");
	TEST_ASSERT(
		memcmp(rte_pktmbuf_mtod(p->mbuf, void *), s->data, s->pkt_len
		) == 0,
		"packet bytes changed"
	);
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// MSS option builders

static void
write_mss_opt(uint8_t *dst, uint16_t mss) {
	dst[0] = TCP_OPT_KIND_MSS;
	dst[1] = 4;
	uint16_t be = rte_cpu_to_be_16(mss);
	memcpy(dst + 2, &be, 2);
}

////////////////////////////////////////////////////////////////////////////////
// Test cases

static int
test_clamp_gt(void) {
	uint8_t opts[4];
	write_mss_opt(opts, 1460);
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp, RTE_TCP_SYN_FLAG, opts, 4, 0, DEFAULT_HEADROOM, 1
		),
		"build"
	);

	uint16_t ip6_len_before =
		rte_be_to_cpu_16(pkt_ip6(&tp.packet)->payload_len);
	uint8_t data_off_before = pkt_tcp(&tp.packet)->data_off;

	enum packet_fix_mss_result rc = packet_fix_mss(&tp.packet, 1200, 1300);
	TEST_ASSERT_EQUAL(rc, packet_fix_mss_ok, "rc");

	struct rte_tcp_hdr *tcp = pkt_tcp(&tp.packet);
	uint16_t *mss_field =
		(uint16_t *)((uint8_t *)(tcp + 1) + 2); /* after kind+len */
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(*mss_field), 1200, "MSS not clamped"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(pkt_ip6(&tp.packet)->payload_len),
		ip6_len_before,
		"payload_len changed"
	);
	TEST_ASSERT_EQUAL(tcp->data_off, data_off_before, "data_off changed");
	TEST_ASSERT_SUCCESS(verify_ip6_tcp_cksum(&tp.packet), "cksum");

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_clamp_lt(void) {
	uint8_t opts[4];
	write_mss_opt(opts, 1000);
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp, RTE_TCP_SYN_FLAG, opts, 4, 0, DEFAULT_HEADROOM, 1
		),
		"build"
	);
	struct pkt_snapshot s;
	snapshot(&tp.packet, &s);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300), packet_fix_mss_ok, "rc"
	);
	TEST_ASSERT_SUCCESS(assert_unchanged(&tp.packet, &s), "unchanged");

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_clamp_eq(void) {
	uint8_t opts[4];
	write_mss_opt(opts, 1200);
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp, RTE_TCP_SYN_FLAG, opts, 4, 0, DEFAULT_HEADROOM, 1
		),
		"build"
	);
	struct pkt_snapshot s;
	snapshot(&tp.packet, &s);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300), packet_fix_mss_ok, "rc"
	);
	TEST_ASSERT_SUCCESS(assert_unchanged(&tp.packet, &s), "unchanged");

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_insert_no_mss(void) {
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp, RTE_TCP_SYN_FLAG, NULL, 0, 0, DEFAULT_HEADROOM, 1
		),
		"build"
	);
	uint32_t pkt_len_before = rte_pktmbuf_pkt_len(tp.mbuf);
	uint16_t ip6_len_before =
		rte_be_to_cpu_16(pkt_ip6(&tp.packet)->payload_len);
	uint8_t data_off_before = pkt_tcp(&tp.packet)->data_off;

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300), packet_fix_mss_ok, "rc"
	);

	TEST_ASSERT_EQUAL(
		rte_pktmbuf_pkt_len(tp.mbuf),
		pkt_len_before + 4u,
		"pkt_len did not grow by 4"
	);
	struct rte_tcp_hdr *tcp = pkt_tcp(&tp.packet);
	TEST_ASSERT_EQUAL(
		tcp->data_off,
		data_off_before + (1 << 4),
		"data_off not +1 word"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(pkt_ip6(&tp.packet)->payload_len),
		ip6_len_before + 4,
		"IPv6 payload_len not +4"
	);
	uint8_t *opt = (uint8_t *)(tcp + 1);
	TEST_ASSERT_EQUAL(opt[0], TCP_OPT_KIND_MSS, "opt kind");
	TEST_ASSERT_EQUAL(opt[1], 4, "opt len");
	uint16_t mss_be;
	memcpy(&mss_be, opt + 2, 2);
	TEST_ASSERT_EQUAL(rte_be_to_cpu_16(mss_be), 1300, "inserted MSS");
	TEST_ASSERT_SUCCESS(verify_ip6_tcp_cksum(&tp.packet), "cksum");

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_skip_ipv4(void) {
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(build_ip4_tcp_syn(&tp), "build");
	struct pkt_snapshot s;
	snapshot(&tp.packet, &s);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300), packet_fix_mss_ok, "rc"
	);
	TEST_ASSERT_SUCCESS(assert_unchanged(&tp.packet, &s), "unchanged");

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_skip_non_tcp(void) {
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(build_ip6_udp(&tp), "build");
	struct pkt_snapshot s;
	snapshot(&tp.packet, &s);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300), packet_fix_mss_ok, "rc"
	);
	TEST_ASSERT_SUCCESS(assert_unchanged(&tp.packet, &s), "unchanged");

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_skip_non_syn(void) {
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp, RTE_TCP_ACK_FLAG, NULL, 0, 0, DEFAULT_HEADROOM, 1
		),
		"build"
	);
	struct pkt_snapshot s;
	snapshot(&tp.packet, &s);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300), packet_fix_mss_ok, "rc"
	);
	TEST_ASSERT_SUCCESS(assert_unchanged(&tp.packet, &s), "unchanged");

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_skip_syn_rst(void) {
	uint8_t opts[4];
	write_mss_opt(opts, 1460);
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp,
			RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG,
			opts,
			4,
			0,
			DEFAULT_HEADROOM,
			1
		),
		"build"
	);
	struct pkt_snapshot s;
	snapshot(&tp.packet, &s);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300), packet_fix_mss_ok, "rc"
	);
	TEST_ASSERT_SUCCESS(assert_unchanged(&tp.packet, &s), "unchanged");

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_malformed_len_zero(void) {
	/* kind=8 (TS) but len=0 — variable-length option must have len >= 2. */
	uint8_t opts[4] = {8, 0, 0, 0};
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp, RTE_TCP_SYN_FLAG, opts, 4, 0, DEFAULT_HEADROOM, 1
		),
		"build"
	);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300),
		packet_fix_mss_malformed,
		"rc"
	);

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_malformed_overrun(void) {
	/* MSS kind=2 with len=6 would overrun an 8-byte options area that
	 * actually declares 4 bytes; we use an 8-byte options area with an
	 * initial non-MSS variable-length option whose len overruns. */
	uint8_t opts[8] = {30, 10, 0, 0, 0, 0, 0, 0};
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp, RTE_TCP_SYN_FLAG, opts, 8, 0, DEFAULT_HEADROOM, 1
		),
		"build"
	);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300),
		packet_fix_mss_malformed,
		"rc"
	);

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_eol_respected(void) {
	uint8_t opts[4] = {TCP_OPT_KIND_NOP, TCP_OPT_KIND_EOL, 0xFF, 0xFF};
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp, RTE_TCP_SYN_FLAG, opts, 4, 0, DEFAULT_HEADROOM, 1
		),
		"build"
	);
	uint32_t pkt_len_before = rte_pktmbuf_pkt_len(tp.mbuf);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300), packet_fix_mss_ok, "rc"
	);
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_pkt_len(tp.mbuf),
		pkt_len_before + 4u,
		"insert did not grow packet"
	);
	struct rte_tcp_hdr *tcp = pkt_tcp(&tp.packet);
	uint8_t *new_opt = (uint8_t *)(tcp + 1);
	TEST_ASSERT_EQUAL(new_opt[0], TCP_OPT_KIND_MSS, "new opt kind");
	TEST_ASSERT_EQUAL(new_opt[1], 4, "new opt len");
	TEST_ASSERT_SUCCESS(verify_ip6_tcp_cksum(&tp.packet), "cksum");

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_no_headroom(void) {
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(&tp, RTE_TCP_SYN_FLAG, NULL, 0, 0, 0, 1), "build"
	);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300),
		packet_fix_mss_no_headroom,
		"rc"
	);

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_malformed_mss_wrong_len(void) {
	/* MSS with len=6 instead of 4. */
	uint8_t opts[8] = {TCP_OPT_KIND_MSS, 6, 0x05, 0xB4, 0, 0, 0, 0};
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp, RTE_TCP_SYN_FLAG, opts, 8, 0, DEFAULT_HEADROOM, 1
		),
		"build"
	);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300),
		packet_fix_mss_malformed,
		"rc"
	);

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_malformed_data_off_short(void) {
	/* data_off = 4 (16 bytes) < fixed 20-byte TCP header. */
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp,
			RTE_TCP_SYN_FLAG,
			NULL,
			0,
			/*data_off_override=*/4,
			DEFAULT_HEADROOM,
			0
		),
		"build"
	);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300),
		packet_fix_mss_malformed,
		"rc"
	);

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_malformed_hdr_full_60(void) {
	/* data_off = 15 (60 bytes) with 40 bytes of NOP options, no MSS:
	 * find_mss_option returns absent; insert_mss_option refuses because
	 * hdr_len + 4 > 60. */
	uint8_t opts[40];
	memset(opts, TCP_OPT_KIND_NOP, sizeof(opts));
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp,
			RTE_TCP_SYN_FLAG,
			opts,
			sizeof(opts),
			0,
			DEFAULT_HEADROOM,
			1
		),
		"build"
	);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300),
		packet_fix_mss_malformed,
		"rc"
	);

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

static int
test_clamp_syn_ack(void) {
	uint8_t opts[4];
	write_mss_opt(opts, 1460);
	struct test_pkt tp;
	TEST_ASSERT_SUCCESS(
		build_ip6_tcp(
			&tp,
			RTE_TCP_SYN_FLAG | RTE_TCP_ACK_FLAG,
			opts,
			4,
			0,
			DEFAULT_HEADROOM,
			1
		),
		"build"
	);

	TEST_ASSERT_EQUAL(
		packet_fix_mss(&tp.packet, 1200, 1300), packet_fix_mss_ok, "rc"
	);
	struct rte_tcp_hdr *tcp = pkt_tcp(&tp.packet);
	uint16_t *mss_field = (uint16_t *)((uint8_t *)(tcp + 1) + 2);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(*mss_field), 1200, "MSS not clamped on SYN+ACK"
	);
	TEST_ASSERT_SUCCESS(verify_ip6_tcp_cksum(&tp.packet), "cksum");

	free_test_pkt(&tp);
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

int
main(void) {
	log_enable_name("debug");

	LOG(INFO, "running mss tests...");

	TEST_ASSERT_SUCCESS(test_clamp_gt(), "clamp_gt");
	TEST_ASSERT_SUCCESS(test_clamp_lt(), "clamp_lt");
	TEST_ASSERT_SUCCESS(test_clamp_eq(), "clamp_eq");
	TEST_ASSERT_SUCCESS(test_insert_no_mss(), "insert_no_mss");
	TEST_ASSERT_SUCCESS(test_skip_ipv4(), "skip_ipv4");
	TEST_ASSERT_SUCCESS(test_skip_non_tcp(), "skip_non_tcp");
	TEST_ASSERT_SUCCESS(test_skip_non_syn(), "skip_non_syn");
	TEST_ASSERT_SUCCESS(test_skip_syn_rst(), "skip_syn_rst");
	TEST_ASSERT_SUCCESS(test_malformed_len_zero(), "malformed_len_zero");
	TEST_ASSERT_SUCCESS(test_malformed_overrun(), "malformed_overrun");
	TEST_ASSERT_SUCCESS(test_eol_respected(), "eol_respected");
	TEST_ASSERT_SUCCESS(test_no_headroom(), "no_headroom");
	TEST_ASSERT_SUCCESS(
		test_malformed_mss_wrong_len(), "malformed_mss_wrong_len"
	);
	TEST_ASSERT_SUCCESS(
		test_malformed_data_off_short(), "malformed_data_off_short"
	);
	TEST_ASSERT_SUCCESS(
		test_malformed_hdr_full_60(), "malformed_hdr_full_60"
	);
	TEST_ASSERT_SUCCESS(test_clamp_syn_ack(), "clamp_syn_ack");

	LOG(INFO, "all mss tests passed");

	return 0;
}
