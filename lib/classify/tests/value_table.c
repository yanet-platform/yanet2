// Unit test of the value table sparse form.
//
// The join outputs above the dense budget store only the touched
// cells (see value_table_init_auto), and no in-tree ruleset of the
// unit suite reaches that budget - so this target builds its tables
// with the threshold lowered (see meson.build) and checks the sparse
// machinery against a dense table driven through the identical touch
// and compact sequence: after compaction every cell of the two forms
// must read back the same value, the untouched cells the compacted
// zero, and the raw insert path must survive growth and updates.

#include "common/value.h"

#include "common/memory.h"

#include <assert.h>
#include <stdint.h>
#include <stdlib.h>

// Wide enough that the insert phase crosses the load bound of the
// initial hash capacity (one chunk of 2^16 slots at load 0.7; the
// first grow lands at 45875 distinct cells and the capacity keeps
// doubling from there) and exercises the rehash; the target's
// threshold override keeps the sparse form despite the small
// rectangle.
#define TEST_V_DIM 1024
#define TEST_H_DIM 256
#define TEST_INSERTS (TEST_V_DIM * TEST_H_DIM)

static uint32_t reference[TEST_V_DIM][TEST_H_DIM];

int
main(void) {
	void *memory = malloc(1 << 25);
	assert(memory != NULL);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 25);
	struct memory_context mctx;
	assert(memory_context_init(&mctx, "test", &allocator) == 0);

	struct value_table sparse;
	assert(value_table_init_auto(
		       &sparse, &mctx, "test:sparse", TEST_V_DIM, TEST_H_DIM
	       ) == 0);
	assert(sparse.sparse);

	// The raw insert path: enough distinct cells to cross two grow
	// steps, updates of already present keys, and the (0,0) cell
	// whose key encoding collides with the empty marker. A host side
	// array replays the same writes as the oracle.
	for (uint32_t idx = 0; idx < TEST_INSERTS; ++idx) {
		uint32_t v = idx / TEST_H_DIM;
		uint32_t h = idx % TEST_H_DIM;
		assert(value_table_sparse_set(&sparse, v, h, idx + 1) == 0);
		assert(value_table_sparse_set(&sparse, 0, 0, 0x1234) == 0);
		reference[v][h] = idx + 1;
	}
	reference[0][0] = 0x1234;

	// The growth really happened: every cell of the rectangle is
	// present and the capacity left the initial chunk behind, so the
	// rehash path ran more than once.
	assert(sparse.sparse_count == TEST_INSERTS);
	assert(sparse.sparse_cap >= 4 * VALUE_TABLE_SPARSE_CHUNK_SIZE);

	for (uint32_t v = 0; v < TEST_V_DIM; ++v) {
		for (uint32_t h = 0; h < TEST_H_DIM; ++h) {
			assert(value_table_sparse_get(&sparse, v, h) ==
			       reference[v][h]);
		}
	}

	// The production flow: generations of touches through the remap
	// machinery on both forms, then one compaction, then the forms
	// must agree on every cell.
	struct value_table sparse2;
	assert(value_table_init_auto(
		       &sparse2, &mctx, "test:sparse2", TEST_V_DIM, TEST_H_DIM
	       ) == 0);
	struct value_table dense2;
	assert(value_table_init(
		       &dense2, &mctx, "test:dense2", TEST_V_DIM, TEST_H_DIM
	       ) == 0);
	assert(!dense2.sparse);

	struct remap_table remap;
	assert(remap_table_init(&remap, &mctx, 1 << 12) == 0);

	for (uint32_t gen = 0; gen < 64; ++gen) {
		remap_table_new_gen(&remap);
		for (uint32_t idx = 0; idx < 48; ++idx) {
			uint32_t v = (gen * 11 + idx * 5) % TEST_V_DIM;
			uint32_t h = (gen * 7 + idx * 3) % TEST_H_DIM;
			assert(value_table_touch(&sparse2, v, h, &remap) == 0);
			assert(value_table_touch(&dense2, v, h, &remap) == 0);
		}
	}

	remap_table_compact(&remap);
	uint32_t empty = remap_table_compacted(&remap, 0);
	value_table_compact(&sparse2, &remap);
	value_table_compact(&dense2, &remap);

	assert(sparse2.empty_value == empty);
	for (uint32_t v = 0; v < TEST_V_DIM; ++v) {
		for (uint32_t h = 0; h < TEST_H_DIM; ++h) {
			assert(value_table_get(&sparse2, v, h) ==
			       value_table_get(&dense2, v, h));
		}
	}

	// A sparse table never touched reads the compacted zero of its
	// own (zero filled) remap everywhere, the (0,0) cell included.
	struct value_table virgin;
	assert(value_table_init_auto(
		       &virgin, &mctx, "test:virgin", TEST_V_DIM, TEST_H_DIM
	       ) == 0);
	struct remap_table virgin_remap;
	assert(remap_table_init(&virgin_remap, &mctx, 16) == 0);
	remap_table_compact(&virgin_remap);
	uint32_t virgin_empty = remap_table_compacted(&virgin_remap, 0);
	value_table_compact(&virgin, &virgin_remap);
	assert(value_table_get(&virgin, 0, 0) == virgin_empty);
	assert(value_table_get(&virgin, 1, 1) == virgin_empty);
	remap_table_free(&virgin_remap);

	value_table_free(&virgin);
	value_table_free(&sparse2);
	value_table_free(&dense2);
	value_table_free(&sparse);
	remap_table_free(&remap);

	memory_context_fini(&mctx);
	block_allocator_fini(&allocator);
	free(memory);
	return 0;
}
