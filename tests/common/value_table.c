#include "common/value.h"

#include <assert.h>
#include <stdio.h>

int
main() {
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
	free(arena0);

	puts("OK");

	return 0;
}
