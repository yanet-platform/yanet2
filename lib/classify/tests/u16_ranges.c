// Range attribute test of the region-based classification library.
//
// Pins the boundary partition of the 16 bit domain and the classes the
// range attribute commits to a brute force reference: the piece starts
// are exactly zero, every range start and every value past a range end,
// every value resolves into the piece holding it, two values share a
// class exactly when the same rules cover them, and the registry gives
// every rule exactly the classes of the values it covers. The scenarios
// cover the edge ranges - the whole domain, the first and the last
// value alone, reversed ranges - and random rulesets of several ranges
// per rule.

#include "lib/classify/compiler/u16_ranges.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/classify.h"

#include "common/container_of.h"
#include "common/memory.h"
#include "common/memory_block.h"

#include <assert.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define TEST_ARENA_SIZE ((size_t)1 << 26)
#define TEST_SPANS_MAX 3
#define TEST_RULES_MAX 256

// A range rule of up to three ranges.
struct test_rule {
	struct classifier_rule rule;
	struct filter_u16_span spans[TEST_SPANS_MAX];
	uint32_t count;
};

// The ranges of a test rule, as the attribute getter.
static void
test_rule_get_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	ranges->count = test_rule->count;
	ranges->items = test_rule->spans;
}

CLASSIFY_U16_RANGES_COMPILE(
	test_range, struct classify_attr_port, test_rule_get_ranges
)

// Returns the next value of a fixed seed generator.
static uint64_t
test_rand(uint64_t *state) {
	uint64_t value = *state;
	value ^= value << 13;
	value ^= value >> 7;
	value ^= value << 17;
	*state = value;
	return value;
}

// A random range: a single value, a short or a wide one, or reversed.
static struct filter_u16_span
test_random_span(uint64_t *state) {
	uint32_t from = test_rand(state) & 0xffff;
	uint32_t width;
	switch (test_rand(state) % 4) {
	case 0:
		width = 0;
		break;
	case 1:
		width = test_rand(state) % 64;
		break;
	case 2:
		width = test_rand(state) % 16384;
		break;
	default:
		return (struct filter_u16_span){from | 1, from & ~1u};
	}
	uint32_t to = from + width > 0xffff ? 0xffff : from + width;
	return (struct filter_u16_span){from, to};
}

// Whether a range of the rule covers the value.
static int
test_rule_covers(const struct test_rule *rule, uint32_t value) {
	for (uint32_t idx = 0; idx < rule->count; ++idx) {
		if (rule->spans[idx].from <= value &&
		    value <= rule->spans[idx].to) {
			return 1;
		}
	}
	return 0;
}

// Whether the class is one of the values the registry holds for the
// group.
static int
test_group_has_class(
	const struct value_registry *registry, uint32_t group, uint32_t class
) {
	const struct value_range *range = ADDR_OF(&registry->ranges) + group;
	const uint32_t *values = ADDR_OF(&range->values);
	for (uint64_t idx = 0; idx < range->count; ++idx) {
		if (values[idx] == class) {
			return 1;
		}
	}
	return 0;
}

/*
 * Asserts that the boundary partition of the ranges of the rules equals
 * the brute force one.
 */
static void
test_check_partition(const struct test_rule *rules, uint32_t rule_count) {
	static uint8_t expected[65537];
	memset(expected, 0, sizeof(expected));
	expected[0] = 1;

	struct filter_u16_partition partition;
	filter_u16_partition_init(&partition);
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct test_rule *rule = rules + rule_idx;
		for (uint32_t idx = 0; idx < rule->count; ++idx) {
			uint32_t from = rule->spans[idx].from;
			uint32_t to = rule->spans[idx].to;
			filter_u16_partition_add(&partition, from, to);
			if (from <= to) {
				expected[from] = 1;
				expected[to + 1] = 1;
			}
		}
	}
	filter_u16_partition_rank(&partition);

	uint32_t piece = 0;
	for (uint32_t value = 0; value < 65536; ++value) {
		if (value != 0 && expected[value]) {
			++piece;
		}
		assert(filter_u16_partition_is_start(&partition, value) ==
		       expected[value]);
		assert(filter_u16_partition_piece(&partition, value) == piece);
	}
	assert(partition.count == piece + 1);
}

/*
 * Asserts that the committed classes of the rules equal the brute force
 * ones: two values share a class exactly when the same rules cover them,
 * and the registry gives every rule exactly the classes of the values it
 * covers.
 */
