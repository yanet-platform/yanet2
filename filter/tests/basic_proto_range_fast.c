#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "common/test_assert.h"
#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdlib.h>

FILTER_COMPILER_DECLARE(sign_proto_range_fast_compile, proto_range_fast);
FILTER_QUERY_DECLARE(sign_proto_range_fast, proto_range_fast);

////////////////////////////////////////////////////////////////////////////////

static void
query_tcp_packet(struct filter *filter, uint16_t flags, uint32_t expected) {
	struct packet packet = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(&packet, sip, dip, 0, 0, IPPROTO_TCP, flags);
	assert(res == 0);
	struct packet *packet_ptr = &packet;
	struct value_range *actions;
	filter_query(filter, sign_proto_range_fast, &packet_ptr, &actions, 1);
	assert(actions->count >= 1);
	assert(ADDR_OF(&actions->values)[0] == expected);
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
	struct value_range *actions;
	filter_query(filter, sign_proto_range_fast, &packet_ptr, &actions, 1);
	assert(actions->count >= 1);
	assert(ADDR_OF(&actions->values)[0] == expected);
	free_packet(&packet);
}

////////////////////////////////////////////////////////////////////////////////

static int
test_basic_tcp_udp(void *memory) {
	LOG(INFO, "=== Test Basic TCP/UDP ===");

	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_proto_range(
		&b1, 256 * IPPROTO_TCP, 256 * IPPROTO_TCP + 255
	);
	struct filter_rule r1 = build_rule(&b1, 1);

	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_proto_range(
		&b2, 256 * IPPROTO_UDP, 256 * IPPROTO_UDP + 255
	);
	struct filter_rule r2 = build_rule(&b2, 2);

	struct filter_rule rules[2] = {r1, r2};

	struct filter filter;

	LOG(INFO, "filter init...");
	res = filter_init(
		&filter,
		sign_proto_range_fast_compile,
		rules,
		2,
		&memory_context
	);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	LOG(INFO, "query tcp packet...");
	query_tcp_packet(&filter, 0, 1);

	LOG(INFO, "query udp packet...");
	query_udp_packet(&filter, 2);

	filter_free(&filter, sign_proto_range_fast_compile);

	return TEST_SUCCESS;
}

static int
test_tcp_flags(void *memory) {
	LOG(INFO, "=== Test TCP Flags ===");

	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	// Rule 1: TCP SYN (flag 0x02)
	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_proto_range(
		&b1, 256 * IPPROTO_TCP + 0x02, 256 * IPPROTO_TCP + 0x02
	);
	struct filter_rule r1 = build_rule(&b1, 1);

	// Rule 2: TCP ACK (flag 0x10)
	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_proto_range(
		&b2, 256 * IPPROTO_TCP + 0x10, 256 * IPPROTO_TCP + 0x10
	);
	struct filter_rule r2 = build_rule(&b2, 2);

	// Rule 3: TCP FIN (flag 0x01)
	struct filter_rule_builder b3;
	builder_init(&b3);
	builder_add_proto_range(
		&b3, 256 * IPPROTO_TCP + 0x01, 256 * IPPROTO_TCP + 0x01
	);
	struct filter_rule r3 = build_rule(&b3, 3);

	struct filter_rule rules[3] = {r1, r2, r3};

	struct filter filter;
	res = filter_init(
		&filter,
		sign_proto_range_fast_compile,
		rules,
		3,
		&memory_context
	);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	LOG(INFO, "query tcp SYN packet...");
	query_tcp_packet(&filter, 0x02, 1);

	LOG(INFO, "query tcp ACK packet...");
	query_tcp_packet(&filter, 0x10, 2);

	LOG(INFO, "query tcp FIN packet...");
	query_tcp_packet(&filter, 0x01, 3);

	filter_free(&filter, sign_proto_range_fast_compile);

	return TEST_SUCCESS;
}

static int
test_multiple_ranges_per_rule(void *memory) {
	LOG(INFO, "=== Test Multiple Ranges Per Rule ===");

	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	// Rule with multiple proto ranges: TCP and UDP
	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_proto_range(
		&b1, 256 * IPPROTO_TCP, 256 * IPPROTO_TCP + 255
	);
	builder_add_proto_range(
		&b1, 256 * IPPROTO_UDP, 256 * IPPROTO_UDP + 255
	);
	struct filter_rule r1 = build_rule(&b1, 1);

	struct filter_rule rules[1] = {r1};

	struct filter filter;
	res = filter_init(
		&filter,
		sign_proto_range_fast_compile,
		rules,
		1,
		&memory_context
	);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	LOG(INFO, "query tcp packet...");
	query_tcp_packet(&filter, 0, 1);

	LOG(INFO, "query udp packet...");
	query_udp_packet(&filter, 1);

	filter_free(&filter, sign_proto_range_fast_compile);

	return TEST_SUCCESS;
}

static int
test_boundary_values(void *memory) {
	LOG(INFO, "=== Test Boundary Values ===");

	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	// Rule 1: Proto range 0-100
	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_proto_range(&b1, 0, 100);
	struct filter_rule r1 = build_rule(&b1, 1);

	// Rule 2: Proto range 65435-65535 (near max uint16_t)
	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_proto_range(&b2, 65435, 65535);
	struct filter_rule r2 = build_rule(&b2, 2);

	struct filter_rule rules[2] = {r1, r2};

	struct filter filter;
	res = filter_init(
		&filter,
		sign_proto_range_fast_compile,
		rules,
		2,
		&memory_context
	);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	// Test proto 0 (boundary)
	struct packet packet1 = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	res = fill_packet_net4(&packet1, sip, dip, 0, 0, 0, 0);
	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet");

	struct packet *packet_ptr1 = &packet1;
	struct value_range *actions1;
	filter_query(
		&filter, sign_proto_range_fast, &packet_ptr1, &actions1, 1
	);
	TEST_ASSERT_EQUAL(actions1->count, 1, "proto 0 should match rule 1");
	TEST_ASSERT_EQUAL(
		ADDR_OF(&actions1->values)[0], 1, "proto 0 should match rule 1"
	);
	free_packet(&packet1);

	filter_free(&filter, sign_proto_range_fast_compile);

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");

	size_t tests = 0;
	size_t failed = 0;

	void *memory = malloc(1 << 24);

	++tests;
	if (test_basic_tcp_udp(memory) != 0) {
		LOG(ERROR, "test_basic_tcp_udp failed");
		++failed;
	}

	++tests;
	if (test_tcp_flags(memory) != 0) {
		LOG(ERROR, "test_tcp_flags failed");
		++failed;
	}

	++tests;
	if (test_multiple_ranges_per_rule(memory) != 0) {
		LOG(ERROR, "test_multiple_ranges_per_rule failed");
		++failed;
	}

	++tests;
	if (test_boundary_values(memory) != 0) {
		LOG(ERROR, "test_boundary_values failed");
		++failed;
	}

	free(memory);

	if (failed == 0) {
		LOG(INFO, "All %zu tests passed", tests);
	} else {
		LOG(ERROR, "%zu/%zu tests failed", failed, tests);
	}

	return (failed == 0 ? 0 : 1);
}
