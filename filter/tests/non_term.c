#include "attribute.h"
#include "filter.h"
#include "rule.h"
#include "utils.h"
#include <assert.h>
#include <netinet/in.h>

////////////////////////////////////////////////////////////////////////////////

void
query_and_expect_actions(
	struct filter *filter,
	uint16_t src_port,
	uint16_t dst_port,
	uint32_t action_count,
	uint32_t *actions
) {
	struct packet packet = make_packet(
		ip(0, 0, 0, 123),
		ip(0, 0, 1, 65),
		src_port,
		dst_port,
		IPPROTO_UDP,
		0,
		0
	);
	query_filter_and_expect_actions(filter, &packet, action_count, actions);
	free_packet(&packet);
}

////////////////////////////////////////////////////////////////////////////////

void
test1(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// build rules
	struct filter_rule rules[3];

	// make rule 1
	struct filter_rule_builder b1;
	{
		builder_init(&b1);
		builder_add_port_src_range(&b1, 100, 200);
		builder_add_port_dst_range(&b1, 300, 500);
		rules[0] = build_rule(&b1, ACTION_NON_TERMINATE | 1);
	}

	// make rule 2
	struct filter_rule_builder b2;
	{
		builder_init(&b2);
		builder_add_port_src_range(&b2, 50, 150);
		builder_add_port_dst_range(&b2, 400, 600);
		rules[1] = build_rule(&b2, ACTION_NON_TERMINATE | 2);
	}

	// make rule 3
	struct filter_rule_builder b3;
	{
		builder_init(&b3);
		builder_add_port_src_range(&b3, 10, 240);
		builder_add_port_dst_range(&b3, 450, 650);
		rules[2] = build_rule(&b3, ACTION_NON_TERMINATE | 3);
	}

	// build filter
	struct filter filter;
	const struct filter_attribute *attrs[2] = {
		&attribute_port_src, &attribute_port_dst
	};
	res = filter_init(&filter, attrs, 2, rules, 3, &memory_context);
	assert(res == 0);

	// query packets

	// query packet which corresponds to all rules
	{
		const uint16_t src_port = 110;
		const uint16_t dst_port = 460;
		uint32_t ref_actions[3] = {
			ACTION_NON_TERMINATE | 1,
			ACTION_NON_TERMINATE | 2,
			ACTION_NON_TERMINATE | 3
		};
		query_and_expect_actions(
			&filter, src_port, dst_port, 3, ref_actions
		);
	}

	// query packet which corresponds to rules 1 and 3
	{
		const uint16_t src_port = 190;
		const uint16_t dst_port = 460;
		uint32_t ref_actions[2] = {
			ACTION_NON_TERMINATE | 1, ACTION_NON_TERMINATE | 3
		};
		query_and_expect_actions(
			&filter, src_port, dst_port, 2, ref_actions
		);
	}

	// query packet which corresponds to rules 2 and 3
	{
		const uint16_t src_port = 60;
		const uint16_t dst_port = 460;
		uint32_t ref_actions[2] = {
			ACTION_NON_TERMINATE | 2, ACTION_NON_TERMINATE | 3
		};
		query_and_expect_actions(
			&filter, src_port, dst_port, 2, ref_actions
		);
	}

	// query packet which corresponds to rule 1 only
	{
		const uint16_t src_port = 190;
		const uint16_t dst_port = 310;
		uint32_t ref_actions[1] = {ACTION_NON_TERMINATE | 1};
		query_and_expect_actions(
			&filter, src_port, dst_port, 1, ref_actions
		);
	}

	// query packet which corresponds to rule 3 only
	{
		const uint16_t src_port = 20;
		const uint16_t dst_port = 500;
		uint32_t ref_actions[1] = {ACTION_NON_TERMINATE | 3};
		query_and_expect_actions(
			&filter, src_port, dst_port, 1, ref_actions
		);
	}

	// query packet which corresponds to no rules
	{
		const uint16_t src_port = 2000;
		const uint16_t dst_port = 500;
		query_and_expect_actions(&filter, src_port, dst_port, 0, NULL);
	}

	// free filter
	filter_free(&filter);
}