static void
test_check_classes(
	struct memory_context *mctx,
	const struct test_rule *rules,
	uint32_t rule_count
) {
	assert(rule_count <= TEST_RULES_MAX);
	const struct classifier_rule *rule_ptrs[TEST_RULES_MAX];
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		rule_ptrs[idx] = &rules[idx].rule;
	}

	struct classify_attr_port attr;
	struct classifier cls;
	assert(classify_test_range_compile(
		       mctx, "range", rule_ptrs, rule_count, &attr, &cls
	       ) == 0);

	// The covering rule set of every value, one bit per rule.
	enum { words = TEST_RULES_MAX / 64 };
	static uint64_t cover[65536][words];
	memset(cover, 0, sizeof(cover));
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		for (uint32_t value = 0; value < 65536; ++value) {
			if (test_rule_covers(rules + rule_idx, value)) {
				cover[value][rule_idx / 64] |=
					(uint64_t)1 << (rule_idx % 64);
			}
		}
	}

	// A representative value of every class.
	static uint32_t first_of[65536];
	memset(first_of, 0xff, sizeof(first_of));
	static uint32_t classes[65536];
	uint32_t class_count = 0;
	for (uint32_t value = 0; value < 65536; ++value) {
		uint32_t class = vline_get(&attr.line, value);
		assert(class < 65536);
		if (first_of[class] == UINT32_MAX) {
			first_of[class] = value;
			classes[class_count++] = class;
			continue;
		}
		assert(memcmp(cover[value],
			      cover[first_of[class]],
			      sizeof(cover[value])) == 0);
	}

	for (uint32_t first = 0; first < class_count; ++first) {
		uint32_t value = first_of[classes[first]];
		for (uint32_t second = first + 1; second < class_count;
		     ++second) {
			assert(memcmp(cover[value],
				      cover[first_of[classes[second]]],
				      sizeof(cover[value])) != 0);
		}
		for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
			assert(test_group_has_class(
				       &cls.registry,
				       cls.rule_groups[rule_idx],
				       classes[first]
			       ) == test_rule_covers(rules + rule_idx, value));
		}
	}

	classifier_fini(&cls, mctx, rule_count);
	classify_attr_port_free(mctx, &attr);
}

// Verifies the partition and the classes of the edge ranges: the whole
// domain, the first and the last value alone, ranges ending at the last
// value and a reversed range.
static void
run_edge_ranges_test(struct memory_context *mctx) {
	const struct test_rule rules[] = {
		{.spans = {{1024, 65535}}, .count = 1},
		{.spans = {{65535, 65535}}, .count = 1},
		{.spans = {{0, 1023}}, .count = 1},
		{.spans = {{0, 0}, {40000, 30000}}, .count = 2},
		{.spans = {{0, 65535}}, .count = 1},
	};
	const uint32_t rule_count = sizeof(rules) / sizeof(rules[0]);

	test_check_partition(rules, rule_count);
	test_check_classes(mctx, rules, rule_count);
}

// Verifies the partition and the classes of random rulesets of up to
// three ranges per rule.
static void
run_random_ranges_test(struct memory_context *mctx) {
	static struct test_rule rules[TEST_RULES_MAX];
	uint64_t state = 3;
	for (uint32_t round = 0; round < 4; ++round) {
		uint32_t rule_count = 16 << (round * 2) > TEST_RULES_MAX
					      ? TEST_RULES_MAX
					      : 16 << (round * 2);
		for (uint32_t idx = 0; idx < rule_count; ++idx) {
			rules[idx].count =
				1 + test_rand(&state) % TEST_SPANS_MAX;
			for (uint32_t span = 0; span < rules[idx].count;
			     ++span) {
				rules[idx].spans[span] =
					test_random_span(&state);
			}
		}

		test_check_partition(rules, rule_count);
		test_check_classes(mctx, rules, rule_count);
	}
}

typedef void (*scenario_func)(struct memory_context *);

// Runs the scenario over a fresh poisoned arena, so a value a build
// leaves unwritten never reads as a valid one.
static void
run_scenario(scenario_func scenario) {
	void *memory = malloc(TEST_ARENA_SIZE);
	assert(memory != NULL);
	memset(memory, 0xa5, TEST_ARENA_SIZE);

	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, TEST_ARENA_SIZE);

	struct memory_context mctx;
	assert(memory_context_init(&mctx, "test", &allocator) == 0);

	scenario(&mctx);

	memory_context_fini(&mctx);
	free(memory);
}

int
main(void) {
	run_scenario(run_edge_ranges_test);
	run_scenario(run_random_ranges_test);

	return 0;
}
