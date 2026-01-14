#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdio.h>
#include <string.h>

FILTER_COMPILER_DECLARE(sign_ports, port_src, port_dst);
FILTER_QUERY_DECLARE(sign_ports, port_src, port_dst);

FILTER_COMPILER_DECLARE(sign_port_src, port_src);
FILTER_QUERY_DECLARE(sign_port_src, port_src);

////////////////////////////////////////////////////////////////////////////////

static void
query_and_expect_action(
	struct filter *filter,
	uint16_t src_port,
	uint16_t dst_port,
	uint32_t expected,
	const char *sign
) {
	struct packet packet = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(
		&packet, sip, dip, src_port, dst_port, IPPROTO_UDP, 0
	);
	assert(res == 0);

	struct packet *packet_ptr = &packet;
	struct value_range *actions;

	if (strcmp(sign, "ports") == 0) {
		FILTER_QUERY(filter, sign_ports, &packet_ptr, &actions, 1);
	} else if (strcmp(sign, "port_src") == 0) {
		FILTER_QUERY(filter, sign_port_src, &packet_ptr, &actions, 1);
	} else {
		assert(0 && "Invalid sign");
	}

	assert(actions->count >= 1);
	assert(ADDR_OF(&actions->values)[0] == expected);
	free_packet(&packet);
}

static void
query_and_expect_no_action(
	struct filter *filter,
	uint16_t src_port,
	uint16_t dst_port,
	const char *sign
) {
	struct packet packet = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(
		&packet, sip, dip, src_port, dst_port, IPPROTO_UDP, 0
	);
	assert(res == 0);

	struct packet *packet_ptr = &packet;
	struct value_range *actions;

	if (strcmp(sign, "ports") == 0) {
		FILTER_QUERY(filter, sign_ports, &packet_ptr, &actions, 1);
	} else if (strcmp(sign, "port_src") == 0) {
		FILTER_QUERY(filter, sign_port_src, &packet_ptr, &actions, 1);
	} else {
		assert(0 && "Invalid sign");
	}

	assert(actions->count == 0);
	free_packet(&packet);
}

////////////////////////////////////////////////////////////////////////////////

static void
test_src_dst_ports(void *memory) {
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// action 1:
	//	src_port: [5..7]
	//	dst_port: [1..5]
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_port_src_range(&builder1, 5, 7);
	builder_add_port_dst_range(&builder1, 1, 5);
	struct filter_rule action1 = build_rule(&builder1, 1);

	// action 2:
	//	src_port: [6..8]
	//	dst_port: [3..4]
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_port_src_range(&builder2, 6, 8);
	builder_add_port_dst_range(&builder2, 3, 4);
	struct filter_rule action2 = build_rule(&builder2, 2);

	struct filter_rule actions[2] = {action1, action2};

	// init filter
	struct filter filter;
	res = FILTER_INIT(&filter, sign_ports, actions, 2, &memory_context);
	assert(res == 0);

	query_and_expect_action(&filter, 6, 3, 1, "ports");
	query_and_expect_action(&filter, 8, 3, 2, "ports");

	FILTER_FREE(&filter, sign_ports);

	memory_bfree(&memory_context, memory, 1 << 24);
	void *mem = memory_balloc(&memory_context, 1 << 24);
	assert(mem == memory);
}

static void
test_src_port_only(void *memory) {
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// action 1:
	//	src_port: [500..700]
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_port_src_range(&builder1, 500, 700);
	struct filter_rule action1 = build_rule(&builder1, 1);

	// action 2:
	//	src_port: [600..800]
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_port_src_range(&builder2, 600, 800);
	struct filter_rule action2 = build_rule(&builder2, 2);

	struct filter_rule actions[2] = {action1, action2};

	// init filter
	struct filter filter;
	res = FILTER_INIT(&filter, sign_port_src, actions, 2, &memory_context);
	assert(res == 0);

	query_and_expect_action(&filter, 500, 0, 1, "port_src");
	query_and_expect_action(&filter, 600, 0, 1, "port_src");
	query_and_expect_action(&filter, 700, 0, 1, "port_src");
	query_and_expect_action(&filter, 701, 0, 2, "port_src");
	query_and_expect_action(&filter, 800, 0, 2, "port_src");

	query_and_expect_no_action(&filter, 499, 0, "port_src");
	query_and_expect_no_action(&filter, 801, 0, "port_src");

	FILTER_FREE(&filter, sign_port_src);

	memory_bfree(&memory_context, memory, 1 << 24);
	void *mem = memory_balloc(&memory_context, 1 << 24);
	assert(mem == memory);
}

int
main() {
	log_enable_name("debug");
	void *memory = malloc(1 << 24);

	LOG(INFO, "Running test_src_port_only 10 times...");
	for (size_t i = 0; i < 10; ++i) {
		test_src_port_only(memory);
		if (i >= 5) {
			memset(memory, (int)i, 1 << 24);
		}
	}
	LOG(INFO, "test_src_port_only passed");

	LOG(INFO, "Running test_src_dst_ports 10 times...");
	for (size_t i = 0; i < 10; ++i) {
		test_src_dst_ports(memory);
		if (i >= 5) {
			memset(memory, (int)i, 1 << 24);
		}
	}
	LOG(INFO, "test_src_dst_ports passed");

	free(memory);

	LOG(INFO, "All tests passed");

	return 0;
}
