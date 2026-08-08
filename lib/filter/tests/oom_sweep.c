// Allocation-failure injection sweep over the production filter compiler.
//
// A plain undersized-arena test does not reach the bug class this guards
// against: the block allocator is a buddy allocator, so failure is monotone
// in arena size and every unchecked memory_balloc site in the compiler
// happens to be followed by a checked, larger one. The bug needs a
// non-monotone failure, which is what a concurrent compile produces in
// production and what single-point fault injection models here: force
// exactly one memory_balloc call to fail, for every call the compile
// makes, and require a clean -1 every time.
#include "common/asan.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/network.h"
#include "common/test_assert.h"

#include "lib/filter/compiler.h"
#include "lib/filter/filter.h"

#include "lib/filter/tests/helpers.h"

#include "logging/log.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

FILTER_COMPILER_DECLARE(sign_net6_src_compile, net6_src);
FILTER_COMPILER_DECLARE(sign_net4_src_compile, net4_src);
FILTER_COMPILER_DECLARE(sign_ports_compile, port_src, port_dst);
FILTER_COMPILER_DECLARE(sign_net4_ports_compile, net4_src, port_src, port_dst);

// Storage for the extern injector state declared in oom_shim.h.
int filter_test_oom_armed;
long filter_test_oom_fail_at = -1;
long filter_test_oom_calls;

#define SWEEP_RULE_COUNT 16
#define SWEEP_ARENA_SIZE (1 << 24)

static struct net6
sweep_mk_net6(uint32_t idx, uint8_t prefix_len) {
	struct net6 net = {0};
	// Spreads the significant bits across both 64-bit halves so both
	// the hi and lo range indices grow with idx.
	net.addr[0] = 0x20;
	net.addr[1] = 0x01;
	net.addr[2] = (uint8_t)(idx >> 8);
	net.addr[3] = (uint8_t)idx;
	net.addr[8] = (uint8_t)(idx >> 4);
	net.addr[9] = (uint8_t)(idx << 3);
	for (uint8_t byte_idx = 0; byte_idx < 16; ++byte_idx) {
		uint8_t bits = prefix_len > byte_idx * 8
				       ? prefix_len - byte_idx * 8
				       : 0;
		if (bits > 8) {
			bits = 8;
		}
		net.mask[byte_idx] = (uint8_t)(bits ? (0xff << (8 - bits)) : 0);
	}
	return net;
}

static void
build_net6_src_rules(
	struct filter_rule_builder *builders,
	struct filter_rule *rules,
	const struct filter_rule **rule_ptrs,
	uint32_t rule_count
) {
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		builder_init(&builders[idx]);
		builder_add_net6_src(
			&builders[idx], sweep_mk_net6(idx, 64 + (idx % 8))
		);
		builder_add_net6_src(
			&builders[idx],
			sweep_mk_net6(idx + 1000, 48 + (idx % 16))
		);
		rules[idx] = build_rule(&builders[idx]);
		rule_ptrs[idx] = &rules[idx];
	}
}

static void
build_net4_src_rules(
	struct filter_rule_builder *builders,
	struct filter_rule *rules,
	const struct filter_rule **rule_ptrs,
	uint32_t rule_count
) {
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		builder_init(&builders[idx]);
		uint8_t addr[NET4_LEN] = {
			10, (uint8_t)(idx >> 8), (uint8_t)idx, 0
		};
		uint8_t mask[NET4_LEN] = {0xff, 0xff, 0xff, 0x00};
		builder_add_net4_src(&builders[idx], addr, mask);
		rules[idx] = build_rule(&builders[idx]);
		rule_ptrs[idx] = &rules[idx];
	}
}

static void
build_ports_rules(
	struct filter_rule_builder *builders,
	struct filter_rule *rules,
	const struct filter_rule **rule_ptrs,
	uint32_t rule_count
) {
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		builder_init(&builders[idx]);
		builder_add_port_src_range(
			&builders[idx],
			(uint16_t)(idx * 3),
			(uint16_t)(idx * 3 + 100)
		);
		builder_add_port_dst_range(
			&builders[idx],
			(uint16_t)(idx * 7),
			(uint16_t)(idx * 7 + 50)
		);
		rules[idx] = build_rule(&builders[idx]);
		rule_ptrs[idx] = &rules[idx];
	}
}