////////////////////////////////////////////////////////////////////////////////

void
test2(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// build rules
	struct filter_rule rules[4];

	// make rule 1
	struct filter_rule_builder b1;
	{
		builder_init(&b1);
		builder_add_port_src_range(&b1, 100, 200);
		builder_add_port_dst_range(&b1, 300, 500);
		rules[0] = build_rule(&b1, ACTION_NON_TERMINATE | 1);
	}

	// make rule 2
	struct filter_rule_builder b2;
	{
		builder_init(&b2);
		builder_add_port_src_range(&b2, 50, 150);
		builder_add_port_dst_range(&b2, 400, 600);
		rules[1] = build_rule(&b2, ACTION_NON_TERMINATE | 2);
	}

	// make rule 3 with terminal action
	struct filter_rule_builder b3;
	{
		builder_init(&b3);
		builder_add_port_src_range(&b3, 10, 240);
		builder_add_port_dst_range(&b3, 450, 650);
		rules[2] = build_rule(&b3, 3);
	}

	// make rule 4
	struct filter_rule_builder b4;
	{
		builder_init(&b4);
		builder_add_port_src_range(&b4, 5, 300);
		builder_add_port_dst_range(&b4, 250, 660);
		rules[3] = build_rule(&b4, 4);
	}

	// build filter
	struct filter filter;
	const struct filter_attribute *attrs[2] = {
		&attribute_port_src, &attribute_port_dst
	};
	res = filter_init(&filter, attrs, 2, rules, 4, &memory_context);
	assert(res == 0);

	// query packets

	// query packet which corresponds to all rules, but only 1, 2, 3 are
	// returned
	{
		const uint16_t src_port = 110;
		const uint16_t dst_port = 460;
		uint32_t ref_actions[3] = {
			ACTION_NON_TERMINATE | 1, ACTION_NON_TERMINATE | 2, 3
		};
		query_and_expect_actions(
			&filter, src_port, dst_port, 3, ref_actions
		);
	}

	// query packet which corresponds to rules 1, 3 and 4, but only 1 and 3
	// are returned
	{
		const uint16_t src_port = 190;
		const uint16_t dst_port = 460;
		uint32_t ref_actions[2] = {ACTION_NON_TERMINATE | 1, 3};
		query_and_expect_actions(
			&filter, src_port, dst_port, 2, ref_actions
		);
	}

	// query packet which corresponds to rules 2, 3 and 4, byt only 2 and 3
	// are returned
	{
		const uint16_t src_port = 60;
		const uint16_t dst_port = 460;
		uint32_t ref_actions[2] = {ACTION_NON_TERMINATE | 2, 3};
		query_and_expect_actions(
			&filter, src_port, dst_port, 2, ref_actions
		);
	}

	// query packet which corresponds to rule 1 and 4
	{
		const uint16_t src_port = 190;
		const uint16_t dst_port = 310;
		uint32_t ref_actions[2] = {ACTION_NON_TERMINATE | 1, 4};
		query_and_expect_actions(
			&filter, src_port, dst_port, 2, ref_actions
		);
	}

	// query packet which corresponds to rules 3 and 4, but only 3 is
	// returned
	{
		const uint16_t src_port = 20;
		const uint16_t dst_port = 500;
		uint32_t ref_actions[1] = {3};
		query_and_expect_actions(
			&filter, src_port, dst_port, 1, ref_actions
		);
	}

	// query packet which corresponds to no rules
	{
		const uint16_t src_port = 2000;
		const uint16_t dst_port = 500;
		query_and_expect_actions(&filter, src_port, dst_port, 0, NULL);
	}

	// query packet which corresponds to rule 4 only
	{
		const uint16_t src_port = 5;
		const uint16_t dst_port = 500;
		uint32_t ref_actions[1] = {4};
		query_and_expect_actions(
			&filter, src_port, dst_port, 1, ref_actions
		);
	}

	// free filter
	filter_free(&filter);
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	void *memory = malloc(1 << 24); // 16MB

	puts("test1...");
	test1(memory);

	puts("test2...");
	test2(memory);

	puts("OK");

	free(memory);

	return 0;
}