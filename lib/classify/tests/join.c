// Verifies the class joins of two stages and the immutable first-match
// joins of the final rule tables.
//
// The class join must meet its definition cell by cell. Explicit expected
// indices pin projection isolation and priority; a tight arena pins the
// duplicate-row canonicalization and the cleanup of failed builds.
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

extern uint64_t join_test_interned_cells;

// The pair sets of the cells being ordered, a byte per pair.
static const uint8_t *cell_sets;
static uint32_t cell_set_size;

// Orders the cells by their pair sets, then by position.
static int
cell_cmp(const void *a, const void *b) {
	uint32_t x = *(const uint32_t *)a;
	uint32_t y = *(const uint32_t *)b;
	int rc =
		memcmp(cell_sets + (size_t)x * cell_set_size,
		       cell_sets + (size_t)y * cell_set_size,
		       cell_set_size);
	return rc ? rc : (x > y) - (x < y);
}

// Joins the stages and asserts the join of its definition.
//
// The pairs are the distinct group pairs in rule order. A cell's class is
// the set of pairs covering it, numbered by first appearance in row-major
// order. A pair's range lists its classes ascending, only the cells unlike
// both neighbours are interned, and the coverage map marks exactly the
// covered classes.
static void
assert_join(
	struct memory_context *ctx,
	const struct classifier *left,
	const struct classifier *right,
	uint32_t rule_count
) {
	struct value_table table;
	struct classifier out = {0};
	join_test_interned_cells = 0;
	assert(classify_join(
		       ctx, "join", left, right, rule_count, &table, &out
	       ) == 0);

	uint32_t *pair_rules = malloc((rule_count + 1) * sizeof(uint32_t));
	assert(pair_rules != NULL);
	uint32_t pair_count = 0;
	for (uint32_t rule = 0; rule < rule_count; ++rule) {
		uint32_t group1 = left->rule_groups[rule];
		uint32_t group2 = right->rule_groups[rule];
		uint32_t pair = 0;
		while (pair < pair_count &&
		       (left->rule_groups[pair_rules[pair]] != group1 ||
			right->rule_groups[pair_rules[pair]] != group2)) {
			++pair;
		}
		if (group1 == FILTER_GROUP_INVALID ||
		    group2 == FILTER_GROUP_INVALID) {
			pair = FILTER_GROUP_INVALID;
		} else if (pair == pair_count) {
			pair_rules[pair_count++] = rule;
		}
		assert(out.rule_groups[rule] == pair);
	}
	assert(out.registry.range_count == pair_count);

	uint32_t v_dim = value_registry_capacity(&left->registry);
	uint32_t h_dim = value_registry_capacity(&right->registry);
	assert(table.v_dim == v_dim && table.h_dim == h_dim);
	uint32_t cell_count = v_dim * h_dim;
	uint8_t *sets = calloc((size_t)cell_count * pair_count + 1, 1);
	assert(sets != NULL);
	const struct value_range *ranges1 = ADDR_OF(&left->registry.ranges);
	const struct value_range *ranges2 = ADDR_OF(&right->registry.ranges);
	for (uint32_t pair = 0; pair < pair_count; ++pair) {
		const struct value_range *rows =
			ranges1 + left->rule_groups[pair_rules[pair]];
		const struct value_range *cols =
			ranges2 + right->rule_groups[pair_rules[pair]];
		const uint32_t *row_values = ADDR_OF(&rows->values);
		const uint32_t *col_values = ADDR_OF(&cols->values);
		for (uint64_t i = 0; i < rows->count; ++i) {
			for (uint64_t j = 0; j < cols->count; ++j) {
				size_t cell = (size_t)row_values[i] * h_dim +
					      col_values[j];
				sets[cell * pair_count + pair] = 1;
			}
		}
	}

	// The first cell of every set; the class of a cell is its set's.
	uint32_t *order = malloc(cell_count * sizeof(uint32_t));
	uint32_t *first = malloc(cell_count * sizeof(uint32_t));
	uint32_t *ids = malloc(cell_count * sizeof(uint32_t));
	assert(order != NULL && first != NULL && ids != NULL);
	for (uint32_t cell = 0; cell < cell_count; ++cell) {
		order[cell] = cell;
	}
	cell_sets = sets;
	cell_set_size = pair_count;
	qsort(order, cell_count, sizeof(uint32_t), cell_cmp);
	for (uint32_t idx = 0; idx < cell_count; ++idx) {
		uint32_t cell = order[idx];
		uint32_t prev = idx ? order[idx - 1] : cell;
		first[cell] = idx && !memcmp(sets + (size_t)prev * pair_count,
					     sets + (size_t)cell * pair_count,
					     pair_count)
				      ? first[prev]
				      : cell;
	}
	struct vline map;
	assert(classify_coverage_map(ctx, &out, rule_count, NULL, &map) == 0);
	uint32_t next = 0;
	uint64_t unlike = 0;
	for (uint32_t cell = 0; cell < cell_count; ++cell) {
		uint32_t v = cell / h_dim, h = cell % h_dim;
		ids[cell] = first[cell] == cell ? next++ : ids[first[cell]];
		assert(value_table_get(&table, v, h) == ids[cell]);
		const uint8_t *set = sets + (size_t)cell * pair_count;
		bool covered = memchr(set, 1, pair_count) != NULL;
		assert((vline_get(&map, ids[cell]) != 0) == covered);
		unlike += (h == 0 || ids[cell - 1] != ids[cell]) &&
			  (v == 0 || ids[cell - h_dim] != ids[cell]);
	}
	assert(value_registry_capacity(&out.registry) == next);
	assert(join_test_interned_cells == unlike);

	const struct value_range *ranges = ADDR_OF(&out.registry.ranges);
	for (uint32_t pair = 0; pair < pair_count; ++pair) {
		const uint32_t *values = ADDR_OF(&ranges[pair].values);
		uint64_t count = 0;
		for (uint32_t cell = 0; cell < cell_count; ++cell) {
			if (first[cell] == cell &&
			    sets[(size_t)cell * pair_count + pair]) {
				assert(count < ranges[pair].count);
				assert(values[count++] == ids[cell]);
			}
		}
		assert(count == ranges[pair].count);
	}

	free(ids);
	free(first);
	free(order);
	free(sets);
	free(pair_rules);
	vline_free(&map);
	value_table_free(&table);
	classifier_fini(&out, ctx, rule_count);
}

