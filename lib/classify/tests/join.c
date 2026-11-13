// Verifies the class joins of two stages and the immutable first-match
// joins of the final rule tables.
//
// The class join must reproduce the canonical form of the test reference
// exactly. Explicit expected indices pin projection isolation and priority;
// a tight arena pins the duplicate-row canonicalization and the cleanup of
// failed builds.
#include "lib/classify/compiler/declare.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/query.h"
#include "lib/classify/tests/join_reference.h"
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

// A projection covering a handful of classes of a large family shares
// one interned class across every uncovered class.
static void
run_uncovered_classes_test(void) {
	void *memory;
	assert(posix_memalign(&memory, 1 << 21, 2 << 20) == 0);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 2 << 20);
	struct memory_context ctx;
	memory_context_init(&ctx, "uncovered", &allocator);
	size_t initial = block_allocator_free_size(&allocator);
	struct classifier left = {0}, right = {0};
	assert(classifier_init(&left, &ctx, 1) == 0);
	assert(classifier_init(&right, &ctx, 1) == 0);
	assert(value_registry_init(&left.registry, &ctx, "left") == 0);
	assert(value_registry_init(&right.registry, &ctx, "right") == 0);
	// The left space spans classes up to 4095 through an unreferenced
	// group, so the rule's group covers only the class zero.
	assert(value_registry_start(&left.registry) == 0);
	assert(value_registry_collect(&left.registry, 0) == 0);
	assert(value_registry_start(&left.registry) == 0);
	assert(value_registry_collect(&left.registry, 4095) == 0);
	assert(value_registry_start(&right.registry) == 0);
	for (uint32_t h = 0; h < 257; ++h) {
		assert(value_registry_collect(&right.registry, h) == 0);
	}
	left.rule_groups[0] = 0;
	right.rule_groups[0] = 0;
	struct classifier_rule dummy = {};
	const struct classifier_rule *rules[] = {&dummy};
	struct rule_table table = {0};
	assert(classify_join_rules(
		       &ctx, "join", &left, &right, rules, 1, &table
	       ) == 0);
	assert(table.class_count == 2);
	assert(rule_table_get(&table, 0, 256) == 0);
	for (uint32_t v = 1; v < 4096; ++v) {
		assert(rule_table_get(&table, v, 128) == FILTER_RULE_INVALID);
	}
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
	assert(value_registry_init(&right_small.registry, &regs_ctx, "small") ==
	       0);
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

// An arena backed memory context whose free size is checked on close.
struct join_arena {
	void *memory;
	struct block_allocator allocator;
	struct memory_context ctx;
	size_t initial;
};

static void
join_arena_open(struct join_arena *arena, size_t size) {
	assert(posix_memalign(&arena->memory, 1 << 21, size) == 0);
	block_allocator_init(&arena->allocator);
	block_allocator_put_arena(&arena->allocator, arena->memory, size);
	memory_context_init(&arena->ctx, "class-join", &arena->allocator);
	arena->initial = block_allocator_free_size(&arena->allocator);
}

static void
join_arena_close(struct join_arena *arena) {
	assert(block_allocator_free_size(&arena->allocator) == arena->initial);
	memory_context_fini(&arena->ctx);
	free(arena->memory);
}

// A stage with an empty registry and the given rule to group mapping;
// the test appends its groups afterwards.
static void
stage_init(
	struct classifier *cls,
	struct memory_context *ctx,
	const uint32_t *rule_groups,
	uint32_t rule_count
) {
	assert(classifier_init(cls, ctx, rule_count) == 0);
	assert(value_registry_init(&cls->registry, ctx, "stage") == 0);
	memcpy(cls->rule_groups, rule_groups, rule_count * sizeof(uint32_t));
}

static int
cmp_u32(const void *a, const void *b) {
	uint32_t x = *(const uint32_t *)a;
	uint32_t y = *(const uint32_t *)b;
	return (x > y) - (x < y);
}