// Three lookups are the threshold that puts filter_init's merge loop on
// merge_and_collect_registry, so this is the sweep's only shape reaching
// merge_registry_values and collect_registry_values in helper.c.
// Its collect_registry_values unwind is swept, but merge_registry_values'
// error_join is not: reaching it needs remap_table_touch to fail, which
// first allocates only past 4096 remap keys, where 16 rules reach 101.
static void
build_net4_ports_rules(
	struct filter_rule_builder *builders,
	struct filter_rule *rules,
	const struct filter_rule **rule_ptrs,
	uint32_t rule_count
) {
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		builder_init(&builders[idx]);
		uint8_t addr[NET4_LEN] = {
			10, (uint8_t)(idx >> 8), (uint8_t)idx, 0
		};
		uint8_t mask[NET4_LEN] = {0xff, 0xff, 0xff, 0x00};
		builder_add_net4_src(&builders[idx], addr, mask);
		builder_add_port_src_range(
			&builders[idx],
			(uint16_t)(idx * 3),
			(uint16_t)(idx * 3 + 100)
		);
		builder_add_port_dst_range(
			&builders[idx],
			(uint16_t)(idx * 7),
			(uint16_t)(idx * 7 + 50)
		);
		rules[idx] = build_rule(&builders[idx]);
		rule_ptrs[idx] = &rules[idx];
	}
}

// Rebuilds the arena, block allocator and memory context from scratch so
// every sweep iteration starts from the same state a fresh config apply
// would see.
static int
sweep_reset_fixture(
	uint8_t *arena,
	size_t arena_size,
	struct block_allocator *allocator,
	struct memory_context *mctx
) {
	// The block allocator leaves parts of the arena ASan-poisoned from
	// the previous iteration's compile, so the region has to be
	// reclaimed before the fill below can touch it.
	asan_unpoison_memory_region(arena, arena_size);

	// A production agent arena is recycled, not freshly zeroed on every
	// config apply. Poisoning it before every iteration reproduces the
	// stale bytes a use that skips a check would actually read, instead
	// of the misleadingly benign zero fill malloc happens to give.
	memset(arena, 0x5a, arena_size);

	block_allocator_init(allocator);
	block_allocator_put_arena(allocator, arena, arena_size);

	int res = memory_context_init(mctx, "oom_sweep", allocator);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize memory context");

	return TEST_SUCCESS;
}

// Compiles one filter with the injector armed at fail_at (-1 disables
// failure and just counts) and returns filter_init's return code.
static int
sweep_compile(
	const struct filter_compiler *compiler,
	const struct filter_rule **rule_ptrs,
	uint32_t rule_count,
	struct memory_context *mctx,
	long fail_at,
	struct filter *filter
) {
	filter_test_oom_calls = 0;
	filter_test_oom_fail_at = fail_at;
	filter_test_oom_armed = 1;

	int rc = filter_init(
		filter, compiler, rule_ptrs, rule_count, mctx, NULL
	);

	filter_test_oom_armed = 0;
	filter_test_oom_fail_at = -1;

	return rc;
}

typedef void (*sweep_build_func)(
	struct filter_rule_builder *builders,
	struct filter_rule *rules,
	const struct filter_rule **rule_ptrs,
	uint32_t rule_count
);