// Verifies that the coverage map gives class zero and a later class with
// the same rules one id, and leaves an uncovered class at zero.
static void
run_coverage_map_test(void) {
	struct join_arena arena;
	join_arena_open(&arena, 1 << 20);
	struct classifier stage = {0};
	stage_init(&stage, &arena.ctx, (uint32_t[]){0}, 1);
	add_group(&stage, (uint32_t[]){0, 2}, 2);
	struct vline map;
	assert(classify_coverage_map(&arena.ctx, &stage, 1, NULL, &map) == 0);
	assert(vline_get(&map, 0) != 0);
	assert(vline_get(&map, 2) == vline_get(&map, 0));
	assert(vline_get(&map, 1) == 0);
	vline_free(&map);
	classifier_fini(&stage, &arena.ctx, 1);
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
	// Rule g covers every class but g on both sides: the small table fits,
	// the overlapping output ranges exceed the arena.
	uint32_t ring[48];
	for (uint32_t i = 0; i < 48; ++i) {
		ring[i] = i % 24;
	}
	struct classifier stage = {0};
	stage_init(&stage, &regs.ctx, ring, 24);
	for (uint32_t g = 0; g < 24; ++g) {
		add_group(&stage, ring + g + 1, 23);
	}
	struct value_table table;
	struct classifier out = {0};
	assert(classify_join(
		       &tight.ctx, "join", &stage, &stage, 24, &table, &out
	       ) == -1);
	assert(out.rule_groups == NULL);
	assert(ADDR_OF(&out.registry.memory_context) == NULL);
	assert(ADDR_OF(&table.memory_context) == NULL);
	join_arena_close(&tight);
	classifier_fini(&stage, &regs.ctx, 24);
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
	// Descending, so a range does not arrive sorted.
	for (uint32_t value = dim; value-- > 0;) {
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
	return count;
}

// Verifies that the join meets its definition over seeded random stages
// of nested, wide, sparse and empty groups, duplicate and invalid pairs.
static void
run_join_random_test(void) {
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
		assert_join(&arena.ctx, stages, stages + 1, rule_count);
		classifier_fini(stages, &arena.ctx, rule_count);
		classifier_fini(stages + 1, &arena.ctx, rule_count);
	}
	join_arena_close(&arena);
}

int
main(void) {
	run_first_match_projection_test();
	run_duplicate_rows_fit_test();
	run_uncovered_classes_test();
	run_failed_join_cleanup_test();
	run_coverage_map_test();
	run_join_failure_cleanup_test();
	run_join_random_test();
	puts("first-match/projection, duplicate rows, failed-build cleanup "
	     "and class join: PASS");
	return 0;
}