// Joins the stages with both the join and the test reference and asserts
// the join reproduces the canonical form of the reference exactly.
//
// The canonical form numbers the class no pair holds zero when a cell
// has it, the rest by first appearance in row-major order, and sorts
// every pair range. Returns the joined table cell at the given position
// for the caller's explicit checks.
static uint32_t
assert_join_matches_reference(
	struct memory_context *ctx,
	const struct classifier *left,
	const struct classifier *right,
	uint32_t rule_count,
	uint32_t probe_v,
	uint32_t probe_h
) {
	struct value_table table, ref_table;
	struct classifier out = {0}, ref = {0};
	assert(classify_join(
		       ctx, "join", left, right, rule_count, &table, &out
	       ) == 0);
	assert(classify_join_reference(
		       ctx,
		       "reference",
		       left,
		       right,
		       rule_count,
		       &ref_table,
		       &ref
	       ) == 0);
	assert(table.v_dim == ref_table.v_dim);
	assert(table.h_dim == ref_table.h_dim);
	assert(memcmp(out.rule_groups,
		      ref.rule_groups,
		      rule_count * sizeof(uint32_t)) == 0);
	assert(value_registry_capacity(&out.registry) ==
	       value_registry_capacity(&ref.registry));
	assert(out.registry.range_count == ref.registry.range_count);

	uint32_t space = 1;
	for (uint32_t v = 0; v < ref_table.v_dim; ++v) {
		for (uint32_t h = 0; h < ref_table.h_dim; ++h) {
			uint32_t id = value_table_get(&ref_table, v, h);
			space = id + 1 > space ? id + 1 : space;
		}
	}
	uint8_t *covered = calloc(space, 1);
	uint32_t *canonical = malloc(space * sizeof(uint32_t));
	assert(covered != NULL && canonical != NULL);
	memset(canonical, 0xff, space * sizeof(uint32_t));
	const struct value_range *ref_ranges = ADDR_OF(&ref.registry.ranges);
	for (uint64_t pair = 0; pair < ref.registry.range_count; ++pair) {
		const uint32_t *values = ADDR_OF(&ref_ranges[pair].values);
		for (uint64_t idx = 0; idx < ref_ranges[pair].count; ++idx) {
			covered[values[idx]] = 1;
		}
	}
	uint32_t next = 0;
	for (uint32_t id = 0; id < space; ++id) {
		if (!covered[id]) {
			canonical[id] = 0;
			next = 1;
		}
	}
	for (uint32_t v = 0; v < ref_table.v_dim; ++v) {
		for (uint32_t h = 0; h < ref_table.h_dim; ++h) {
			uint32_t id = value_table_get(&ref_table, v, h);
			if (canonical[id] == UINT32_MAX) {
				canonical[id] = next++;
			}
			assert(value_table_get(&table, v, h) == canonical[id]);
		}
	}

	const struct value_range *ranges = ADDR_OF(&out.registry.ranges);
	for (uint64_t pair = 0; pair < ref.registry.range_count; ++pair) {
		uint64_t count = ref_ranges[pair].count;
		assert(ranges[pair].count == count);
		uint32_t *expected = malloc((count + 1) * sizeof(uint32_t));
		assert(expected != NULL);
		const uint32_t *values = ADDR_OF(&ref_ranges[pair].values);
		for (uint64_t idx = 0; idx < count; ++idx) {
			expected[idx] = canonical[values[idx]];
		}
		qsort(expected, count, sizeof(uint32_t), cmp_u32);
		assert(count == 0 || memcmp(ADDR_OF(&ranges[pair].values),
					    expected,
					    count * sizeof(uint32_t)) == 0);
		free(expected);
	}

	uint32_t probe = value_table_get(&table, probe_v, probe_h);
	free(canonical);
	free(covered);
	value_table_free(&table);
	value_table_free(&ref_table);
	classifier_fini(&out, ctx, rule_count);
	classifier_fini(&ref, ctx, rule_count);
	return probe;
}

// Releases both stages of a scenario.
static void
stages_fini(
	struct memory_context *ctx,
	struct classifier *left,
	struct classifier *right,
	uint32_t rule_count
) {
	classifier_fini(left, ctx, rule_count);
	classifier_fini(right, ctx, rule_count);
}

