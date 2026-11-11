/*
 * Fuzzes the LPM range insert by the lookups at the bounds of its ranges.
 *
 * The first byte picks the key size, the second the arena budget in chunks
 * of pages down from the whole arena; the rest is up to eight ranges, each
 * a start, an end and a two-byte value. Each bound of a range and the key
 * just outside it must map to the value of the last range holding it. An
 * insert fails only for want of memory, leaving the keys outside its range
 * intact and each key inside with the old or the new value. An inverted
 * range is empty. The LPM test checks the page layout against the walk.
 */

#include <errno.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "common/key.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"

#define FUZZ_MAX_RANGES 8
// The arena in blocks of a chunk of pages: three chunks and their directory.
//
// A range of the longest key fits, and the budget makes the allocation of
// the first, the second, the third or the fourth chunk fail; larger trees
// are left to the LPM test.
#define FUZZ_ARENA_CHUNKS 4

struct fuzz_range {
	const uint8_t *from;
	const uint8_t *to;
	uint32_t value;
};

// The ranges the tree took, in order, and the one it failed to take.
struct fuzz_model {
	uint8_t key_size;
	struct fuzz_range taken[FUZZ_MAX_RANGES];
	size_t taken_count;
	struct fuzz_range failed;
	bool has_failed;
};

static struct fuzz_range
fuzz_range_at(const uint8_t *ranges, uint8_t key_size, size_t idx) {
	const uint8_t *from = ranges + idx * (2u * key_size + 2);
	const uint8_t *to = from + key_size;
	return (struct fuzz_range){
		.from = from,
		.to = to,
		.value = (uint32_t)to[key_size] << 8 | to[key_size + 1],
	};
}

static bool
range_holds(
	uint8_t key_size, const struct fuzz_range *range, const uint8_t *key
) {
	return filter_key_cmp(key_size, range->from, key) <= 0 &&
	       filter_key_cmp(key_size, key, range->to) <= 0;
}

// Steps the key to its neighbour up or down; false when it wraps around.
static bool
key_step(uint8_t key_size, uint8_t *key, bool up) {
	uint8_t edge = up ? 0xff : 0x00;
	for (size_t idx = key_size; idx-- > 0;) {
		uint8_t byte = key[idx];
		key[idx] = (uint8_t)(up ? byte + 1 : byte - 1);
		if (byte != edge) {
			return true;
		}
	}
	return false;
}

// Whether the tree maps the key to the value of the last range holding it.
static bool
lookup_matches(
	const struct lpm *lpm,
	const struct fuzz_model *model,
	const uint8_t *key
) {
	uint32_t value = lpm_lookup(lpm, model->key_size, key);
	if (model->has_failed &&
	    range_holds(model->key_size, &model->failed, key) &&
	    value == model->failed.value) {
		return true;
	}
	for (size_t idx = model->taken_count; idx-- > 0;) {
		if (range_holds(model->key_size, &model->taken[idx], key)) {
			return value == model->taken[idx].value;
		}
	}
	return value == LPM_VALUE_INVALID;
}

// Whether the tree maps the bounds of the range and their outer neighbours.
static bool
bounds_match(
	const struct lpm *lpm,
	const struct fuzz_model *model,
	const struct fuzz_range *range
) {
	uint8_t key[LPM_KEY_SIZE_MAX];
	const uint8_t *bounds[2] = {range->from, range->to};
	for (size_t idx = 0; idx < 2; ++idx) {
		memcpy(key, bounds[idx], model->key_size);
		if (!lookup_matches(lpm, model, key) ||
		    (key_step(model->key_size, key, idx == 1) &&
		     !lookup_matches(lpm, model, key))) {
			return false;
		}
	}
	return true;
}

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	// A chunk takes a block with red zones in a sanitized build, so the
	// arena is sized in blocks to hold the same chunks in every build.
	//
	// Aligned to its size, the arena splits into the same blocks, and an
	// input runs out of memory at the same page on every run.
	static void *arena;
	static size_t chunk_block;
	if (arena == NULL) {
		chunk_block = block_allocator_pool_size(
			NULL,
			block_allocator_pool_index(
				NULL,
				sizeof(struct lpm_page) * LPM_CHUNK_SIZE +
					2 * ASAN_RED_ZONE
			)
		);
		size_t arena_size = FUZZ_ARENA_CHUNKS * chunk_block;
		arena = aligned_alloc(arena_size, arena_size);
		if (arena == NULL) {
			abort();
		}
	}
	if (size < 2) {
		return 0;
	}
	struct fuzz_model model = {
		.key_size = (uint8_t)(1 + data[0] % LPM_KEY_SIZE_MAX),
	};
	size_t budget =
		(FUZZ_ARENA_CHUNKS - data[1] % FUZZ_ARENA_CHUNKS) * chunk_block;
	const uint8_t *ranges = data + 2;
	size_t count = (size - 2) / (2u * model.key_size + 2);
	if (count > FUZZ_MAX_RANGES) {
		count = FUZZ_MAX_RANGES;
	}

	struct block_allocator ba;
	struct memory_context mctx;
	struct lpm lpm;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, budget);
	memory_context_init(&mctx, "lpm_fuzz", &ba);
	if (lpm_init(&lpm, &mctx, "lpm")) {
		memory_context_fini(&mctx);
		return 0;
	}

	// A key size of 0 is refused and leaves the tree untouched.
	errno = 0;
	if (lpm_insert(&lpm, 0, ranges, ranges, 0) != -1 || errno != EINVAL) {
		abort();
	}
	size_t done = 0;
	while (done < count && !model.has_failed) {
		struct fuzz_range range =
			fuzz_range_at(ranges, model.key_size, done++);
		size_t page_count = lpm.page_count;
		errno = 0;
		int rc = lpm_insert(
			&lpm, model.key_size, range.from, range.to, range.value
		);
		if (filter_key_cmp(model.key_size, range.from, range.to) > 0) {
			if (rc != 0 || lpm.page_count != page_count) {
				abort();
			}
		} else if (rc != 0) {
			if (errno != ENOMEM) {
				abort();
			}
			model.failed = range;
			model.has_failed = true;
		} else {
			model.taken[model.taken_count++] = range;
		}
	}
	for (size_t idx = 0; idx < done; ++idx) {
		struct fuzz_range range =
			fuzz_range_at(ranges, model.key_size, idx);
		if (!bounds_match(&lpm, &model, &range)) {
			abort();
		}
	}

	lpm_free(&lpm);
	memory_context_fini(&mctx);
	return 0;
}
