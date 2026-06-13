#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/logging/log.h"

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define BIG_ALIGN (1u << 21)
#define RAW_ALLOC_SZ ((size_t)(1u << 22))

// Count the number of children reachable from ctx via first_child/next_sibling.
static size_t
helper_count_children(struct memory_context *ctx) {
	size_t count = 0;
	struct memory_context *child = ADDR_OF(&ctx->first_child);
	while (child != NULL) {
		++count;
		child = ADDR_OF(&child->next_sibling);
	}
	return count;
}

// Return 1 if needle appears in parent's child list, 0 otherwise.
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

// Verifies that a freshly initialised root context has no parent and no
// children.
static int
test_root_links_null(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	struct memory_context root;
	TEST_ASSERT(
		memory_context_init(&root, "root", &ba) == 0, "init failed"
	);

	TEST_ASSERT(ADDR_OF(&root.parent) == NULL, "root parent must be NULL");
	TEST_ASSERT(
		ADDR_OF(&root.first_child) == NULL,
		"root first_child must be NULL"
	);
	TEST_ASSERT(
		ADDR_OF(&root.next_sibling) == NULL,
		"root next_sibling must be NULL"
	);

	memory_context_fini(&root);
	return 0;
}

// Verifies that children link in LIFO order, the last inserted child being the
// head.
static int
test_head_insertion_order(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	struct memory_context root, a, b, c;
	TEST_ASSERT(
		memory_context_init(&root, "root", &ba) == 0, "root init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&a, &root, "a") == 0, "a init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&b, &root, "b") == 0, "b init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&c, &root, "c") == 0, "c init failed"
	);

	// Inserted a, b, c => head is c (LIFO order).
	TEST_ASSERT(
		ADDR_OF(&root.first_child) == &c,
		"first_child must be &c after inserting a, b, c"
	);
	TEST_ASSERT(
		ADDR_OF(&c.next_sibling) == &b, "c.next_sibling must be &b"
	);
	TEST_ASSERT(
		ADDR_OF(&b.next_sibling) == &a, "b.next_sibling must be &a"
	);
	TEST_ASSERT(
		ADDR_OF(&a.next_sibling) == NULL, "a.next_sibling must be NULL"
	);
	TEST_ASSERT(helper_count_children(&root) == 3, "child count must be 3");

	memory_context_fini(&a);
	memory_context_fini(&b);
	memory_context_fini(&c);
	memory_context_fini(&root);
	return 0;
}

// Verifies that init_from builds correct parent and first_child links at every
// depth.
static int
test_nested_depth(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	struct memory_context agent, cp_module, filter, lpm, big_array;
	TEST_ASSERT(
		memory_context_init(&agent, "agent", &ba) == 0,
		"agent init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&cp_module, &agent, "cp_module") == 0,
		"cp_module init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&filter, &cp_module, "filter") == 0,
		"filter init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&lpm, &filter, "lpm") == 0,
		"lpm init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&big_array, &lpm, "big_array") == 0,
		"big_array init failed"
	);

	TEST_ASSERT(
		ADDR_OF(&cp_module.parent) == &agent,
		"cp_module.parent must be &agent"
	);
	TEST_ASSERT(
		ADDR_OF(&filter.parent) == &cp_module,
		"filter.parent must be &cp_module"
	);
	TEST_ASSERT(
		ADDR_OF(&lpm.parent) == &filter, "lpm.parent must be &filter"
	);
	TEST_ASSERT(
		ADDR_OF(&big_array.parent) == &lpm,
		"big_array.parent must be &lpm"
	);

	TEST_ASSERT(
		ADDR_OF(&agent.first_child) == &cp_module,
		"agent.first_child must be &cp_module"
	);
	TEST_ASSERT(
		ADDR_OF(&cp_module.first_child) == &filter,
		"cp_module.first_child must be &filter"
	);
	TEST_ASSERT(
		ADDR_OF(&filter.first_child) == &lpm,
		"filter.first_child must be &lpm"
	);
	TEST_ASSERT(
		ADDR_OF(&lpm.first_child) == &big_array,
		"lpm.first_child must be &big_array"
	);

	memory_context_fini(&big_array);
	memory_context_fini(&lpm);
	memory_context_fini(&filter);
	memory_context_fini(&cp_module);
	memory_context_fini(&agent);
	return 0;
}

