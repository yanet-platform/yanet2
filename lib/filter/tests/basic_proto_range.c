#include "lib/filter/compiler.h"
#include "lib/filter/filter.h"
#include "lib/filter/query.h"

#include "lib/filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "lib/logging/log.h"
#include <assert.h>
#include <netinet/in.h>

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

	const struct filter_rule *rule_ptrs[4] = {&r1, &r2, &r3, &r4};

	struct filter filter;

	LOG(INFO, "filter init...");
	res = filter_init(
		&filter,
		sign_proto_range_compile,
		rule_ptrs,
		4,
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

	LOG(INFO, "query unavailable udp...");
	query_unavailable_packet(&filter, IPPROTO_UDP, 2);

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

	LOG(INFO, "query real protocol zero...");
	query_proto_zero_packet(&filter, 0);

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
