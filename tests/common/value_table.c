#include "common/value.h"

#include <assert.h>
#include <stdio.h>

// value_table_free must be a no-op on an object whose value_table_init
// failed, e.g. when the backing allocator cannot satisfy any allocation.
// Before the fix the values field was left uninitialized and the subsequent
// free dereferenced it.
static void
test_free_after_failed_init(void) {
	struct block_allocator alloc;
	block_allocator_init(&alloc);

	struct memory_context mem_ctx;
	int res = memory_context_init(&mem_ctx, "fail", &alloc);
	assert(res == 0);

	struct value_table table;
	memset(&table, 0xa5, sizeof(table));

	res = value_table_init(&table, &mem_ctx, "test-table", 1, 10);
	assert(res == -1);

	value_table_free(&table);

	// A failed init must never leave a child memory context attached to
	// the parent.
	assert(ADDR_OF(&mem_ctx.first_child) == NULL);
}

// Verifies that value_table_init failing after it has already created its
// child memory context (arena runs out on a later allocation) still leaves
// no child attached to the parent.
static void
test_partial_failure_no_child(void) {
	// Big enough for the context struct and the tiny values array (a
	// single chunk pointer for these dims), too small for the first
	// 64KB values chunk.
	void *arena = malloc(1 << 12);
	assert(arena != NULL);

	struct block_allocator alloc;
	block_allocator_init(&alloc);
	block_allocator_put_arena(&alloc, arena, 1 << 12);

	struct memory_context mem_ctx;
	int res = memory_context_init(&mem_ctx, "partial-fail", &alloc);
	assert(res == 0);

	struct value_table table;
	res = value_table_init(&table, &mem_ctx, "test-table", 1, 10);
	assert(res == -1);

	assert(ADDR_OF(&mem_ctx.first_child) == NULL);
	assert(mem_ctx.balloc_size == mem_ctx.bfree_size);

	free(arena);
}

int
main() {
	test_free_after_failed_init();
	test_partial_failure_no_child();

	void *arena0 = malloc(1 << 24); // 16MB
	if (arena0 == NULL) {
		return 1;
	}

	struct block_allocator alloc;
	block_allocator_init(&alloc);
	block_allocator_put_arena(&alloc, arena0, 1 << 24);

	struct memory_context mem_ctx;
	if (memory_context_init(&mem_ctx, "test", &alloc) < 0) {
		return 1;
	}

	// Baseline to compare against once the table is freed below: nothing
	// but the table (and the remap table) is allocated from mem_ctx.
	size_t balloc_size_before = mem_ctx.balloc_size;
	size_t bfree_size_before = mem_ctx.bfree_size;

	struct value_table table;
	int res = value_table_init(&table, &mem_ctx, "test-table", 1, 10);
	assert(res == 0);

	// The table gets its own child node, named after the call site, in
	// mem_ctx's memory tree.
	struct memory_context *child = ADDR_OF(&mem_ctx.first_child);
	assert(child != NULL);
	assert(strcmp(child->name, "test-table") == 0);
	assert(ADDR_OF(&child->next_sibling) == NULL);

	struct remap_table remap_table;
	res = remap_table_init(&remap_table, &mem_ctx, 10);
	assert(res == 0);

	uint32_t l[5] = {2, 3, 0, 8, 6};
	uint32_t r[5] = {5, 7, 4, 9, 10};

	uint32_t mask[10];
	memset(mask, 0, sizeof(mask));

	for (size_t i = 0; i < 5; ++i) {
		remap_table_new_gen(&remap_table);
		for (size_t x = l[i]; x < r[i]; ++x) {
			mask[x] |= 1 << i;
			uint32_t *value = value_table_get_ptr(&table, 0, x);
			remap_table_touch(&remap_table, *value, value);
		}
	}

	remap_table_compact(&remap_table);
	value_table_compact(&table, &remap_table);

	for (size_t i = 0; i < 10; ++i) {
		for (size_t j = i + 1; j < 10; ++j) {
			int res = (mask[i] == mask[j]) ^
				  (value_table_get(&table, 0, i) ==
				   value_table_get(&table, 0, j));
			assert(res == 0);
		}
	}

	remap_table_free(&remap_table);
	value_table_free(&table);

	// The child node and every byte it (and its own context struct) used
	// must be gone once the table is freed.
	assert(ADDR_OF(&mem_ctx.first_child) == NULL);
	assert(mem_ctx.balloc_size - mem_ctx.bfree_size ==
	       balloc_size_before - bfree_size_before);

	memory_context_fini(&mem_ctx);
	free(arena0);

	puts("OK");

	return 0;
}
