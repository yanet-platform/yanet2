// Verifies the immutable first-match joins of the final rule tables.
//
// Explicit expected indices pin projection isolation and priority; a tight
// arena pins the duplicate-row canonicalization and the cleanup of failed
// builds.
#include "lib/classify/compiler/declare.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/query.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>

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
	struct rule_table table = {0};
	assert(classify_join_rules(
		       &ctx, "join", &left, &right, rules, 5, &table
	       ) == 0);
	for (uint32_t v = 0; v < 4; ++v) {
		for (uint32_t h = 0; h < 4; ++h) {
			assert(rule_table_get(&table, v, h) == expected[v][h]);
		}
	}
	// The two equal rows of the expected matrix share one interned row,
	// and the uncovered row interns as the all-invalid one.
	assert(table.class_count == 3);
	rule_table_free(&table);
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
	struct rule_table table = {0};
	assert(classify_join_rules(
		       &ctx, "join", &left, &right, rules, 1, &table
	       ) == 0);
	assert(rule_table_get(&table, 0, 0) == 0);
	assert(rule_table_get(&table, 8191, 256) == 0);
	// All 8192 rows are equal, so exactly one row is interned.
	assert(table.class_count == 1);
	rule_table_free(&table);
	classifier_fini(&left, &ctx, 1);
	classifier_fini(&right, &ctx, 1);
	assert(block_allocator_free_size(&allocator) == initial);
	memory_context_fini(&ctx);
	free(memory);
}

static void
run_failed_join_cleanup_test(void) {
	// The classifiers live under a roomy arena; the joint builds under
	// a small dedicated one, so the packed rows are exactly the
	// allocation that stops fitting.
	void *regs_memory;
	assert(posix_memalign(&regs_memory, 1 << 21, 1 << 20) == 0);
	struct block_allocator regs_allocator;
	block_allocator_init(&regs_allocator);
	block_allocator_put_arena(&regs_allocator, regs_memory, 1 << 20);
	struct memory_context regs_ctx;
	memory_context_init(&regs_ctx, "failure:regs", &regs_allocator);

	void *join_memory;
	assert(posix_memalign(&join_memory, 1 << 21, 8 << 10) == 0);
	struct block_allocator join_allocator;
	block_allocator_init(&join_allocator);
	block_allocator_put_arena(&join_allocator, join_memory, 8 << 10);
	struct memory_context join_ctx;
	memory_context_init(&join_ctx, "failure:join", &join_allocator);
	size_t initial = block_allocator_free_size(&join_allocator);

	struct classifier left = {0}, right_big = {0}, right_small = {0};
	assert(classifier_init(&left, &regs_ctx, 1) == 0);
	assert(classifier_init(&right_big, &regs_ctx, 1) == 0);
	assert(classifier_init(&right_small, &regs_ctx, 1) == 0);
	assert(value_registry_init(&left.registry, &regs_ctx, "left") == 0);
	assert(value_registry_init(&right_big.registry, &regs_ctx, "big") == 0);
	assert(value_registry_init(&right_small.registry, &regs_ctx, "small") == 0);
	assert(value_registry_start(&left.registry) == 0);
	for (uint32_t v = 0; v < 64; ++v) {
		assert(value_registry_collect(&left.registry, v) == 0);
	}
	assert(value_registry_start(&right_big.registry) == 0);
	for (uint32_t h = 0; h < 16384; ++h) {
		assert(value_registry_collect(&right_big.registry, h) == 0);
	}
	assert(value_registry_start(&right_small.registry) == 0);
	for (uint32_t h = 0; h < 256; ++h) {
		assert(value_registry_collect(&right_small.registry, h) == 0);
	}
	left.rule_groups[0] = 0;
	right_big.rule_groups[0] = 0;
	right_small.rule_groups[0] = 0;
	struct classifier_rule dummy = {};
	const struct classifier_rule *rules[] = {&dummy};
	// The interned row of 16384 cells cannot fit the 8 KiB arena, so
	// the build fails at the packed rows; the half-built table frees
	// as cleanly as an empty one and the same arena serves the retry.
	struct rule_table table = {0};
	assert(classify_join_rules(
		       &join_ctx, "join", &left, &right_big, rules, 1, &table
	       ) == -1);
	rule_table_free(&table);
	assert(block_allocator_free_size(&join_allocator) == initial);
	struct rule_table retry = {0};
	assert(classify_join_rules(
		       &join_ctx, "retry", &left, &right_small, rules, 1, &retry
	       ) == 0);
	assert(retry.class_count == 1);
	rule_table_free(&retry);
	assert(block_allocator_free_size(&join_allocator) == initial);
	classifier_fini(&left, &regs_ctx, 1);
	classifier_fini(&right_big, &regs_ctx, 1);
	classifier_fini(&right_small, &regs_ctx, 1);
	memory_context_fini(&join_ctx);
	memory_context_fini(&regs_ctx);
	free(join_memory);
	free(regs_memory);
}

int
main(void) {
	run_first_match_projection_test();
	run_duplicate_rows_fit_test();
	run_failed_join_cleanup_test();
	puts("first-match/projection, duplicate rows and failed-build "
	     "cleanup: PASS");
	return 0;
}