// Verifies that a pair spanning both whole dimensions covers every cell
// through its constant term alone, so the classes start at zero.
static void
run_join_any_pair_test(void) {
	struct join_arena arena;
	join_arena_open(&arena, 4 << 20);
	struct classifier left = {0}, right = {0};
	stage_init(&left, &arena.ctx, (uint32_t[]){0, 1}, 2);
	stage_init(&right, &arena.ctx, (uint32_t[]){0, 1}, 2);
	add_group(&left, (uint32_t[]){0, 1, 2, 3}, 4);
	add_group(&left, (uint32_t[]){2}, 1);
	add_group(&right, (uint32_t[]){0, 1, 2, 3, 4}, 5);
	add_group(&right, (uint32_t[]){1}, 1);
	assert(assert_join_matches_reference(
		       &arena.ctx, &left, &right, 2, 0, 0
	       ) == 0);
	stages_fini(&arena.ctx, &left, &right, 2);
	join_arena_close(&arena);
}

// Verifies that a row side holding every row covers its columns through
// the column term of the pair.
static void
run_join_all_rows_test(void) {
	struct join_arena arena;
	join_arena_open(&arena, 4 << 20);
	struct classifier left = {0}, right = {0};
	stage_init(&left, &arena.ctx, (uint32_t[]){0, 1}, 2);
	stage_init(&right, &arena.ctx, (uint32_t[]){0, 1}, 2);
	add_group(&left, (uint32_t[]){0, 1, 2}, 3);
	add_group(&left, (uint32_t[]){1}, 1);
	add_group(&right, (uint32_t[]){1, 3}, 2);
	add_group(&right, (uint32_t[]){0, 2}, 2);
	assert_join_matches_reference(&arena.ctx, &left, &right, 2, 0, 0);
	stages_fini(&arena.ctx, &left, &right, 2);
	join_arena_close(&arena);
}

// Verifies that a column side holding every column covers its rows
// through the row term of the pair.
static void
run_join_all_columns_test(void) {
	struct join_arena arena;
	join_arena_open(&arena, 4 << 20);
	struct classifier left = {0}, right = {0};
	stage_init(&left, &arena.ctx, (uint32_t[]){0, 1}, 2);
	stage_init(&right, &arena.ctx, (uint32_t[]){0, 1}, 2);
	add_group(&left, (uint32_t[]){1, 3}, 2);
	add_group(&left, (uint32_t[]){0, 2}, 2);
	add_group(&right, (uint32_t[]){0, 1, 2}, 3);
	add_group(&right, (uint32_t[]){1}, 1);
	assert_join_matches_reference(&arena.ctx, &left, &right, 2, 0, 0);
	stages_fini(&arena.ctx, &left, &right, 2);
	join_arena_close(&arena);
}

// Verifies that a pair over half of both dimensions paints the product of
// its two complements on top of the constant and both linear terms.
static void
run_join_both_sides_large_test(void) {
	struct join_arena arena;
	join_arena_open(&arena, 4 << 20);
	struct classifier left = {0}, right = {0};
	stage_init(&left, &arena.ctx, (uint32_t[]){0, 1}, 2);
	stage_init(&right, &arena.ctx, (uint32_t[]){0, 1}, 2);
	add_group(&left, (uint32_t[]){4, 0, 2, 1}, 4);
	add_group(&left, (uint32_t[]){5, 3}, 2);
	add_group(&right, (uint32_t[]){1, 2, 3, 5, 6}, 5);
	add_group(&right, (uint32_t[]){0}, 1);
	assert_join_matches_reference(&arena.ctx, &left, &right, 2, 0, 0);
	stages_fini(&arena.ctx, &left, &right, 2);
	join_arena_close(&arena);
}

// Verifies that the uncovered class takes zero even when its first cell
// comes last, and the first covered class takes zero without it.
static void
run_join_uncovered_class_test(void) {
	struct join_arena arena;
	join_arena_open(&arena, 4 << 20);
	struct classifier left = {0}, right = {0};
	stage_init(&left, &arena.ctx, (uint32_t[]){0, 1, 2}, 3);
	stage_init(&right, &arena.ctx, (uint32_t[]){0, 1, 1}, 3);
	add_group(&left, (uint32_t[]){0, 1, 2}, 3);
	add_group(&left, (uint32_t[]){0, 1}, 2);
	add_group(&left, (uint32_t[]){2}, 1);
	add_group(&right, (uint32_t[]){0, 1}, 2);
	add_group(&right, (uint32_t[]){2}, 1);
	// The third row holds the uncovered cell last.
	assert(assert_join_matches_reference(
		       &arena.ctx, &left, &right, 2, 2, 2
	       ) == 0);
	assert(assert_join_matches_reference(
		       &arena.ctx, &left, &right, 2, 0, 0
	       ) == 1);
	// The third rule covers it.
	assert(assert_join_matches_reference(
		       &arena.ctx, &left, &right, 3, 0, 0
	       ) == 0);
	stages_fini(&arena.ctx, &left, &right, 3);
	join_arena_close(&arena);
}

