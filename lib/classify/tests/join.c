// Verifies packed mutable rows and immutable first-match joins.
//
// Tight arenas expose per-row rounding and duplicate-row storage; explicit
// expected indices pin projection isolation, priority and counter identity.
#include "lib/classify/compiler/declare.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/query.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>

static void
run_packed_rows_test(void) {
	void *memory;
	assert(posix_memalign(&memory, 1 << 21, 14 << 20) == 0);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 14 << 20);
	struct memory_context ctx;
	memory_context_init(&ctx, "packed", &allocator);
	size_t initial = block_allocator_free_size(&allocator);
	for (unsigned round = 0; round < 3; ++round) {
		struct value_table table = {0};
		assert(value_table_init(&table, &ctx, "rows", 10000, 257) == 0);
		*value_table_get_ptr(&table, 0, 256) = 11;
		*value_table_get_ptr(&table, 9999, 256) = 29;
		assert(value_table_get(&table, 0, 256) == 11);
		assert(value_table_get(&table, 5000, 256) == 0);
		assert(value_table_get(&table, 9999, 256) == 29);
		value_table_free(&table);
		assert(block_allocator_free_size(&allocator) == initial);
	}
	memory_context_fini(&ctx);
	free(memory);
}

static void
run_failed_rows_cleanup_test(void) {
	void *memory;
	assert(posix_memalign(&memory, 1 << 21, 16384) == 0);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 16384);
	struct memory_context ctx;
	memory_context_init(&ctx, "failure", &allocator);
	size_t initial = block_allocator_free_size(&allocator);
	struct value_table table = {0};
	assert(value_table_init(&table, &ctx, "too-large", 100, 1024) == -1);
	value_table_free(&table);
	assert(block_allocator_free_size(&allocator) == initial);
	assert(value_table_init(&table, &ctx, "retry", 5, 17) == 0);
	*value_table_get_ptr(&table, 4, 16) = 37;
	assert(value_table_get(&table, 4, 16) == 37);
	value_table_free(&table);
	assert(block_allocator_free_size(&allocator) == initial);
	memory_context_fini(&ctx);
	free(memory);
}

// Appends one explicitly enumerated group, leaving its class identifiers
// intact.
static void
add_group(struct classifier *cls, const uint32_t *values, size_t count) {
	assert(value_registry_start(&cls->registry) == 0);
	for (size_t i = 0; i < count; ++i) {
		assert(value_registry_collect(&cls->registry, values[i]) == 0);
	}
}

static void
run_first_match_projection_test(void) {
	void *memory;
	assert(posix_memalign(&memory, 1 << 21, 4 << 20) == 0);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 4 << 20);
	struct memory_context ctx;
	memory_context_init(&ctx, "first-match", &allocator);
	size_t initial = block_allocator_free_size(&allocator);
	struct classifier left = {0}, right = {0};
	assert(classifier_init(&left, &ctx, 5) == 0);
	assert(classifier_init(&right, &ctx, 5) == 0);
	assert(value_registry_init(&left.registry, &ctx, "left") == 0);
	assert(value_registry_init(&right.registry, &ctx, "right") == 0);
	add_group(&left, (uint32_t[]){0, 1}, 2);
	add_group(&left, (uint32_t[]){2}, 1);
	add_group(&left, (uint32_t[]){3}, 1);
	add_group(&right, (uint32_t[]){0, 1}, 2);
	add_group(&right, (uint32_t[]){1, 2}, 2);
	add_group(&right, (uint32_t[]){3}, 1);
	memcpy(left.rule_groups,
	       (uint32_t[]){2, 0, 0, 1, 0},
	       5 * sizeof(uint32_t));
	memcpy(right.rule_groups,
	       (uint32_t[]){2, 1, 0, 1, 1},
	       5 * sizeof(uint32_t));
	struct classifier_rule dummy = {};
	const struct classifier_rule *rules[] = {
		NULL, &dummy, &dummy, &dummy, &dummy
	};
	const uint32_t expected[4][4] = {
		{2, 1, 1, FILTER_RULE_INVALID},
		{2, 1, 1, FILTER_RULE_INVALID},
		{FILTER_RULE_INVALID, 3, 3, FILTER_RULE_INVALID},
		{FILTER_RULE_INVALID,
		 FILTER_RULE_INVALID,
		 FILTER_RULE_INVALID,
		 FILTER_RULE_INVALID},
	};
	struct value_table table = {0};
	assert(classify_join_rules(&ctx, &left, &right, rules, 5, &table) == 0);
	for (uint32_t v = 0; v < 4; ++v) {
		for (uint32_t h = 0; h < 4; ++h) {
			assert(value_table_get(&table, v, h) == expected[v][h]);
		}
	}
	value_table_free(&table);
	classifier_fini(&left, &ctx, 5);
	classifier_fini(&right, &ctx, 5);
	assert(block_allocator_free_size(&allocator) == initial);
	memory_context_fini(&ctx);
	free(memory);
}

static void
run_duplicate_rows_fit_test(void) {
	void *memory;
	assert(posix_memalign(&memory, 1 << 21, 2 << 20) == 0);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 2 << 20);
	struct memory_context ctx;
	memory_context_init(&ctx, "duplicates", &allocator);
	size_t initial = block_allocator_free_size(&allocator);
	struct classifier left = {0}, right = {0};
	assert(classifier_init(&left, &ctx, 1) == 0);
	assert(classifier_init(&right, &ctx, 1) == 0);
	assert(value_registry_init(&left.registry, &ctx, "left") == 0);
	assert(value_registry_init(&right.registry, &ctx, "right") == 0);
	assert(value_registry_start(&left.registry) == 0);
	for (uint32_t v = 0; v < 8192; ++v) {
		assert(value_registry_collect(&left.registry, v) == 0);
	}
	assert(value_registry_start(&right.registry) == 0);
	for (uint32_t h = 0; h < 257; ++h) {
		assert(value_registry_collect(&right.registry, h) == 0);
	}
	left.rule_groups[0] = right.rule_groups[0] = 0;
	struct classifier_rule dummy = {};
	const struct classifier_rule *rules[] = {&dummy};
	struct value_table table = {0};
	assert(classify_join_rules(&ctx, &left, &right, rules, 1, &table) == 0);
	assert(value_table_get(&table, 0, 0) == 0);
	assert(value_table_get(&table, 8191, 256) == 0);
	value_table_free(&table);
	classifier_fini(&left, &ctx, 1);
	classifier_fini(&right, &ctx, 1);
	assert(block_allocator_free_size(&allocator) == initial);
	memory_context_fini(&ctx);
	free(memory);
}

int
main(void) {
	run_packed_rows_test();
	run_failed_rows_cleanup_test();
	run_first_match_projection_test();
	run_duplicate_rows_fit_test();
	puts("packed rows, allocation failure/retry, first-match/projection "
	     "and duplicate rows: PASS");
	return 0;
}
