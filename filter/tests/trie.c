#include "filter/trie.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/registry.h"
#include "filter/helper.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>

////////////////////////////////////////////////////////////////////////////////

#define MEMORY (1 << 24)

////////////////////////////////////////////////////////////////////////////////

void
test1(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, MEMORY);

	struct memory_context mctx;
	int ret = memory_context_init(&mctx, "test", &allocator);
	assert(ret == 0);

	// init trie
	struct trie trie;
	ret = trie_init(&trie, &mctx);
	assert(ret == 0);

	// add rules in trie

	const uint8_t v1[] = {0x10, 0x20, 0xff, 0xff};
	ret = trie_add(&trie, v1, 16, 0);
	assert(ret == 0);

	const uint8_t v2[] = {0x10, 0x20, 0xff, 0xff};
	ret = trie_add(&trie, v2, 20, 1);
	assert(ret == 0);

	const uint8_t v3[] = {0x10, 0x20, 0xee, 0xee};
	ret = trie_add(&trie, v3, 20, 2);
	assert(ret == 0);

	// build classifiers
	trie_build_classifiers(&trie);

	// make requests
	const uint8_t r1[] = {0x10, 0x20, 0xff, 0xff}; // only 1 and 2
	uint32_t c1 = trie_classify(&trie, r1, 4);

	const uint8_t r2[] = {0x10, 0x20, 0xee, 0}; // only 1 and 3
	uint32_t c2 = trie_classify(&trie, r2, 4);

	const uint8_t r3[] = {0x10, 0x20, 0, 0}; // only 1
	uint32_t c3 = trie_classify(&trie, r3, 4);

	const uint8_t r4[] = {0x11, 0x20, 0, 0}; // nothing
	uint32_t c4 = trie_classify(&trie, r4, 4);

	assert(c1 != c2);
	assert(c1 != c3);
	assert(c2 != c3);
	assert(c4 != c1);
	assert(c4 != c2);
	assert(c4 != c3);

	// collect rule classifiers in registry
	struct value_registry registry;
	ret = value_registry_init(&registry, &mctx);
	assert(ret == 0);

	ret = fill_rule_registry_by_trie(&trie, 3, &registry, &mctx);
	assert(ret == 0);

	assert(registry.range_count == 3);
	uint32_t ref_range_counts[] = {3, 1, 1};

	for (uint64_t i = 0; i < registry.range_count; ++i) {
		struct value_range range = ADDR_OF(&registry.ranges)[i];
		assert(range.count == ref_range_counts[i]);
	}

	// free registry
	value_registry_free(&registry);

	// free trie
	trie_free_mem(&trie);
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	void *memory = malloc(MEMORY);

	puts("test1...");
	test1(memory);

	free(memory);

	puts("OK");
	return 0;
}