// Verifies that a rule without a group on either side keeps the invalid
// mark and adds no pair.
static void
run_join_invalid_group_test(void) {
	struct join_arena arena;
	join_arena_open(&arena, 4 << 20);
	struct classifier left = {0}, right = {0};
	stage_init(
		&left, &arena.ctx, (uint32_t[]){FILTER_GROUP_INVALID, 0, 1}, 3
	);
	stage_init(
		&right, &arena.ctx, (uint32_t[]){0, FILTER_GROUP_INVALID, 1}, 3
	);
	add_group(&left, (uint32_t[]){0}, 1);
	add_group(&left, (uint32_t[]){1}, 1);
	add_group(&right, (uint32_t[]){0}, 1);
	add_group(&right, (uint32_t[]){1}, 1);
	assert_join_matches_reference(&arena.ctx, &left, &right, 3, 0, 0);
	stages_fini(&arena.ctx, &left, &right, 3);
	join_arena_close(&arena);
}

// Verifies that a single row or column, a whole dimension of one class,
// covers the other side through the linear terms.
static void
run_join_single_line_test(void) {
	struct join_arena arena;
	join_arena_open(&arena, 4 << 20);
	struct classifier line = {0}, wide = {0};
	stage_init(&line, &arena.ctx, (uint32_t[]){0, 0}, 2);
	stage_init(&wide, &arena.ctx, (uint32_t[]){0, 1}, 2);
	add_group(&line, (uint32_t[]){0}, 1);
	add_group(&wide, (uint32_t[]){1, 2}, 2);
	add_group(&wide, (uint32_t[]){2, 3, 5}, 3);
	assert_join_matches_reference(&arena.ctx, &line, &wide, 2, 0, 0);
	assert_join_matches_reference(&arena.ctx, &wide, &line, 2, 0, 0);
	stages_fini(&arena.ctx, &line, &wide, 2);
	join_arena_close(&arena);
}

// Verifies that classes interned before the index grows past its initial
// capacity keep their ids when their cells recur after it.
static void
run_join_class_growth_test(void) {
	struct join_arena arena;
	join_arena_open(&arena, 16 << 20);
	enum { side = 40, rule_count = side * side };
	uint32_t *rule_groups = malloc(2 * rule_count * sizeof(uint32_t));
	assert(rule_groups != NULL);
	for (uint32_t rule = 0; rule < rule_count; ++rule) {
		rule_groups[rule] = rule / side;
		rule_groups[rule_count + rule] = rule % side;
	}
	struct classifier left, right;
	stage_init(&left, &arena.ctx, rule_groups, rule_count);
	stage_init(&right, &arena.ctx, rule_groups + rule_count, rule_count);
	for (uint32_t value = 0; value < side; ++value) {
		add_group(&left, (uint32_t[]){value, value + side}, 2);
		add_group(&right, &value, 1);
	}
	assert(assert_join_matches_reference(
		       &arena.ctx,
		       &left,
		       &right,
		       rule_count,
		       2 * side - 1,
		       side - 1
	       ) == rule_count - 1);
	stages_fini(&arena.ctx, &left, &right, rule_count);
	free(rule_groups);
	join_arena_close(&arena);
}

