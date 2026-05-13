#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/logging/log.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static size_t
helper_count_children(struct memory_context *ctx) {
	size_t n = 0;
	struct memory_context *child = ADDR_OF(&ctx->first_child);
	while (child != NULL) {
		++n;
		child = ADDR_OF(&child->next_sibling);
	}
	return n;
}

// Returns 1 when needle is reachable in the child chain of parent.
static int
helper_child_is_linked(
	struct memory_context *parent, struct memory_context *needle
) {
	struct memory_context *child = ADDR_OF(&parent->first_child);
	while (child != NULL) {
		if (child == needle) {
			return 1;
		}
		child = ADDR_OF(&child->next_sibling);
	}
	return 0;
}

static int
helper_make_arena(void **raw_out, struct block_allocator *ba) {
	// 2 MiB, large enough for all test structures.
	const size_t raw_size = (1u << 21) + (1u << 21);
	void *raw = malloc(raw_size);
	if (raw == NULL) {
		return -1;
	}

	// Align to 2 MiB boundary inside the allocation.
	uintptr_t p = (uintptr_t)raw;
	uintptr_t aligned = (p + (1u << 21) - 1) & ~((uintptr_t)(1u << 21) - 1);
	block_allocator_init(ba);
	block_allocator_put_arena(ba, (void *)aligned, 1u << 21);
	*raw_out = raw;
	return 0;
}

// NOTE: root context has all tree links NULL.
static int
test_root_links_null(void) {
	void *raw = NULL;
	struct block_allocator ba;
	TEST_ASSERT(helper_make_arena(&raw, &ba) == 0, "arena setup failed");

	struct memory_context root;
	memory_context_init(&root, "root", &ba);

	TEST_ASSERT(
		ADDR_OF(&root.parent) == NULL,
		"root parent must be NULL after init"
	);
	TEST_ASSERT(
		ADDR_OF(&root.first_child) == NULL,
		"root first_child must be NULL after init"
	);
	TEST_ASSERT(
		ADDR_OF(&root.next_sibling) == NULL,
		"root next_sibling must be NULL after init"
	);

	free(raw);
	return 0;
}

// NOTE: head-insertion order — first_child is the last added child, and
// next_sibling walks back in insertion order.
static int
test_head_insertion_order(void) {
	void *raw = NULL;
	struct block_allocator ba;
	TEST_ASSERT(helper_make_arena(&raw, &ba) == 0, "arena setup failed");

	struct memory_context root;
	memory_context_init(&root, "root", &ba);

	struct memory_context a, b, c;
	memory_context_init_from(&a, &root, "a");
	memory_context_init_from(&b, &root, "b");
	memory_context_init_from(&c, &root, "c");

	// After inserting a, b, c in order, first_child should be c (last
	// added) and the chain should be c -> b -> a -> NULL.
	struct memory_context *head = ADDR_OF(&root.first_child);
	TEST_ASSERT(
		head == &c, "first_child should be the last inserted child"
	);
	TEST_ASSERT(
		ADDR_OF(&c.next_sibling) == &b, "c.next_sibling should be b"
	);
	TEST_ASSERT(
		ADDR_OF(&b.next_sibling) == &a, "b.next_sibling should be a"
	);
	TEST_ASSERT(
		ADDR_OF(&a.next_sibling) == NULL,
		"a.next_sibling should be NULL"
	);
	TEST_ASSERT(
		helper_count_children(&root) == 3, "root should have 3 children"
	);

	free(raw);
	return 0;
}

// NOTE: nested init_from at multiple depths.
static int
test_nested_depth(void) {
	void *raw = NULL;
	struct block_allocator ba;
	TEST_ASSERT(helper_make_arena(&raw, &ba) == 0, "arena setup failed");

	// Simulate: agent -> cp_module -> filter -> lpm -> big_array
	struct memory_context agent, cp_mod, filter, lpm, big_array;

	memory_context_init(&agent, "agent", &ba);
	memory_context_init_from(&cp_mod, &agent, "cp_module");
	memory_context_init_from(&filter, &cp_mod, "filter");
	memory_context_init_from(&lpm, &filter, "lpm");
	memory_context_init_from(&big_array, &lpm, "big_array");

	// Verify parent pointers at each level.
	TEST_ASSERT(ADDR_OF(&agent.parent) == NULL, "agent has no parent");
	TEST_ASSERT(
		ADDR_OF(&cp_mod.parent) == &agent,
		"cp_mod parent should be agent"
	);
	TEST_ASSERT(
		ADDR_OF(&filter.parent) == &cp_mod,
		"filter parent should be cp_mod"
	);
	TEST_ASSERT(
		ADDR_OF(&lpm.parent) == &filter, "lpm parent should be filter"
	);
	TEST_ASSERT(
		ADDR_OF(&big_array.parent) == &lpm,
		"big_array parent should be lpm"
	);

	// Verify linkage at each depth.
	TEST_ASSERT(
		ADDR_OF(&agent.first_child) == &cp_mod,
		"agent.first_child should be cp_mod"
	);
	TEST_ASSERT(
		ADDR_OF(&cp_mod.first_child) == &filter,
		"cp_mod.first_child should be filter"
	);
	TEST_ASSERT(
		ADDR_OF(&filter.first_child) == &lpm,
		"filter.first_child should be lpm"
	);
	TEST_ASSERT(
		ADDR_OF(&lpm.first_child) == &big_array,
		"lpm.first_child should be big_array"
	);

	free(raw);
	return 0;
}

