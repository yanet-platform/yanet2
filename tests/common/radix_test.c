#include "common/radix.h"

#include <assert.h>
#include <inttypes.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>

struct radix_iterate_ctx {
	uint8_t keys[4][10];
	uint32_t values[10];
	uint32_t count;
};

int
radix_iterate_cb(
	uint8_t key_size, const uint8_t *key, uint32_t value, void *data
) {
	assert(key_size == 4);
	struct radix_iterate_ctx *ctx = (struct radix_iterate_ctx *)data;
	memcpy(ctx->keys[ctx->count], key, 4);
	ctx->values[ctx->count] = value;
	++ctx->count;
	return 0;
}

// The bytes of one chunk of pages.
#define CHUNK_BYTES (sizeof(radix_page_t) * RADIX_CHUNK_SIZE)
// The bytes of a directory of the given number of entries.
#define DIRECTORY_BYTES(entries) ((entries) * sizeof(radix_page_t *))

// Writes the 3-byte key numbered by its two leading bytes.
static void
three_byte_key(uint32_t idx, uint8_t key[3]) {
	key[0] = (uint8_t)(idx >> 8);
	key[1] = (uint8_t)idx;
	key[2] = 0;
}

/*
 * Maps each of the count 3-byte keys from the first one to its own number.
 *
 * A key adds a page for its two leading bytes, and a page for its first
 * byte when no earlier key shares it.
 */
static int
insert_keys(struct radix *tree, uint32_t first, uint32_t count) {
	for (uint32_t idx = first; idx < first + count; ++idx) {
		uint8_t key[3];
		three_byte_key(idx, key);
		if (radix_insert(tree, 3, key, idx)) {
			return -1;
		}
	}
	return 0;
}

// Whether each 3-byte key 0..count-1 looks up to its own number.
static bool
keys_hold(const struct radix *tree, uint32_t count) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		uint8_t key[3];
		three_byte_key(idx, key);
		if (radix_lookup(tree, 3, key) != idx) {
			fprintf(stdout, "key %u lost\n", idx);
			return false;
		}
	}
	return true;
}

// The bytes the context holds allocated.
static size_t
live_bytes(const struct memory_context *mctx) {
	return mctx->balloc_size - mctx->bfree_size;
}

// 620 keys build a page each, three pages for their first bytes and the
// root: 624 pages in 39 chunks.
#define GROWTH_KEYS 620
#define GROWTH_PAGES 624
#define GROWTH_CHUNKS 39
// The directory capacity at 39 chunks: the next power of two.
#define GROWTH_DIRECTORY_ENTRIES 64
_Static_assert(
	GROWTH_PAGES == GROWTH_CHUNKS * RADIX_CHUNK_SIZE,
	"the growth pages fill whole chunks"
);

/*
 * verifies that the directory grows past several power-of-two chunk counts
 * keeping every key, and the free returns every allocated byte.
 *
 * At 39 chunks the directory holds 64 entries, so the free must return it
 * at that capacity for the freed bytes to equal the allocated ones.
 */
static int
run_directory_growth_test(void) {
	// Twice the chunks for the sanitizer red zones, twice for headroom.
	size_t arena_size = 4 * GROWTH_CHUNKS * CHUNK_BYTES;
	void *arena = malloc(arena_size);
	if (arena == NULL) {
		return -1;
	}
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, arena_size);
	struct memory_context mctx;
	memory_context_init(&mctx, "radix_growth", &ba);

	int rc = -1;
	struct radix tree;
	if (radix_init(&tree, &mctx)) {
		fprintf(stdout, "could not initialize radix\n");
		goto out;
	}
	if (insert_keys(&tree, 0, GROWTH_KEYS)) {
		fprintf(stdout, "insert failed\n");
		goto release;
	}
	if (tree.page_count != GROWTH_PAGES) {
		fprintf(stdout, "%" PRIu64 " pages built\n", tree.page_count);
		goto release;
	}
	if (!keys_hold(&tree, GROWTH_KEYS)) {
		goto release;
	}
	size_t expected = GROWTH_CHUNKS * CHUNK_BYTES +
			  DIRECTORY_BYTES(GROWTH_DIRECTORY_ENTRIES);
	if (live_bytes(&mctx) != expected) {
		fprintf(stdout,
			"%zu bytes live, expected %zu\n",
			live_bytes(&mctx),
			expected);
		goto release;
	}
	// Directories of 1, 2, 4, ... 64 entries are allocated in turn and each
	// but the last is copied into the next, so 63 offsets are copied for
	// 39 chunks.
	size_t allocated = mctx.balloc_size - GROWTH_CHUNKS * CHUNK_BYTES;
	if (allocated != DIRECTORY_BYTES(1 + 2 + 4 + 8 + 16 + 32 + 64)) {
		fprintf(stdout, "%zu directory bytes allocated\n", allocated);
		goto release;
	}
	rc = 0;