// Verifies that a pair recovering its classes from the first cells of its
// rows or columns skips the first cells outside its other side.
static void
run_join_class_recovery_test(void) {
	struct join_arena arena;
	join_arena_open(&arena, 4 << 20);
	// The 8 x 8 pair reads the first cells of its rows: row 0 also holds
	// the first cell of a class outside its columns.
	struct classifier left = {0}, right = {0};
	stage_init(&left, &arena.ctx, (uint32_t[]){0, 1, 2, 2}, 4);
	stage_init(&right, &arena.ctx, (uint32_t[]){0, 1, 2, 3}, 4);
	add_group(&left, (uint32_t[]){0, 1, 2, 3, 4, 5, 6, 7}, 8);
	add_group(&left, (uint32_t[]){0}, 1);
	add_group(&left, (uint32_t[]){9}, 1);
	add_group(&right, (uint32_t[]){0, 1, 2, 3, 4, 5, 6, 7}, 8);
	add_group(&right, (uint32_t[]){8, 9}, 2);
	add_group(&right, (uint32_t[]){0, 1, 2, 3}, 4);
	add_group(&right, (uint32_t[]){4, 5, 6, 7}, 4);
	assert_join_matches_reference(&arena.ctx, &left, &right, 4, 0, 0);
	stages_fini(&arena.ctx, &left, &right, 4);
	// The same pair reads the first cells of its columns: column 0 also
	// holds the first cell of a class outside its rows.
	stage_init(&left, &arena.ctx, (uint32_t[]){0, 1, 2, 3}, 4);
	stage_init(&right, &arena.ctx, (uint32_t[]){0, 1, 2, 2}, 4);
	add_group(&left, (uint32_t[]){0, 1, 2, 3, 4, 5, 6, 7}, 8);
	add_group(&left, (uint32_t[]){8, 9}, 2);
	add_group(&left, (uint32_t[]){0, 1, 2, 3}, 4);
	add_group(&left, (uint32_t[]){4, 5, 6, 7}, 4);
	add_group(&right, (uint32_t[]){0, 1, 2, 3, 4, 5, 6, 7}, 8);
	add_group(&right, (uint32_t[]){0}, 1);
	add_group(&right, (uint32_t[]){9}, 1);
	assert_join_matches_reference(&arena.ctx, &left, &right, 4, 0, 0);
	stages_fini(&arena.ctx, &left, &right, 4);
	join_arena_close(&arena);
}

// Verifies that rules sharing a group pair share one pair and one range.
static void
run_join_duplicate_pairs_test(void) {
	struct join_arena arena;
	join_arena_open(&arena, 4 << 20);
	struct classifier left = {0}, right = {0};
	stage_init(&left, &arena.ctx, (uint32_t[]){0, 1, 0, 1}, 4);
	stage_init(&right, &arena.ctx, (uint32_t[]){0, 0, 0, 0}, 4);
	add_group(&left, (uint32_t[]){0, 1}, 2);
	add_group(&left, (uint32_t[]){1, 2}, 2);
	add_group(&right, (uint32_t[]){1}, 1);
	add_group(&right, (uint32_t[]){0, 2}, 2);
	assert_join_matches_reference(&arena.ctx, &left, &right, 4, 0, 0);
	stages_fini(&arena.ctx, &left, &right, 4);
	join_arena_close(&arena);
}

// Verifies that a join failing in its arena leaves the stage zeroed, the
// table released and the arena as it found it.
static void
run_join_failure_cleanup_test(void) {
	struct join_arena regs;
	join_arena_open(&regs, 4 << 20);
	struct join_arena tight;
	join_arena_open(&tight, 8 << 10);
	struct classifier left = {0}, right = {0};
	uint32_t groups_left[64], groups_right[64];
	for (uint32_t i = 0; i < 64; ++i) {
		groups_left[i] = i / 8;
		groups_right[i] = i % 8;
	}
	stage_init(&left, &regs.ctx, groups_left, 64);
	stage_init(&right, &regs.ctx, groups_right, 64);
	// The small table fits; the overlapping output ranges exceed the arena.
	for (uint32_t g = 0; g < 8; ++g) {
		uint32_t values[7], count = 0;
		for (uint32_t v = 0; v < 8; ++v) {
			if (v != g) {
				values[count++] = v;
			}
		}
		add_group(&left, values, count);
		add_group(&right, values, count);
	}
	struct value_table table;
	struct classifier out = {0};
	assert(classify_join(
		       &tight.ctx, "join", &left, &right, 64, &table, &out
	       ) == -1);
	assert(out.rule_groups == NULL);
	assert(ADDR_OF(&out.registry.memory_context) == NULL);
	assert(ADDR_OF(&table.memory_context) == NULL);
	join_arena_close(&tight);
	stages_fini(&regs.ctx, &left, &right, 64);
	join_arena_close(&regs);
}

