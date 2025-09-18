#include "filter.h"
#include "rule.h"
#include "utils.h"

#include <assert.h>
#include <netinet/in.h>
#include <stdio.h>
#include <stdlib.h>

////////////////////////////////////////////////////////////////////////////////

void
query_and_check_actions(
	struct filter *filter,
	uint16_t src_port,
	uint32_t ref_actions_count,
	const uint32_t *ref_actions
) {
	struct packet packet = make_packet(
		ip(0, 0, 0, 123),
		ip(0, 0, 1, 65),
		src_port,
		222,
		IPPROTO_UDP,
		0,
		0
	);
	const uint32_t *actions;
	uint32_t actions_count;
	int res = filter_query(filter, &packet, &actions, &actions_count);
	assert(res == 0);
	assert(actions_count == ref_actions_count);
	for (uint32_t i = 0; i < actions_count; ++i) {
		assert(actions[i] == ref_actions[i]);
	}
	free_packet(&packet);
}

////////////////////////////////////////////////////////////////////////////////

void
test1(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context mctx;
	int res = memory_context_init(&mctx, "test", &allocator);
	assert(res == 0);

	// build rules
	struct filter_rule rules[8];

	// non-terminal rule for all categories
	struct filter_rule_builder b1;
	const uint32_t action1 = ACTION_NON_TERMINATE | 1;
	{
		builder_init(&b1);
		builder_add_port_src_range(&b1, 200, 300);
		rules[0] = build_rule(&b1, action1);
	}

	// terminal rule for categories 0 and 1
	struct filter_rule_builder b2;
	const uint32_t action2 = MAKE_ACTION_CATEGORY_MASK(0b11) | 2;
	{
		builder_init(&b2);
		builder_add_port_src_range(&b2, 250, 350);
		rules[1] = build_rule(&b2, action2);
	}

	// terminal rule for all categories
	struct filter_rule_builder b3;
	const uint32_t action3 = 3;
	{
		builder_init(&b3);
		builder_add_port_src_range(&b3, 150, 260);
		rules[2] = build_rule(&b3, action3);
	}

	// non-terminal rule for category 0
	struct filter_rule_builder b4;
	const uint32_t action4 =
		MAKE_ACTION_CATEGORY_MASK(0b01) | ACTION_NON_TERMINATE | 4;
	{
		builder_init(&b4);
		builder_add_port_src_range(&b4, 255, 350);
		rules[3] = build_rule(&b4, action4);
	}

	// non-terminal rule for category 0 and 1
	struct filter_rule_builder b5;
	const uint32_t action5 =
		MAKE_ACTION_CATEGORY_MASK(0b11) | ACTION_NON_TERMINATE | 5;
	{
		builder_init(&b5);
		builder_add_port_src_range(&b5, 100, 300);
		rules[4] = build_rule(&b5, action5);
	}

	// non-terminal rule for all categories
	struct filter_rule_builder b6;
	const uint32_t action6 = ACTION_NON_TERMINATE | 6;
	{
		builder_init(&b6);
		builder_add_port_src_range(&b6, 100, 600);
		rules[5] = build_rule(&b6, action6);
	}

	// terminal rule for category 1
	struct filter_rule_builder b7;
	const uint32_t action7 = MAKE_ACTION_CATEGORY_MASK(0b10) | 7;
	{
		builder_init(&b7);
		builder_add_port_src_range(&b7, 350, 450);
		rules[6] = build_rule(&b7, action7);
	}

	// non-terminal rule for category 1
	struct filter_rule_builder b8;
	const uint32_t action8 = MAKE_ACTION_CATEGORY_MASK(0b10) | 8;
	{
		builder_init(&b8);
		builder_add_port_src_range(&b8, 400, 500);
		rules[7] = build_rule(&b8, action8);
	}

	// init filter
	const struct filter_attribute *attrs[1] = {&attribute_port_src};
	struct filter filter;
	res = filter_init(&filter, attrs, 1, rules, 8, &mctx);
	assert(res == 0);

	// query packet 1
	{
		uint32_t actions[3] = {action6, action7, action8};
		query_and_check_actions(&filter, 440, 3, actions);
		uint32_t found = find_actions_with_category(actions, 3, 1);
		assert(found == 2);
		assert(actions[0] == action6 && actions[1] == action7);
	}

	// query packet 2
	{
		uint32_t actions[3] = {action1, action2, action3};
		query_and_check_actions(&filter, 255, 3, actions);

		// check for category 0
		{
			uint32_t tmp[3];
			memcpy(tmp, actions, 3 * 4);
			uint32_t found = find_actions_with_category(tmp, 3, 0);
			assert(found == 2);
			assert(tmp[0] == action1 && tmp[1] == action2);
		}

		// check for category 1
		{
			uint32_t found =
				find_actions_with_category(actions, 3, 1);
			assert(found == 2);
			assert(actions[0] == action1 && actions[1] == action2);
		}
	}

	// query packet 3
	{
		uint32_t actions[2] = {action1, action3};
		query_and_check_actions(&filter, 240, 2, actions);
	}

	// query packet 4
	{
		uint32_t actions[4] = {action2, action4, action6, action7};
		query_and_check_actions(&filter, 350, 4, actions);

		// check for category 0
		{
			uint32_t tmp[4];
			memcpy(tmp, actions, 4 * 4);
			uint32_t found = find_actions_with_category(tmp, 4, 0);
			assert(found == 1);
			assert(tmp[0] == action2);
		}

		// check for category 1
		{
			uint32_t found =
				find_actions_with_category(actions, 4, 0);
			assert(found == 1);
			assert(actions[0] == action2);
		}
	}

	// query packet 5
	{
		uint32_t actions[3] = {action6, action7, action8};
		query_and_check_actions(&filter, 450, 3, actions);

		// check for category 0
		{
			uint32_t tmp[3];
			memcpy(tmp, actions, 3 * 4);
			uint32_t found = find_actions_with_category(tmp, 3, 0);
			assert(found == 1);
			assert(tmp[0] == action6);
		}

		// check for category 1
		{
			uint32_t found =
				find_actions_with_category(actions, 3, 1);
			assert(found == 2);
			assert(actions[0] == action6 && actions[1] == action7);
		}
	}

	// free filter
	filter_free(&filter);
}

