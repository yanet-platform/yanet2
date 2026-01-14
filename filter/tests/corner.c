#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdio.h>

FILTER_COMPILER_DECLARE(sign_port_src, port_src);
FILTER_QUERY_DECLARE(sign_port_src, port_src);

static void
query_and_expect_action(
	struct filter *filter, uint16_t src_port, uint32_t expected
) {
	struct packet packet = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(
		&packet, sip, dip, src_port, 0, IPPROTO_UDP, 0
	);
	assert(res == 0);
	struct packet *packet_ptr = &packet;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_port_src, &packet_ptr, &actions, 1);
	assert(actions->count >= 1);
	assert(ADDR_OF(&actions->values)[0] == expected);
	free_packet(&packet);
}

static void
query_and_expect_no_action(struct filter *filter, uint16_t src_port) {
	struct packet packet = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(
		&packet, sip, dip, src_port, 0, IPPROTO_UDP, 0
	);
	assert(res == 0);
	struct packet *packet_ptr = &packet;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_port_src, &packet_ptr, &actions, 1);
	assert(actions->count == 0);
	free_packet(&packet);
}

static void
check_single_attribute(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int memory_context_init_result =
		memory_context_init(&memory_context, "test", &allocator);
	assert(memory_context_init_result == 0);

	// first action
	// src port: [5-7] + [6-10] + [15-20]
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_port_src_range(&builder1, 5, 7);
	builder_add_port_src_range(&builder1, 6, 10);
	builder_add_port_src_range(&builder1, 15, 20);
	struct filter_rule rule1 = build_rule(&builder1, 1);

	// second action
	// src port: [11-21]
	struct filter_rule_builder builder2;
	builder_init(&builder2);
	builder_add_port_src_range(&builder2, 11, 21);
	struct filter_rule rule2 = build_rule(&builder2, 2);

	// third action
	// src port: [30-40]
	struct filter_rule_builder builder3;
	builder_init(&builder3);
	builder_add_port_src_range(&builder3, 30, 40);
	struct filter_rule rule3 = build_rule(&builder3, 3);

	// setup rules
	struct filter_rule rules[3] = {rule1, rule2, rule3};

	// setup filter
	struct filter filter;
	int init_result =
		FILTER_INIT(&filter, sign_port_src, rules, 3, &memory_context);
	assert(init_result == 0);

	// make few queries and expect hit
	{
#define queries 18

		uint16_t query_ports[queries] = {
			5,
			6,
			7,
			8,
			9,
			10,
			11,
			12,
			13,
			14,
			15,
			16,
			20,
			21,
			30,
			31,
			35,
			40
		};

		uint32_t expected_actions[queries] = {
			1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 1, 1, 1, 2, 3, 3, 3, 3
		};

		for (size_t i = 0; i < queries; ++i) {
			query_and_expect_action(
				&filter, query_ports[i], expected_actions[i]
			);
		}

#undef queries
	}

	// make few queries without hit
	{
#define queries 6

		uint16_t query_ports[queries] = {45, 1, 2, 3, 4, 25};
		for (size_t i = 0; i < queries; ++i) {
			query_and_expect_no_action(&filter, query_ports[i]);
		}

#undef queries
	}

	FILTER_FREE(&filter, sign_port_src);
}

int
main() {
	log_enable_name("debug");
	void *memory = malloc(1 << 24); // 16 MB

	// Single attribute is corner case because
	// attribute leaf is root in the same time.
	LOG(INFO, "Running check_single_attribute...");
	check_single_attribute(memory);
	LOG(INFO, "check_single_attribute passed");

	LOG(INFO, "All tests passed");

	free(memory);

	return 0;
}