// A seeded random group of one of the shapes rulesets produce.
//
// The shapes are a single class, an interval, a sparse or a dense
// scatter, the whole dimension and nothing.
static uint32_t
random_group(uint64_t *state, uint32_t dim, uint32_t *values) {
	*state ^= *state << 13;
	*state ^= *state >> 7;
	*state ^= *state << 17;
	uint64_t r = *state;
	uint32_t shape = r % 6;
	uint32_t count = 0;
	uint32_t lo = (r >> 8) % dim;
	uint32_t hi = lo + (r >> 24) % (dim - lo) + 1;
	for (uint32_t value = 0; value < dim; ++value) {
		uint64_t coin = (r >> 32) ^ (value * 0x9e3779b97f4a7c15ULL);
		coin ^= coin >> 29;
		bool hit = shape == 0	? value == lo
			   : shape == 1 ? value >= lo && value < hi
			   : shape == 2 ? coin % 8 == 0
			   : shape == 3 ? coin % 8 != 0
					: shape == 4;
		if (hit) {
			values[count++] = value;
		}
	}
	// Reversed, so a range does not arrive sorted.
	for (uint32_t idx = 0; idx < count / 2; ++idx) {
		uint32_t tmp = values[idx];
		values[idx] = values[count - 1 - idx];
		values[count - 1 - idx] = tmp;
	}
	return count;
}

// Verifies that the join matches the reference over seeded random stages
// of nested, wide, sparse and empty groups, duplicate and invalid pairs.
static void
run_join_random_equivalence_test(void) {
	struct join_arena arena;
	join_arena_open(&arena, 64 << 20);
	uint64_t state = 0x2545f4914f6cdd1dULL;
	uint32_t values[320];
	for (uint32_t round = 0; round < 2000; ++round) {
		uint32_t rule_count =
			(uint32_t)(state % (round % 16 ? 24 : 160));
		uint32_t dims[2] = {
			1 + (uint32_t)(state >> 8) % (round % 4 ? 24 : 300),
			1 + (uint32_t)(state >> 20) % (round % 3 ? 24 : 300),
		};
		uint32_t group_counts[2] = {
			1 + (uint32_t)(state >> 32) % (round % 16 ? 6 : 32),
			1 + (uint32_t)(state >> 40) % (round % 16 ? 6 : 32),
		};
		struct classifier stages[2];
		for (uint32_t side = 0; side < 2; ++side) {
			uint32_t rule_groups[160];
			for (uint32_t rule = 0; rule < rule_count; ++rule) {
				uint32_t pick =
					(uint32_t)(state >> (rule % 48));
				rule_groups[rule] =
					pick % 11 == 0
						? FILTER_GROUP_INVALID
						: pick % group_counts[side];
				state = state * 6364136223846793005ULL + 1;
			}
			stage_init(
				stages + side,
				&arena.ctx,
				rule_groups,
				rule_count
			);
			for (uint32_t g = 0; g < group_counts[side]; ++g) {
				uint32_t count = random_group(
					&state, dims[side], values
				);
				add_group(stages + side, values, count);
			}
		}
		assert_join_matches_reference(
			&arena.ctx, stages, stages + 1, rule_count, 0, 0
		);
		stages_fini(&arena.ctx, stages, stages + 1, rule_count);
	}
	join_arena_close(&arena);
}

int
main(void) {
	run_first_match_projection_test();
	run_duplicate_rows_fit_test();
	run_uncovered_classes_test();
	run_failed_join_cleanup_test();
	run_join_any_pair_test();
	run_join_all_rows_test();
	run_join_all_columns_test();
	run_join_both_sides_large_test();
	run_join_uncovered_class_test();
	run_join_invalid_group_test();
	run_join_single_line_test();
	run_join_class_growth_test();
	run_join_class_recovery_test();
	run_join_duplicate_pairs_test();
	run_join_failure_cleanup_test();
	run_join_random_equivalence_test();
	puts("first-match/projection, duplicate rows, failed-build cleanup "
	     "and class join against the reference: PASS");
	return 0;
}