////////////////////////////////////////////////////////////////////////////////

void
test2() {
	{
		uint32_t actions[3] = {
			MAKE_ACTION_CATEGORY_MASK(0b01) | 1,
			2,
			MAKE_ACTION_CATEGORY_MASK(0b11) | ACTION_NON_TERMINATE |
				2
		};
		{
			uint32_t tmp[3];
			memcpy(tmp, actions, 3 * 4);
			uint32_t found = find_actions_with_category(tmp, 3, 0);
			assert(found == 1);
			assert(tmp[0] == actions[0]);
		}
		{
			uint32_t tmp[3];
			memcpy(tmp, actions, 3 * 4);
			uint32_t found = find_actions_with_category(tmp, 3, 1);
			assert(found == 1);
			assert(tmp[0] == actions[1]);
		}
	}
	{
		uint32_t actions[3] = {
			MAKE_ACTION_CATEGORY_MASK(0b01) | 1,
			2 | ACTION_NON_TERMINATE,
			MAKE_ACTION_CATEGORY_MASK(0b11) | ACTION_NON_TERMINATE |
				2
		};
		{
			uint32_t tmp[3];
			memcpy(tmp, actions, 3 * 4);
			uint32_t found = find_actions_with_category(tmp, 3, 0);
			assert(found == 1);
			assert(tmp[0] == actions[0]);
		}
		{
			uint32_t tmp[3];
			memcpy(tmp, actions, 3 * 4);
			uint32_t found = find_actions_with_category(tmp, 3, 1);
			assert(found == 2);
			assert(tmp[0] == actions[1] && tmp[1] == actions[2]);
		}
		{
			uint32_t tmp[3];
			memcpy(tmp, actions, 3 * 4);
			uint32_t found = find_actions_with_category(tmp, 3, 2);
			assert(found == 1);
			assert(tmp[0] == actions[1]);
		}
	}
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	void *memory = malloc(1 << 24); // 16MB

	puts("test1...");
	test1(memory);

	puts("test2...");
	test2();

	puts("OK");

	free(memory);

	return 0;
}