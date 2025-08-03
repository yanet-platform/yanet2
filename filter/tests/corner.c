#include "utils.h"

#include "attribute.h"
#include "common/memory_block.h"
#include "filter.h"

#include <netinet/in.h>
#include <rte_ip.h>

#include <assert.h>
#include <stdio.h>

void
check_single_attribute(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int memory_context_init_result =
		memory_context_init(&memory_context, "test", &allocator);
	assert(memory_context_init_result == 0);

	// setup single attribute
	const struct filter_attribute *attribute = &attribute_port_src;

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
		filter_init(&filter, &attribute, 1, rules, 3, &memory_context);
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
			struct packet packet = make_packet(
				0, 0, query_ports[i], 0, IPPROTO_UDP, 0, 0
			);
			query_filter_and_expect_action(
				&filter, &packet, expected_actions[i]
			);
			free_packet(&packet);
		}

#undef queries
	}

	// make few queries without hit
	{
#define queries 6

		uint16_t query_ports[queries] = {45, 1, 2, 3, 4, 25};
		for (size_t i = 0; i < queries; ++i) {
			struct packet packet = make_packet(
				0, 0, query_ports[i], 0, IPPROTO_UDP, 0, 0
			);
			query_filter_and_expect_no_actions(&filter, &packet);
			free_packet(&packet);
		}

#undef queries
	}

	filter_free(&filter);
}

void
check_no_attributes(void *memory) {
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	// init memory
	struct memory_context memory_context;
	int memory_context_init_result =
		memory_context_init(&memory_context, "test", &allocator);
	assert(memory_context_init_result == 0);

	// first action
	// src port: [5-7]
	struct filter_rule_builder builder;
	builder_init(&builder);
	builder_add_port_src_range(&builder, 5, 7);
	struct filter_rule action = build_rule(&builder, 1);

	// init filter
	//
	// initialization must fail because of
	// there are no attributes.
	struct filter filter;
	int init_result =
		filter_init(&filter, NULL, 0, &action, 1, &memory_context);

	assert(init_result < 0);

	filter_free(&filter);
}

int
main() {
	void *memory = malloc(1 << 24); // 16 MB

	// Single attribute is corner case because
	// attribute leaf is root in the same time.
	check_single_attribute(memory);

	// Filter initialization must fail
	// in case there are no attributes.
	check_no_attributes(memory);

	puts("OK!");

	free(memory);

	return 0;
}