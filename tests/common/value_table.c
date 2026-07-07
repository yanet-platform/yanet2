#include "common/value.h"

#include <assert.h>
#include <stdio.h>

// value_table_free must be a no-op on an object whose value_table_init
// failed, e.g. when the backing allocator cannot satisfy the values array.
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

	res = value_table_init(&table, &mem_ctx, 1, 10);
	assert(res == -1);

	value_table_free(&table);
}

int
main() {
	test_free_after_failed_init();

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

	struct value_table table;
	int res = value_table_init(&table, &mem_ctx, 1, 10);
	assert(res == 0);

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
	memory_context_fini(&mem_ctx);
	free(arena0);

	puts("OK");

	return 0;
}
