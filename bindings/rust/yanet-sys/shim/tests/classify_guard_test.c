// Verifies that the classify lookups of the SDK's shim stay inside the object
// they index, whatever class they are given.
//
// These lookups are the reference the SDK's Rust lookups are tested against,
// over classes past each end too, as safe Rust takes classes from the caller:
// a class beyond a joint, a rule table or a rule map reads class zero or no
// rule, never memory past the object. In-range classes read what the library
// itself reads.
#include "classify_shim.h"

#include "common/value.h"
#include "lib/classify/compiler/declare.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/query.h"
#include "lib/classify/rule_table.h"

#include <assert.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define RULE_COUNT 2
#define ARENA_SIZE (4 << 20)

// Appends one group of the given classes to a stage's registry.
static void
add_group(struct classifier *stage, const uint32_t *values, size_t count) {
	assert(value_registry_start(&stage->registry) == 0);
	for (size_t idx = 0; idx < count; ++idx) {
		assert(value_registry_collect(&stage->registry, values[idx]) ==
		       0);
	}
}

// Fills a stage of two rules over classes 0 and 1: rule 0 matches class 0,
// rule 1 both classes.
static void
fill_stage(
	struct classifier *stage, struct memory_context *ctx, const char *name
) {
	assert(classifier_init(stage, ctx, RULE_COUNT) == 0);
	assert(value_registry_init(&stage->registry, ctx, name) == 0);
	add_group(stage, (uint32_t[]){0}, 1);
	add_group(stage, (uint32_t[]){0, 1}, 2);
	memcpy(stage->rule_groups,
	       (uint32_t[]){0, 1},
	       RULE_COUNT * sizeof(uint32_t));
}

static void
run_lookups_stay_inside_test(void) {
	void *memory;
	assert(posix_memalign(&memory, 1 << 21, ARENA_SIZE) == 0);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, ARENA_SIZE);
	struct memory_context ctx;
	memory_context_init(&ctx, "guard", &allocator);
	size_t initial = block_allocator_free_size(&allocator);

	struct classifier left = {0}, right = {0}, joined = {0};
	fill_stage(&left, &ctx, "left");
	fill_stage(&right, &ctx, "right");
	struct classifier_rule dummy = {};
	const struct classifier_rule *rules[RULE_COUNT] = {&dummy, &dummy};

	struct value_table joint = {0};
	assert(classify_join(
		       &ctx, "joint", &left, &right, RULE_COUNT, &joint, &joined
	       ) == 0);
	struct rule_table table = {0};
	assert(classify_join_rules(
		       &ctx, "table", &left, &right, rules, RULE_COUNT, &table
	       ) == 0);
	struct vline map = {0};
	assert(classify_decode(&ctx, "map", &left, rules, RULE_COUNT, &map) == 0
	);

	// In range, the shim reads what the library reads.
	for (uint32_t v = 0; v < joint.v_dim; ++v) {
		for (uint32_t h = 0; h < joint.h_dim; ++h) {
			uint32_t class;
			yanet_sys_classify_joint_lookup(
				&joint, &v, &h, &class, 1
			);
			assert(class == value_table_get(&joint, v, h));
		}
	}
	for (uint32_t v = 0; v < table.v_dim; ++v) {
		for (uint32_t h = 0; h < table.h_dim; ++h) {
			uint32_t rule;
			yanet_sys_classify_rule_table_lookup(
				&table, &v, &h, &rule, 1
			);
			assert(rule == rule_table_get(&table, v, h));
		}
	}
	for (uint32_t class = 0; class < map.size; ++class) {
		uint32_t rule;
		yanet_sys_classify_resolve(&map, &class, &rule, 1);
		assert(rule == vline_get(&map, class));
	}

	// Beyond the object, on either side, class zero or no rule.
	const uint32_t beyond[] = {joint.v_dim, joint.h_dim, UINT32_MAX};
	for (size_t idx = 0; idx < sizeof(beyond) / sizeof(beyond[0]); ++idx) {
		uint32_t in = 0, out = beyond[idx];
		uint32_t class, rule;
		yanet_sys_classify_joint_lookup(&joint, &out, &in, &class, 1);
		assert(class == 0);
		yanet_sys_classify_joint_lookup(&joint, &in, &out, &class, 1);
		assert(class == 0);
		yanet_sys_classify_rule_table_lookup(
			&table, &out, &in, &rule, 1
		);
		assert(rule == YANET_SYS_CLASSIFY_NO_RULE);
		yanet_sys_classify_rule_table_lookup(
			&table, &in, &out, &rule, 1
		);
		assert(rule == YANET_SYS_CLASSIFY_NO_RULE);
	}
	const uint32_t past_map[] = {map.size, UINT32_MAX};
	for (size_t idx = 0; idx < sizeof(past_map) / sizeof(past_map[0]);
	     ++idx) {
		uint32_t rule;
		yanet_sys_classify_resolve(&map, &past_map[idx], &rule, 1);
		assert(rule == YANET_SYS_CLASSIFY_NO_RULE);
	}

	// An empty map and table, as a build writes them from no rules, hold
	// no rule.
	struct vline empty_map = {0};
	struct rule_table empty_table = {0};
	uint32_t zero = 0, rule;
	yanet_sys_classify_resolve(&empty_map, &zero, &rule, 1);
	assert(rule == YANET_SYS_CLASSIFY_NO_RULE);
	yanet_sys_classify_rule_table_lookup(
		&empty_table, &zero, &zero, &rule, 1
	);
	assert(rule == YANET_SYS_CLASSIFY_NO_RULE);

	yanet_sys_classify_rule_map_free(&map);
	yanet_sys_classify_rule_table_free(&table);
	yanet_sys_classify_joint_free(&joint);
	yanet_sys_classify_stage_free(&joined, &ctx, RULE_COUNT);
	yanet_sys_classify_stage_free(&left, &ctx, RULE_COUNT);
	yanet_sys_classify_stage_free(&right, &ctx, RULE_COUNT);
	assert(block_allocator_free_size(&allocator) == initial);
	memory_context_fini(&ctx);
	free(memory);
}

int
main(void) {
	run_lookups_stay_inside_test();
	return 0;
}