// NOTE: fini of a middle sibling leaves the chain intact for the others.
static int
test_fini_middle_sibling(void) {
	void *raw = NULL;
	struct block_allocator ba;
	TEST_ASSERT(helper_make_arena(&raw, &ba) == 0, "arena setup failed");

	struct memory_context root;
	memory_context_init(&root, "root", &ba);

	struct memory_context a, b, c;
	memory_context_init_from(&a, &root, "a");
	memory_context_init_from(&b, &root, "b");
	memory_context_init_from(&c, &root, "c");

	// Chain is c -> b -> a. Remove b (the middle one).
	memory_context_fini(&b);

	// b must no longer be reachable.
	TEST_ASSERT(
		!helper_child_is_linked(&root, &b),
		"b must not be reachable after fini"
	);
	// a and c must still be reachable.
	TEST_ASSERT(
		helper_child_is_linked(&root, &a),
		"a must remain reachable after fini"
	);
	TEST_ASSERT(
		helper_child_is_linked(&root, &c),
		"c must remain reachable after fini"
	);
	TEST_ASSERT(
		helper_count_children(&root) == 2, "root should have 2 children"
	);

	// Chain should now be c -> a -> NULL.
	TEST_ASSERT(
		ADDR_OF(&root.first_child) == &c,
		"first_child should still be c"
	);
	TEST_ASSERT(
		ADDR_OF(&c.next_sibling) == &a,
		"c.next_sibling should now be a (b was removed)"
	);
	TEST_ASSERT(
		ADDR_OF(&a.next_sibling) == NULL,
		"a.next_sibling should be NULL"
	);

	// b's parent should be NULL after fini.
	TEST_ASSERT(
		ADDR_OF(&b.parent) == NULL, "b.parent must be NULL after fini"
	);

	free(raw);
	return 0;
}

// NOTE: fini is idempotent — calling it twice must not crash or corrupt.
static int
test_fini_idempotent(void) {
	void *raw = NULL;
	struct block_allocator ba;
	TEST_ASSERT(helper_make_arena(&raw, &ba) == 0, "arena setup failed");

	struct memory_context root;
	memory_context_init(&root, "root", &ba);

	struct memory_context child;
	memory_context_init_from(&child, &root, "child");

	memory_context_fini(&child);
	// Second call must not corrupt root.first_child or crash.
	memory_context_fini(&child);

	TEST_ASSERT(
		ADDR_OF(&root.first_child) == NULL,
		"root should have no children after double fini"
	);
	TEST_ASSERT(helper_count_children(&root) == 0, "child count must be 0");

	free(raw);
	return 0;
}

// NOTE: fini of a root context (parent == NULL) is a no-op.
static int
test_fini_root_noop(void) {
	void *raw = NULL;
	struct block_allocator ba;
	TEST_ASSERT(helper_make_arena(&raw, &ba) == 0, "arena setup failed");

	struct memory_context root;
	memory_context_init(&root, "root", &ba);

	// Must not crash or modify anything.
	memory_context_fini(&root);

	TEST_ASSERT(
		ADDR_OF(&root.parent) == NULL,
		"root parent still NULL after fini"
	);
	TEST_ASSERT(
		ADDR_OF(&root.first_child) == NULL,
		"root first_child still NULL after fini"
	);

	free(raw);
	return 0;
}

// NOTE: stress — 100 cycles of init_from/fini on a single parent leave
// an empty child list.
static int
test_stress_init_fini_cycle(void) {
	void *raw = NULL;
	struct block_allocator ba;
	TEST_ASSERT(helper_make_arena(&raw, &ba) == 0, "arena setup failed");

	struct memory_context root;
	memory_context_init(&root, "root", &ba);

	struct memory_context child;

	for (int cycle = 0; cycle < 100; ++cycle) {
		memory_context_init_from(&child, &root, "child");
		TEST_ASSERT(
			helper_child_is_linked(&root, &child),
			"child must be linked after init_from (cycle %d)",
			cycle
		);
		memory_context_fini(&child);
		TEST_ASSERT(
			!helper_child_is_linked(&root, &child),
			"child must be unlinked after fini (cycle %d)",
			cycle
		);
		TEST_ASSERT(
			helper_count_children(&root) == 0,
			"root must have 0 children after fini (cycle %d)",
			cycle
		);
	}

	free(raw);
	return 0;
}

int
main(void) {
	log_enable_name("info");

	if (test_root_links_null() != 0) {
		LOG(ERROR, "test_root_links_null failed");
		return -1;
	}
	if (test_head_insertion_order() != 0) {
		LOG(ERROR, "test_head_insertion_order failed");
		return -1;
	}
	if (test_nested_depth() != 0) {
		LOG(ERROR, "test_nested_depth failed");
		return -1;
	}
	if (test_fini_middle_sibling() != 0) {
		LOG(ERROR, "test_fini_middle_sibling failed");
		return -1;
	}
	if (test_fini_idempotent() != 0) {
		LOG(ERROR, "test_fini_idempotent failed");
		return -1;
	}
	if (test_fini_root_noop() != 0) {
		LOG(ERROR, "test_fini_root_noop failed");
		return -1;
	}
	if (test_stress_init_fini_cycle() != 0) {
		LOG(ERROR, "test_stress_init_fini_cycle failed");
		return -1;
	}

	LOG(INFO, "memory_context tree tests: OK");
	return 0;
}
