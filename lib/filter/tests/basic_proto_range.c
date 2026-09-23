#include "lib/filter/compiler.h"
#include "lib/filter/filter.h"
#include "lib/filter/query.h"

#include "lib/filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "lib/logging/log.h"
#include <assert.h>
#include <netinet/in.h>
#include <rte_ether.h>
#include <rte_ip.h>

FILTER_COMPILER_DECLARE(sign_proto_range_compile, proto_range);
FILTER_QUERY_DECLARE(sign_proto_range, proto_range);

static void
query_tcp_packet(struct filter *filter, uint16_t flags, uint32_t expected) {
	struct packet packet = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(&packet, sip, dip, 0, 0, IPPROTO_TCP, flags);
	assert(res == 0);
	struct packet *packet_ptr = &packet;
	uint32_t actions;
	filter_query(filter, sign_proto_range, &packet_ptr, &actions, 1);
	assert(actions == expected);
	free_packet(&packet);
}

static void
query_udp_packet(struct filter *filter, uint32_t expected) {
	struct packet packet = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(&packet, sip, dip, 0, 0, IPPROTO_UDP, 0);
	assert(res == 0);
	struct packet *packet_ptr = &packet;
	uint32_t actions;
	filter_query(filter, sign_proto_range, &packet_ptr, &actions, 1);
	assert(actions == expected);
	free_packet(&packet);
}

// Queries a packet whose transport header is unavailable: the packet parses
// normally and the declared protocol is then tagged, as the parser does for
// a non-initial fragment whose payload must not be read.
static void
query_unavailable_packet(
	struct filter *filter, uint8_t proto, uint32_t expected
) {
	struct packet packet = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(&packet, sip, dip, 0, 0, proto, 0);
	assert(res == 0);
	packet.transport_header.type |= PACKET_TRANSPORT_HEADER_UNAVAILABLE;
	struct packet *packet_ptr = &packet;
	uint32_t actions;
	filter_query(filter, sign_proto_range, &packet_ptr, &actions, 1);
	assert(actions == expected);
	free_packet(&packet);
}

// Verifies that a TCP frame whose transport region stops one byte before
// the flags byte never reaches classification: parse refuses any TCP
// header shorter than the fixed header, so the classifier's flags read
// stays inside the frame for every packet that got parsed.
static void
query_truncated_tcp_flags_packet(void) {
	uint16_t transport_bytes = 13;
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv4_hdr) + transport_bytes;
	struct packet packet = {0};
	packet.mbuf = alloc_mbuf(128, pkt_len, 1);
	assert(packet.mbuf != NULL);

	uint8_t *data = rte_pktmbuf_mtod(packet.mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ip4 = (struct rte_ipv4_hdr *)(eth + 1);
	ip4->version_ihl = 0x45;
	ip4->total_length = rte_cpu_to_be_16(sizeof(*ip4) + transport_bytes);
	ip4->time_to_live = 64;
	ip4->next_proto_id = IPPROTO_TCP;
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	memcpy(&ip4->src_addr, sip, NET4_LEN);
	memcpy(&ip4->dst_addr, dip, NET4_LEN);

	// The flags byte sits exactly at the frame end: make the byte the
	// read would see deterministic.
	uint8_t *flags = data + pkt_len;
	*flags = 0;

	int res = parse_packet(&packet);
	assert(res == -1);
	free_packet(&packet);
}

// Queries a real protocol-0 packet: its transport type is the plain protocol
// number, unlike a tagged fragment.
static void
query_proto_zero_packet(struct filter *filter, uint32_t expected) {
	struct packet packet = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(&packet, sip, dip, 0, 0, IPPROTO_HOPOPTS, 0);
	assert(res == 0);
	struct packet *packet_ptr = &packet;
	uint32_t actions;
	filter_query(filter, sign_proto_range, &packet_ptr, &actions, 1);
	assert(actions == expected);
	free_packet(&packet);
}

// Builds an IPv4 packet whose transport position holds an ICMP or ICMPv6
// header with the given type byte. Both protocols read their subtype from
// the first transport byte, so one frame shape serves both.
static int
build_icmp_packet(struct packet *packet, uint8_t proto, uint8_t type) {
	uint16_t pkt_len =
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr) + 4;
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = alloc_mbuf(128, pkt_len, 0);
	assert(packet->mbuf != NULL);

	struct rte_ether_hdr *eth =
		rte_pktmbuf_mtod(packet->mbuf, struct rte_ether_hdr *);
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ip4 = (struct rte_ipv4_hdr *)(eth + 1);
	ip4->version_ihl = 0x45;
	ip4->total_length = rte_cpu_to_be_16(sizeof(*ip4) + 4);
	ip4->time_to_live = 64;
	ip4->next_proto_id = proto;
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	memcpy(&ip4->src_addr, sip, NET4_LEN);
	memcpy(&ip4->dst_addr, dip, NET4_LEN);

	uint8_t *icmp = (uint8_t *)(ip4 + 1);
	icmp[0] = type;

	return parse_packet(packet);
}