// Same checks as TEST_ASSERT_SUCCESS/TEST_ASSERT_EQUAL, but jump to the
// sweep's single cleanup exit instead of returning directly, so the arena
// allocated further up in sweep_signature is always freed.
#define SWEEP_ASSERT_SUCCESS(value, msg, ...)                                  \
	do {                                                                   \
		if ((value) != TEST_SUCCESS) {                                 \
			LOG(ERROR, "ASSERT FAILED: " msg, ##__VA_ARGS__);      \
			goto out;                                              \
		}                                                              \
	} while (0)

#define SWEEP_ASSERT_EQUAL(a, b, msg, ...)                                     \
	do {                                                                   \
		if ((a) != (b)) {                                              \
			LOG(ERROR,                                             \
			    "ASSERT FAILED: " msg                              \
			    " (expected: %ld, got: %ld)",                      \
			    ##__VA_ARGS__,                                     \
			    (long)(b),                                         \
			    (long)(a));                                        \
			goto out;                                              \
		}                                                              \
	} while (0)

// Verifies that injecting a single allocation failure at every call the
// compiler makes, for one attribute signature, always leaves filter_init
// reporting a clean -1 and never crashes.
static int
sweep_signature(
	const char *name,
	const struct filter_compiler *compiler,
	sweep_build_func build
) {
	static struct filter_rule_builder builders[SWEEP_RULE_COUNT];
	static struct filter_rule rules[SWEEP_RULE_COUNT];
	static const struct filter_rule *rule_ptrs[SWEEP_RULE_COUNT];
	build(builders, rules, rule_ptrs, SWEEP_RULE_COUNT);

	uint8_t *arena = malloc(SWEEP_ARENA_SIZE);
	TEST_ASSERT_NOT_NULL(
		arena, "%s: failed to allocate the sweep arena", name
	);

	int result = TEST_FAILED;

	struct block_allocator allocator;
	struct memory_context mctx;
	SWEEP_ASSERT_SUCCESS(
		sweep_reset_fixture(arena, SWEEP_ARENA_SIZE, &allocator, &mctx),
		"%s: failed to reset the sweep fixture",
		name
	);

	struct filter filter;
	int rc = sweep_compile(
		compiler, rule_ptrs, SWEEP_RULE_COUNT, &mctx, -1, &filter
	);
	SWEEP_ASSERT_EQUAL(rc, 0, "%s: clean compile failed", name);
	long total_calls = filter_test_oom_calls;
	filter_free(&filter, compiler);

	LOG(INFO, "%s: sweeping %ld allocation points", name, total_calls);

	for (long fail_at = 0; fail_at < total_calls; ++fail_at) {
		SWEEP_ASSERT_SUCCESS(
			sweep_reset_fixture(
				arena, SWEEP_ARENA_SIZE, &allocator, &mctx
			),
			"%s: failed to reset the sweep fixture for call %ld",
			name,
			fail_at
		);
		size_t free_before = block_allocator_free_size(&allocator);

		struct filter failed_filter;
		int frc = sweep_compile(
			compiler,
			rule_ptrs,
			SWEEP_RULE_COUNT,
			&mctx,
			fail_at,
			&failed_filter
		);
		SWEEP_ASSERT_EQUAL(
			frc,
			-1,
			"%s: injecting a failure at call %ld did not produce "
			"a clean OOM",
			name,
			fail_at
		);
		SWEEP_ASSERT_EQUAL(
			block_allocator_free_size(&allocator),
			free_before,
			"%s: injecting a failure at call %ld leaked arena "
			"bytes",
			name,
			fail_at
		);
	}

	result = TEST_SUCCESS;

out:
	// The last iteration leaves the arena ASan-poisoned; the allocator
	// requires manually poisoned memory to be unpoisoned before free.
	asan_unpoison_memory_region(arena, SWEEP_ARENA_SIZE);
	free(arena);

	return result;
}

int
main(void) {
	log_enable_name("info");

	size_t tests = 0;
	size_t failed = 0;

	++tests;
	if (sweep_signature(
		    "net6-src", sign_net6_src_compile, build_net6_src_rules
	    )) {
		LOG(ERROR, "net6-src sweep failed");
		++failed;
	}

	++tests;
	if (sweep_signature(
		    "net4-src", sign_net4_src_compile, build_net4_src_rules
	    )) {
		LOG(ERROR, "net4-src sweep failed");
		++failed;
	}

	++tests;
	if (sweep_signature("ports", sign_ports_compile, build_ports_rules)) {
		LOG(ERROR, "ports sweep failed");
		++failed;
	}

	++tests;
	if (sweep_signature(
		    "net4-ports",
		    sign_net4_ports_compile,
		    build_net4_ports_rules
	    )) {
		LOG(ERROR, "net4-ports sweep failed");
		++failed;
	}

	if (failed == 0) {
		LOG(INFO, "All %zu OOM sweeps passed", tests);
	} else {
		LOG(ERROR, "%zu/%zu OOM sweeps failed", failed, tests);
	}

	return failed == 0 ? 0 : 1;
}