release:
	radix_free(&tree);
	if (rc == 0 && mctx.balloc_size != mctx.bfree_size) {
		fprintf(stdout,
			"%zu bytes allocated, %zu freed\n",
			mctx.balloc_size,
			mctx.bfree_size);
		rc = -1;
	}
out:
	memory_context_fini(&mctx);
	free(arena);
	return rc;
}

#define OOM_ARENA_SIZE (1 << 20)

// The most blocks an exhausted test arena is split into.
#define FILLERS_MAX 512

// The blocks taken to exhaust an allocator.
struct fillers {
	void *blocks[FILLERS_MAX];
	size_t sizes[FILLERS_MAX];
	size_t count;
};

// Takes every free block, the largest first, so any allocation then fails.
static int
exhaust(struct block_allocator *ba, struct fillers *fillers) {
	for (size_t size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE; size > 0;
	     size >>= 1) {
		void *block;
		while ((block = block_allocator_balloc(ba, size)) != NULL) {
			if (fillers->count == FILLERS_MAX) {
				return -1;
			}
			fillers->blocks[fillers->count] = block;
			fillers->sizes[fillers->count++] = size;
		}
	}
	return 0;
}

/*
 * verifies that a page refused at a directory reallocation, for want of
 * the chunk or the directory, leaves the tree unchanged and leaks nothing.
 *
 * The tree is built to four full chunks, a power of two, so the next page
 * needs a chunk and then a directory of eight entries. With the arena
 * exhausted the chunk fails; with only a block of its size released the
 * directory fails and the chunk is released. The tree then grows once
 * memory is added.
 */
static int
run_directory_growth_out_of_memory_test(void) {
	void *arena0 = malloc(OOM_ARENA_SIZE);
	void *arena1 = malloc(OOM_ARENA_SIZE);
	struct fillers fillers = {0};
	// The block reserved for the chunk until the second attempt.
	void *chunk_block = NULL;
	if (arena0 == NULL || arena1 == NULL) {
		free(arena0);
		free(arena1);
		return -1;
	}
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena0, OOM_ARENA_SIZE);
	size_t free_size = block_allocator_free_size(&ba);
	struct memory_context mctx;
	memory_context_init(&mctx, "radix_growth_oom", &ba);

	int rc = -1;
	struct radix tree;
	if (radix_init(&tree, &mctx)) {
		fprintf(stdout, "could not initialize radix\n");
		goto out;
	}
	// 62 keys build a page each, one page for their first byte and the
	// root: 64 pages in four full chunks.
	uint32_t count = 62;
	if (insert_keys(&tree, 0, count)) {
		fprintf(stdout, "insert failed\n");
		goto release;
	}
	if (tree.page_count != 4 * RADIX_CHUNK_SIZE) {
		fprintf(stdout, "%" PRIu64 " pages built\n", tree.page_count);
		goto release;
	}

	chunk_block = block_allocator_balloc(&ba, CHUNK_BYTES);
	if (chunk_block == NULL || exhaust(&ba, &fillers)) {
		fprintf(stdout, "could not exhaust the arena\n");
		goto release;
	}
	radix_page_t **directory = ADDR_OF(&tree.pages);
	size_t live = live_bytes(&mctx);
	for (int attempt = 0; attempt < 2; ++attempt) {
		// The second attempt has room for the chunk only, so it
		// allocates the chunk and frees it when the directory fails.
		if (attempt == 1) {
			block_allocator_bfree(&ba, chunk_block, CHUNK_BYTES);
			chunk_block = NULL;
		}
		size_t ballocs = mctx.balloc_count + attempt;
		size_t bfrees = mctx.bfree_count + attempt;
		size_t balloc_size = mctx.balloc_size + attempt * CHUNK_BYTES;
		if (radix_new_page(&tree, NULL) != -1) {
			fprintf(stdout, "attempt %d: page allocated\n", attempt
			);
			goto release;
		}
		if (tree.page_count != 4 * RADIX_CHUNK_SIZE ||
		    ADDR_OF(&tree.pages) != directory ||
		    live_bytes(&mctx) != live || !keys_hold(&tree, count)) {
			fprintf(stdout, "attempt %d: tree changed\n", attempt);
			goto release;
		}
		if (mctx.balloc_count != ballocs ||
		    mctx.bfree_count != bfrees ||
		    mctx.balloc_size != balloc_size) {
			fprintf(stdout,
				"attempt %d: unexpected allocations\n",
				attempt);
			goto release;
		}
	}

	size_t exhausted_size = block_allocator_free_size(&ba);
	block_allocator_put_arena(&ba, arena1, OOM_ARENA_SIZE);
	free_size += block_allocator_free_size(&ba) - exhausted_size;
	if (insert_keys(&tree, count, 1)) {
		fprintf(stdout, "insert failed after the arena expansion\n");
		goto release;
	}
	++count;
	// Five chunks under a directory of eight entries.
	if (tree.page_count != 4 * RADIX_CHUNK_SIZE + 1 ||
	    !keys_hold(&tree, count) ||
	    live_bytes(&mctx) != 5 * CHUNK_BYTES + DIRECTORY_BYTES(8)) {
		fprintf(stdout, "no growth after the arena expansion\n");
		goto release;
	}
	rc = 0;