// Queries a packet carrying a real ICMP or ICMPv6 header with the given
// type byte, the ordinary untagged classification path.
static void
query_icmp_packet(
	struct filter *filter, uint8_t proto, uint8_t type, uint32_t expected
) {
	struct packet packet;
	int res = build_icmp_packet(&packet, proto, type);
	assert(res == 0);
	struct packet *packet_ptr = &packet;
	uint32_t actions;
	filter_query(filter, sign_proto_range, &packet_ptr, &actions, 1);
	assert(actions == expected);
	free_packet(&packet);
}

// Queries an untagged packet whose declared protocol is ICMP or ICMPv6
// but whose transport region holds no bytes at all: the type byte must
// not be read from beyond the frame.
static void
query_short_icmp_packet(
	struct filter *filter, uint8_t proto, uint32_t expected
) {
	struct packet packet = {0};
	uint16_t pkt_len =
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr);
	packet.mbuf = alloc_mbuf(128, pkt_len, 0);
	assert(packet.mbuf != NULL);

	struct rte_ether_hdr *eth =
		rte_pktmbuf_mtod(packet.mbuf, struct rte_ether_hdr *);
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ip4 = (struct rte_ipv4_hdr *)(eth + 1);
	ip4->version_ihl = 0x45;
	ip4->total_length = rte_cpu_to_be_16(sizeof(*ip4));
	ip4->time_to_live = 64;
	ip4->next_proto_id = proto;
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	memcpy(&ip4->src_addr, sip, NET4_LEN);
	memcpy(&ip4->dst_addr, dip, NET4_LEN);

	int res = parse_packet(&packet);
	assert(res == 0);
	struct packet *packet_ptr = &packet;
	uint32_t actions;
	filter_query(filter, sign_proto_range, &packet_ptr, &actions, 1);
	assert(actions == expected);
	free_packet(&packet);
}

// Queries a packet whose declared protocol is ICMP or ICMPv6 but whose
// transport header is fragment payload: the type byte is tagged unavailable
// and must never be read as a subtype. The frame carries the exact-subtype
// value (echo request), so any unguarded read flips the result to the
// exact rule instead of the whole-block one.
static void
query_unavailable_icmp_packet(
	struct filter *filter, uint8_t proto, uint32_t expected
) {
	uint8_t echo_request = proto == IPPROTO_ICMP ? 8 : 128;
	struct packet packet;
	int res = build_icmp_packet(&packet, proto, echo_request);
	assert(res == 0);
	packet.transport_header.type |= PACKET_TRANSPORT_HEADER_UNAVAILABLE;
	struct packet *packet_ptr = &packet;
	uint32_t actions;
	filter_query(filter, sign_proto_range, &packet_ptr, &actions, 1);
	assert(actions == expected);
	free_packet(&packet);
}

static void
test_proto_1(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_proto_range(
		&b1, 256 * IPPROTO_TCP, 256 * IPPROTO_TCP + 255
	);
	struct filter_rule r1 = build_rule(&b1);

	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_proto_range(
		&b2, 256 * IPPROTO_UDP, 256 * IPPROTO_UDP + 255
	);
	struct filter_rule r2 = build_rule(&b2);

	const struct filter_rule *rule_ptrs[2] = {&r1, &r2};

	struct filter filter;

	LOG(INFO, "filter init...");
	res = filter_init(
		&filter,
		sign_proto_range_compile,
		rule_ptrs,
		2,
		&memory_context,
		"filter",
		NULL
	);
	assert(res == 0);

	LOG(INFO, "query tcp packet...");
	query_tcp_packet(&filter, 0, 0);

	LOG(INFO, "query udp packet...");
	query_udp_packet(&filter, 1);

	filter_free(&filter, sign_proto_range_compile);
	memory_context_fini(&memory_context);
}