// Verifies that finalising a middle sibling unlinks only it and keeps the rest
// of the chain intact.
static int
test_fini_middle_sibling(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	// Insert a, b, c => list becomes c -> b -> a.
	struct memory_context root, a, b, c;
	TEST_ASSERT(
		memory_context_init(&root, "root", &ba) == 0, "root init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&a, &root, "a") == 0, "a init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&b, &root, "b") == 0, "b init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&c, &root, "c") == 0, "c init failed"
	);

	// Remove b from the middle.
	memory_context_fini(&b);

	// b must not be reachable from root.
	TEST_ASSERT(
		!helper_child_is_linked(&root, &b),
		"b must not be in child list"
	);

	// a and c must still be reachable.
	TEST_ASSERT(
		helper_child_is_linked(&root, &a),
		"a must still be in child list"
	);
	TEST_ASSERT(
		helper_child_is_linked(&root, &c),
		"c must still be in child list"
	);

	// Exactly 2 children remain.
	TEST_ASSERT(
		helper_count_children(&root) == 2,
		"child count must be 2 after removing b"
	);

	// Chain integrity: c is still head, c.next_sibling == a, a.next_sibling
	// == NULL.
	TEST_ASSERT(
		ADDR_OF(&root.first_child) == &c,
		"first_child must be &c after fini(&b)"
	);
	TEST_ASSERT(
		ADDR_OF(&c.next_sibling) == &a,
		"c.next_sibling must be &a after fini(&b)"
	);
	TEST_ASSERT(
		ADDR_OF(&a.next_sibling) == NULL,
		"a.next_sibling must be NULL after fini(&b)"
	);

	// After memset, b's parent link is zero, so ADDR_OF returns NULL.
	TEST_ASSERT(
		ADDR_OF(&b.parent) == NULL,
		"b.parent must be NULL after fini (struct is zeroed)"
	);

	memory_context_fini(&a);
	memory_context_fini(&c);
	memory_context_fini(&root);
	return 0;
}

// Verifies that calling fini twice on the same context is safe and leaves the
// parent childless.
static int
test_fini_idempotent(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	struct memory_context root, child;
	TEST_ASSERT(
		memory_context_init(&root, "root", &ba) == 0, "root init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&child, &root, "child") == 0,
		"child init failed"
	);

	memory_context_fini(&child);
	// Second call on an already-zeroed struct must be a no-op.
	memory_context_fini(&child);

	TEST_ASSERT(
		ADDR_OF(&root.first_child) == NULL,
		"root must have no children after double fini"
	);

	memory_context_fini(&root);
	return 0;
}

// Verifies that finalising a root context with no parent is a harmless no-op.
static int
test_fini_root_noop(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	struct memory_context root;
	TEST_ASSERT(
		memory_context_init(&root, "root", &ba) == 0, "root init failed"
	);

	// Fini on a root context (NULL parent) must not crash.
	memory_context_fini(&root);

	// The struct is zeroed; the NULL offsets decode back to NULL.
	TEST_ASSERT(
		ADDR_OF(&root.parent) == NULL,
		"root.parent must be NULL post-fini"
	);
	TEST_ASSERT(
		ADDR_OF(&root.first_child) == NULL,
		"root.first_child must be NULL post-fini"
	);
	return 0;
}

// Verifies that finalising a parent before its child detaches the child so the
// child's later fini is safe.
static int
test_fini_parent_before_child(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	// root -> parent -> child (three-level chain).
	struct memory_context root, parent, child;
	TEST_ASSERT(
		memory_context_init(&root, "root", &ba) == 0, "root init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&parent, &root, "parent") == 0,
		"parent init failed"
	);
	TEST_ASSERT(
		memory_context_init_from(&child, &parent, "child") == 0,
		"child init failed"
	);

	// Finalise parent first (out-of-order teardown).
	memory_context_fini(&parent);

	// Child must be detached: its parent link must be NULL.
	TEST_ASSERT(
		ADDR_OF(&child.parent) == NULL,
		"child.parent must be NULL after parent fini"
	);

	// Child must also have its next_sibling cleared by the detach.
	TEST_ASSERT(
		ADDR_OF(&child.next_sibling) == NULL,
		"child.next_sibling must be NULL after parent fini"
	);

	// Root must have no children: parent was unlinked from root's child
	// list.
	TEST_ASSERT(
		helper_count_children(&root) == 0,
		"root must have 0 children after parent fini"
	);
	TEST_ASSERT(
		!helper_child_is_linked(&root, &parent),
		"parent must not be in root child list after fini"
	);

	// Subsequent fini on the now-detached child must be a harmless no-op.
	memory_context_fini(&child);

	// Root's child list must remain empty and intact.
	TEST_ASSERT(
		helper_count_children(&root) == 0,
		"root must still have 0 children after child fini"
	);

	memory_context_fini(&root);
	return 0;
}

// Verifies that repeated init_from and fini on one parent always returns to an
// empty child list.
static int
test_stress_init_fini_cycle(void) {
	struct block_allocator ba;
	TEST_ASSERT(
		block_allocator_init(&ba) == 0, "block_allocator_init failed"
	);

	struct memory_context root;
	TEST_ASSERT(
		memory_context_init(&root, "stress-root", &ba) == 0,
		"root init failed"
	);

	for (int iter = 0; iter < 100; ++iter) {
		struct memory_context child;
		TEST_ASSERT(
			memory_context_init_from(&child, &root, "child") == 0,
			"child init failed at iter %d",
			iter
		);
		TEST_ASSERT(
			helper_count_children(&root) == 1,
			"child count must be 1 after init at iter %d",
			iter
		);

		memory_context_fini(&child);
		TEST_ASSERT(
			helper_count_children(&root) == 0,
			"child count must be 0 after fini at iter %d",
			iter
		);
	}

	memory_context_fini(&root);
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
	if (test_fini_parent_before_child() != 0) {
		LOG(ERROR, "test_fini_parent_before_child failed");
		return -1;
	}
	if (test_stress_init_fini_cycle() != 0) {
		LOG(ERROR, "test_stress_init_fini_cycle failed");
		return -1;
	}

	LOG(INFO, "memory_context_tree tests: OK");
	return 0;
}