release:
	radix_free(&tree);
	if (chunk_block != NULL) {
		block_allocator_bfree(&ba, chunk_block, CHUNK_BYTES);
	}
	for (size_t idx = 0; idx < fillers.count; ++idx) {
		block_allocator_bfree(
			&ba, fillers.blocks[idx], fillers.sizes[idx]
		);
	}
	if (rc == 0 && (mctx.balloc_size != mctx.bfree_size ||
			block_allocator_free_size(&ba) != free_size)) {
		fprintf(stdout, "the free leaked\n");
		rc = -1;
	}
out:
	memory_context_fini(&mctx);
	free(arena1);
	free(arena0);
	return rc;
}

int
main() {
	if (run_directory_growth_test()) {
		fprintf(stdout, "run_directory_growth_test: FAILED\n");
		return 1;
	}

	if (run_directory_growth_out_of_memory_test()) {
		fprintf(stdout,
			"run_directory_growth_out_of_memory_test: FAILED\n");
		return 1;
	}

	void *arena = malloc(1 << 24); // 16MB
	if (arena == NULL) {
		return 1;
	}

	struct block_allocator alloc;
	block_allocator_init(&alloc);
	block_allocator_put_arena(&alloc, arena, 1 << 24);

	struct memory_context mem_ctx;
	if (memory_context_init(&mem_ctx, "test", &alloc) < 0) {
		return 1;
	}

	struct radix radix;
	if (radix_init(&radix, &mem_ctx) < 0) {
		return 1;
	}

	uint8_t k1[4] = {192, 168, 0, 1};
	uint8_t k2[4] = {192, 173, 255, 0};
	if (radix_insert(&radix, 4, k1, 1) < 0) {
		return 1;
	}
	if (radix_insert(&radix, 4, k2, 2) < 0) {
		return 1;
	}

	uint32_t v1 = radix_lookup(&radix, 4, k1);
	assert(v1 == 1);

	uint32_t v2 = radix_lookup(&radix, 4, k2);
	assert(v2 == 2);

	if (radix_insert(&radix, 4, k1, 3) < 0) {
		return 1;
	}
	v1 = radix_lookup(&radix, 4, k1);
	assert(v1 == 3);

	struct radix_iterate_ctx ctx;
	ctx.count = 0;
	if (radix_walk(&radix, 4, radix_iterate_cb, &ctx) < 0) {
		return 1;
	}
	assert(ctx.count == 2);

	assert(memcmp(k1, ctx.keys[0], 4) == 0);
	assert(ctx.values[0] == 3);

	assert(memcmp(k2, ctx.keys[1], 4) == 0);
	assert(ctx.values[1] == 2);

	radix_free(&radix);

	puts("OK!");

	memory_context_fini(&mem_ctx);
	free(arena);

	return 0;
}