// An exact-subtype TCP rule must not catch a fragment whose transport header
// is unavailable, while a whole-block rule and a real flags-zero packet keep
// their match; protocol 0 never catches fragments of other protocols.
static void
test_proto_unavailable_subtypes(void *memory) {
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_proto_range(&b1, 256 * IPPROTO_TCP, 256 * IPPROTO_TCP);
	struct filter_rule r1 = build_rule(&b1);

	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_proto_range(
		&b2, 256 * IPPROTO_TCP, 256 * IPPROTO_TCP + 255
	);
	struct filter_rule r2 = build_rule(&b2);

	struct filter_rule_builder b3;
	builder_init(&b3);
	builder_add_proto_range(
		&b3, 256 * IPPROTO_UDP, 256 * IPPROTO_UDP + 255
	);
	struct filter_rule r3 = build_rule(&b3);

	struct filter_rule_builder b4;
	builder_init(&b4);
	builder_add_proto_range(&b4, 0, 255);
	struct filter_rule r4 = build_rule(&b4);

	struct filter_rule_builder b5;
	builder_init(&b5);
	builder_add_proto_range(
		&b5, 256 * IPPROTO_ICMP + 8, 256 * IPPROTO_ICMP + 8
	);
	struct filter_rule r5 = build_rule(&b5);

	struct filter_rule_builder b6;
	builder_init(&b6);
	builder_add_proto_range(
		&b6, 256 * IPPROTO_ICMP, 256 * IPPROTO_ICMP + 255
	);
	struct filter_rule r6 = build_rule(&b6);

	struct filter_rule_builder b7;
	builder_init(&b7);
	builder_add_proto_range(
		&b7, 256 * IPPROTO_ICMPV6 + 128, 256 * IPPROTO_ICMPV6 + 128
	);
	struct filter_rule r7 = build_rule(&b7);

	struct filter_rule_builder b8;
	builder_init(&b8);
	builder_add_proto_range(
		&b8, 256 * IPPROTO_ICMPV6, 256 * IPPROTO_ICMPV6 + 255
	);
	struct filter_rule r8 = build_rule(&b8);

	const struct filter_rule *rule_ptrs[8] = {
		&r1, &r2, &r3, &r4, &r5, &r6, &r7, &r8
	};

	struct filter filter;

	LOG(INFO, "filter init...");
	res = filter_init(
		&filter,
		sign_proto_range_compile,
		rule_ptrs,
		8,
		&memory_context,
		"filter",
		NULL
	);
	assert(res == 0);

	// A real TCP packet with flags zero matches the exact rule first.
	LOG(INFO, "query real tcp flags zero...");
	query_tcp_packet(&filter, 0, 0);

	// A real TCP packet with SYN does not match the exact rule.
	LOG(INFO, "query real tcp syn...");
	query_tcp_packet(&filter, RTE_TCP_SYN_FLAG, 1);

	// A TCP fragment without a transport header skips the exact rule and
	// matches the whole-block rule, never the exact flags-zero slot.
	LOG(INFO, "query unavailable tcp...");
	query_unavailable_packet(&filter, IPPROTO_TCP, 1);

	// A real TCP header that stops before the flags never reaches this
	// classifier: parse already refused the short header.
	LOG(INFO, "query truncated tcp flags (parse refusal)...");
	query_truncated_tcp_flags_packet();

	LOG(INFO, "query unavailable udp...");
	query_unavailable_packet(&filter, IPPROTO_UDP, 2);

	// An ICMP fragment must fall to the whole-ICMP rule: its unavailable
	// type byte must not be read as a real subtype, an exact-subtype rule
	// must not catch it, and protocol 0 must not swallow it.
	LOG(INFO, "query real icmp echo request...");
	query_icmp_packet(&filter, IPPROTO_ICMP, 8, 4);

	LOG(INFO, "query real icmp echo reply...");
	query_icmp_packet(&filter, IPPROTO_ICMP, 0, 5);

	LOG(INFO, "query unavailable icmp...");
	query_unavailable_icmp_packet(&filter, IPPROTO_ICMP, 5);

	LOG(INFO, "query real icmpv6 echo request...");
	query_icmp_packet(&filter, IPPROTO_ICMPV6, 128, 6);

	LOG(INFO, "query unavailable icmpv6...");
	query_unavailable_icmp_packet(&filter, IPPROTO_ICMPV6, 7);

	// A packet with no transport bytes at all must not have its type
	// read from beyond the frame: both fall to the whole-block rules.
	LOG(INFO, "query short icmp...");
	query_short_icmp_packet(&filter, IPPROTO_ICMP, 5);

	LOG(INFO, "query short icmpv6...");
	query_short_icmp_packet(&filter, IPPROTO_ICMPV6, 7);

	LOG(INFO, "query real protocol zero...");
	query_proto_zero_packet(&filter, 3);

	filter_free(&filter, sign_proto_range_compile);
	memory_context_fini(&memory_context);
}

// A protocol-0 rule alone must not catch fragments of other protocols: their
// declared protocol never collapses into the protocol-0 class, while a real
// protocol-0 packet still matches.
static void
test_proto_zero_noncollision(void *memory) {
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_proto_range(&b1, 0, 255);
	struct filter_rule r1 = build_rule(&b1);

	const struct filter_rule *rule_ptrs[1] = {&r1};

	struct filter filter;

	LOG(INFO, "filter init...");
	res = filter_init(
		&filter,
		sign_proto_range_compile,
		rule_ptrs,
		1,
		&memory_context,
		"filter",
		NULL
	);
	assert(res == 0);

	LOG(INFO, "query unavailable tcp...");
	query_unavailable_packet(&filter, IPPROTO_TCP, FILTER_RULE_INVALID);

	LOG(INFO, "query unavailable udp...");
	query_unavailable_packet(&filter, IPPROTO_UDP, FILTER_RULE_INVALID);

	// The tagged ICMP and ICMPv6 classes must also stay outside the
	// protocol-0 block: the whole protocol numbers 1 and 58 are not 0.
	LOG(INFO, "query unavailable icmp...");
	query_unavailable_icmp_packet(
		&filter, IPPROTO_ICMP, FILTER_RULE_INVALID
	);

	LOG(INFO, "query unavailable icmpv6...");
	query_unavailable_icmp_packet(
		&filter, IPPROTO_ICMPV6, FILTER_RULE_INVALID
	);

	LOG(INFO, "query real protocol zero...");
	query_proto_zero_packet(&filter, 0);

	// A fragment whose declared protocol is 0 stays on the protocol-0
	// class: the tag must not push it into another protocol's subtype
	// space, so the protocol-0 rule keeps matching it.
	LOG(INFO, "query unavailable protocol zero...");
	query_unavailable_packet(&filter, IPPROTO_HOPOPTS, 0);

	filter_free(&filter, sign_proto_range_compile);
	memory_context_fini(&memory_context);
}

// A rule whose protocol ranges cover a whole block only in union — split,
// unsorted and overlapping — must match an unavailable-header packet, while
// a rule with a one-value hole in the same block must not.
static void
test_proto_union_coverage(void *memory) {
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_proto_range(
		&b1, 256 * IPPROTO_TCP, 256 * IPPROTO_TCP + 126
	);
	builder_add_proto_range(
		&b1, 256 * IPPROTO_TCP + 128, 256 * IPPROTO_TCP + 255
	);
	struct filter_rule r1 = build_rule(&b1);

	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_proto_range(
		&b2, 256 * IPPROTO_TCP + 128, 256 * IPPROTO_TCP + 255
	);
	builder_add_proto_range(
		&b2, 256 * IPPROTO_TCP, 256 * IPPROTO_TCP + 127
	);
	struct filter_rule r2 = build_rule(&b2);

	const struct filter_rule *rule_ptrs[2] = {&r1, &r2};

	struct filter filter;

	LOG(INFO, "filter init...");
	res = filter_init(
		&filter,
		sign_proto_range_compile,
		rule_ptrs,
		2,
		&memory_context,
		"filter",
		NULL
	);
	assert(res == 0);

	// A real packet carrying the hole value falls to the unsorted
	// full-coverage rule: the split rule deliberately does not cover it.
	LOG(INFO, "query real tcp hole value...");
	query_tcp_packet(&filter, 127, 1);

	// A real packet outside the hole still matches the split rule first.
	LOG(INFO, "query real tcp covered value...");
	query_tcp_packet(&filter, 0, 0);

	// The split rule leaves a hole, so an unavailable-header packet only
	// matches the unsorted full-coverage rule.
	LOG(INFO, "query unavailable tcp...");
	query_unavailable_packet(&filter, IPPROTO_TCP, 1);

	filter_free(&filter, sign_proto_range_compile);
	memory_context_fini(&memory_context);
}

int
main() {
	log_enable_name("debug");

	void *memory = malloc(1 << 24);

	test_proto_1(memory);
	test_proto_unavailable_subtypes(memory);
	test_proto_union_coverage(memory);
	test_proto_zero_noncollision(memory);

	free(memory);

	LOG(INFO, "passed");

	return 0;
}
