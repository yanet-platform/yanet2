#pragma once

/*
 * Frozen longest prefix match: a read-only map of n-byte keys to values.
 *
 * A builder (fast_lpm_builder.h) compiles every insert at once into a trie
 * that is never modified afterwards. A node is either full, 256 entries
 * indexed by one key byte, or a bucket, a subtree of at most four range
 * boundaries searched with one compare of the remaining key. Keys are 4, 8,
 * 12 or 16 bytes, big-endian; values are below the invalid value, which
 * every key outside the inserted ranges maps to.
 */

#include <endian.h>
#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <string.h>

#if defined(__SSE2__)
#include <immintrin.h>
#endif

#include "memory.h"
#include "memory_address.h"

// The value of a key no inserted range covers.
#define FAST_LPM_VALUE_INVALID 0x7fffffffu

#define FAST_LPM_KEY_SIZE_MAX 16

/*
 * A node entry: a leaf or a link to a child node.
 *
 * A leaf is the value shifted left by one with the low bit set, so the
 * all-ones word is the invalid value. A link keeps the low bit clear,
 * the child kind in the next two bits and, above them, the signed distance
 * from the node holding the entry to the child in 64-byte units.
 */
#define FAST_LPM_ENTRY_LEAF 0x1u
#define FAST_LPM_ENTRY_KIND_MASK 0x6u
#define FAST_LPM_ENTRY_LINK_MASK (~0x7u)
#define FAST_LPM_KIND_FULL 0x0u
#define FAST_LPM_KIND_NARROW 0x2u
#define FAST_LPM_KIND_WIDE 0x4u

#define FAST_LPM_NODE_ALIGN 64
#define FAST_LPM_FULL_SIZE (256 * sizeof(uint32_t))
#define FAST_LPM_BUCKET_SIZE 64

/*
 * Bucket layouts, every bucket one 64-byte line.
 *
 * Narrow (the boundaries differ in the last 8 key bytes at most): values[5]
 * in reverse order at 0 and four bounds at 32, 32-bit for 4-byte keys and
 * 64-bit otherwise, stored as the low key bytes of the boundary with the
 * sign bit flipped. The value index is the number of bounds above the key.
 * Wide (16- and 12-byte keys above the last 8 bytes): values[3] at 0 and
 * two bounds at 16 as (high, low) word pairs of the whole key; the value
 * index is the number of bounds at or below the key. Missing bounds are
 * all ones and missing values repeat the last one.
 */
#define FAST_LPM_NARROW_BOUNDS 4
#define FAST_LPM_NARROW_BOUNDS_OFFSET 32
#define FAST_LPM_WIDE_BOUNDS 2
#define FAST_LPM_WIDE_BOUNDS_OFFSET 16

// The head of every memory block holding nodes; nodes follow it.
struct fast_lpm_chunk {
	// The next block of the trie, an offset pointer.
	struct fast_lpm_chunk *next;
	uint64_t size;
	uint8_t pad[FAST_LPM_NODE_ALIGN - 16];
};

struct fast_lpm {
	struct memory_context memory_context;
	// The full root node, an offset pointer.
	uint8_t *root;
	// The memory blocks of the trie, an offset pointer.
	struct fast_lpm_chunk *chunks;
	// Inserts waiting for the build, offset pointers into the LPM's
	// memory: start and end keys as 16-byte integers, and values.
	uint8_t *staged_from;
	uint8_t *staged_to;
	uint32_t *staged_value;
	uint64_t staged_count;
	uint64_t staged_capacity;
	uint8_t key_size;
	// Built: the trie holds the inserts and takes no more.
	bool frozen;
};

static inline bool
fast_lpm_key_size_valid(uint8_t key_size) {
	return key_size == 4 || key_size == 8 || key_size == 12 ||
	       key_size == 16;
}

// Releases the memory blocks of a trie list.
static inline void
fast_lpm_free_chunks(
	struct memory_context *memory_context, struct fast_lpm_chunk *chunk
) {
	while (chunk != NULL) {
		struct fast_lpm_chunk *next = ADDR_OF(&chunk->next);
		memory_bfree(memory_context, chunk, chunk->size);
		chunk = next;
	}
}

// Releases the staged inserts.
static inline void
fast_lpm_free_staged(struct fast_lpm *lpm) {
	uint64_t capacity = lpm->staged_capacity;
	memory_bfree(
		&lpm->memory_context, ADDR_OF(&lpm->staged_from), capacity * 16
	);
	memory_bfree(
		&lpm->memory_context, ADDR_OF(&lpm->staged_to), capacity * 16
	);
	memory_bfree(
		&lpm->memory_context,
		ADDR_OF(&lpm->staged_value),
		capacity * sizeof(uint32_t)
	);
	SET_OFFSET_OF(&lpm->staged_from, NULL);
	SET_OFFSET_OF(&lpm->staged_to, NULL);
	SET_OFFSET_OF(&lpm->staged_value, NULL);
	lpm->staged_count = 0;
	lpm->staged_capacity = 0;
}

/*
 * Releases the LPM's memory.
 *
 * Safe on a zeroed LPM and on one whose init failed.
 */
static inline void
fast_lpm_free(struct fast_lpm *lpm) {
	if (ADDR_OF(&lpm->memory_context.block_allocator) == NULL) {
		return;
	}
	fast_lpm_free_chunks(&lpm->memory_context, ADDR_OF(&lpm->chunks));
	SET_OFFSET_OF(&lpm->chunks, NULL);
	SET_OFFSET_OF(&lpm->root, NULL);
	fast_lpm_free_staged(lpm);
	memory_context_fini(&lpm->memory_context);
}

static inline const uint8_t *
fast_lpm_link(const uint8_t *node, uint32_t entry) {
	return node + (int64_t)(int32_t)(entry & FAST_LPM_ENTRY_LINK_MASK) *
			      (FAST_LPM_NODE_ALIGN / 8);
}

static inline uint32_t
fast_lpm_leaf_value(uint32_t entry) {
	return entry >> 1;
}

// The key as an integer: bytes [from, from + 8) or [from, from + 4).
static inline uint64_t
fast_lpm_key_be64(const uint8_t *key) {
	uint64_t word;
	memcpy(&word, key, sizeof(word));
	return be64toh(word);
}

static inline uint32_t
fast_lpm_key_be32(const uint8_t *key) {
	uint32_t word;
	memcpy(&word, key, sizeof(word));
	return be32toh(word);
}

// The number of set bits in a 4-bit mask.
static inline uint32_t
fast_lpm_count4(uint32_t mask) {
	return (uint32_t)(0x4332322132212110ull >> (mask * 4)) & 0xf;
}

static inline uint32_t
fast_lpm_narrow_lookup(
	const uint8_t *node, uint8_t key_size, const uint8_t *key
) {
	const uint32_t *values = (const uint32_t *)node;
	const uint8_t *bounds = node + FAST_LPM_NARROW_BOUNDS_OFFSET;
	uint32_t above;
	if (key_size == 4) {
		int32_t biased =
			(int32_t)(fast_lpm_key_be32(key) ^ 0x80000000u);
#if defined(__SSE2__)
		__m128i gt = _mm_cmpgt_epi32(
			_mm_load_si128((const __m128i *)bounds),
			_mm_set1_epi32(biased)
		);
		above = fast_lpm_count4((uint32_t
		)_mm_movemask_ps(_mm_castsi128_ps(gt)));
#else
		const int32_t *b = (const int32_t *)bounds;
		above = (b[0] > biased) + (b[1] > biased) + (b[2] > biased) +
			(b[3] > biased);
#endif
	} else {
		int64_t biased =
			(int64_t)(fast_lpm_key_be64(key + key_size - 8) ^
				  0x8000000000000000ull);
#if defined(__AVX2__)
		__m256i gt = _mm256_cmpgt_epi64(
			_mm256_load_si256((const __m256i *)bounds),
			_mm256_set1_epi64x(biased)
		);
		above = fast_lpm_count4((uint32_t
		)_mm256_movemask_pd(_mm256_castsi256_pd(gt)));
#else
		const int64_t *b = (const int64_t *)bounds;
		above = (b[0] > biased) + (b[1] > biased) + (b[2] > biased) +
			(b[3] > biased);
#endif
	}
	return fast_lpm_leaf_value(values[above]);
}

static inline uint32_t
fast_lpm_wide_lookup(
	const uint8_t *node, uint8_t key_size, const uint8_t *key
) {
	const uint32_t *values = (const uint32_t *)node;
	const uint64_t *b =
		(const uint64_t *)(node + FAST_LPM_WIDE_BOUNDS_OFFSET);
	uint64_t hi = key_size == 16 ? fast_lpm_key_be64(key)
				     : fast_lpm_key_be32(key);
	uint64_t lo = fast_lpm_key_be64(key + key_size - 8);
	uint32_t below = (b[0] < hi || (b[0] == hi && b[1] <= lo)) +
			 (b[2] < hi || (b[2] == hi && b[3] <= lo));
	return fast_lpm_leaf_value(values[below]);
}

/*
 * Returns the value of the key or the invalid value.
 *
 * The LPM must be built. The key size must be the one the LPM was built
 * with; pass it as a constant so the walk compiles for that size.
 */
static inline uint32_t
fast_lpm_lookup(
	const struct fast_lpm *lpm, uint8_t key_size, const uint8_t *key
) {
	const uint8_t *node = ADDR_OF_NONNULL(&lpm->root);
	uint32_t entry = UINT32_MAX;
	for (uint8_t depth = 0; depth < key_size; ++depth) {
		entry = ((const uint32_t *)node)[key[depth]];
		if (entry & FAST_LPM_ENTRY_LEAF) {
			break;
		}
		uint32_t kind = entry & FAST_LPM_ENTRY_KIND_MASK;
		if (kind != FAST_LPM_KIND_FULL) {
			node = fast_lpm_link(node, entry);
			if (kind == FAST_LPM_KIND_NARROW) {
				return fast_lpm_narrow_lookup(
					node, key_size, key
				);
			}
			return fast_lpm_wide_lookup(node, key_size, key);
		}
		// A full child keeps the kind bits clear: no mask needed.
		node += (int64_t)(int32_t)entry * (FAST_LPM_NODE_ALIGN / 8);
	}
	return fast_lpm_leaf_value(entry);
}

static inline uint32_t
fast_lpm4_lookup(const struct fast_lpm *lpm, const uint8_t *key) {
	return fast_lpm_lookup(lpm, 4, key);
}

static inline uint32_t
fast_lpm8_lookup(const struct fast_lpm *lpm, const uint8_t *key) {
	return fast_lpm_lookup(lpm, 8, key);
}

static inline uint32_t
fast_lpm16_lookup(const struct fast_lpm *lpm, const uint8_t *key) {
	return fast_lpm_lookup(lpm, 16, key);
}

/*
 * A walk over the ranges of a built LPM in key order.
 *
 * Every step yields a maximal run of keys mapped to one valid value; keys
 * mapped to the invalid value are skipped. Reads the trie in place.
 */
struct fast_lpm_iter {
	const struct fast_lpm *lpm;
	uint8_t key_size;

	// The current range, valid after a step that returned true.
	uint8_t cur_from[FAST_LPM_KEY_SIZE_MAX];
	uint8_t cur_to[FAST_LPM_KEY_SIZE_MAX];
	uint32_t cur_value;

	// The full nodes on the path and the next slot of each.
	const uint8_t *nodes[FAST_LPM_KEY_SIZE_MAX];
	uint16_t slots[FAST_LPM_KEY_SIZE_MAX];
	int8_t depth;
	// The key bytes of the path.
	uint8_t key[FAST_LPM_KEY_SIZE_MAX];

	// The bucket being walked and its next piece, NULL when none.
	const uint8_t *bucket;
	uint32_t bucket_kind;
	uint8_t bucket_depth;
	uint8_t bucket_piece;

	// The open run: its first key and value.
	uint8_t run_from[FAST_LPM_KEY_SIZE_MAX];
	uint32_t run_value;
	bool has_run;
	bool done;
};

static inline void
fast_lpm_iter_init(struct fast_lpm_iter *it, const struct fast_lpm *lpm) {
	memset(it, 0, sizeof(*it));
	it->lpm = lpm;
	it->key_size = lpm->key_size;
	it->nodes[0] = ADDR_OF(&lpm->root);
	it->depth = it->nodes[0] == NULL ? -1 : 0;
}

/*
 * The next piece of the trie: a first key and the value of the keys up
 * to the next piece. Pieces of one value may follow each other.
 */
static inline bool
fast_lpm_iter_piece(struct fast_lpm_iter *it, uint8_t *start, uint32_t *value) {
	uint8_t key_size = it->key_size;
	for (;;) {
		if (it->bucket != NULL) {
			bool wide = it->bucket_kind == FAST_LPM_KIND_WIDE;
			uint8_t pieces = 1 + (wide ? FAST_LPM_WIDE_BOUNDS
						   : FAST_LPM_NARROW_BOUNDS);
			if (it->bucket_piece < pieces) {
				uint8_t piece = it->bucket_piece++;
				const uint32_t *values =
					(const uint32_t *)it->bucket;
				memcpy(start, it->key, it->bucket_depth);
				memset(start + it->bucket_depth,
				       0,
				       key_size - it->bucket_depth);
				if (wide) {
					*value = fast_lpm_leaf_value(
						values[piece]
					);
				} else {
					*value = fast_lpm_leaf_value(
						values[FAST_LPM_NARROW_BOUNDS -
						       piece]
					);
				}
				if (piece == 0) {
					return true;
				}
				// Bounds are whole low key words; padding
				// bounds repeat the last value.
				const uint8_t *bounds =
					it->bucket +
					(wide ? FAST_LPM_WIDE_BOUNDS_OFFSET
					      : FAST_LPM_NARROW_BOUNDS_OFFSET);
				if (wide) {
					const uint64_t *b =
						(const uint64_t *)bounds +
						2 * (piece - 1);
					uint64_t hi = htobe64(b[0]);
					uint64_t lo = htobe64(b[1]);
					memcpy(start,
					       (const uint8_t *)&hi + 16 -
						       key_size,
					       key_size - 8);
					memcpy(start + key_size - 8, &lo, 8);
				} else if (key_size == 4) {
					uint32_t b = htobe32(
						((const uint32_t *)bounds
						)[piece - 1] ^
						0x80000000u
					);
					memcpy(start, &b, 4);
				} else {
					uint64_t b = htobe64(
						((const uint64_t *)bounds
						)[piece - 1] ^
						0x8000000000000000ull
					);
					memcpy(start + key_size - 8, &b, 8);
				}
				return true;
			}
			it->bucket = NULL;
		}
		if (it->depth < 0) {
			return false;
		}
		int8_t depth = it->depth;
		if (it->slots[depth] == 256) {
			it->depth--;
			continue;
		}
		uint32_t slot = it->slots[depth]++;
		const uint8_t *node = it->nodes[depth];
		uint32_t entry = ((const uint32_t *)node)[slot];
		it->key[depth] = (uint8_t)slot;
		if (entry & FAST_LPM_ENTRY_LEAF) {
			memcpy(start, it->key, depth + 1);
			memset(start + depth + 1, 0, key_size - depth - 1);
			*value = fast_lpm_leaf_value(entry);
			return true;
		}
		const uint8_t *child = fast_lpm_link(node, entry);
		uint32_t kind = entry & FAST_LPM_ENTRY_KIND_MASK;
		if (kind == FAST_LPM_KIND_FULL) {
			it->depth++;
			it->nodes[it->depth] = child;
			it->slots[it->depth] = 0;
		} else {
			it->bucket = child;
			it->bucket_kind = kind;
			it->bucket_depth = depth + 1;
			it->bucket_piece = 0;
		}
	}
}

// Decrements a big-endian key.
static inline void
fast_lpm_key_dec(uint8_t key_size, uint8_t *key) {
	for (int idx = key_size - 1; idx >= 0; --idx) {
		if (key[idx]-- != 0) {
			break;
		}
	}
}

/*
 * Advances to the next range.
 *
 * Returns true with the range in the current fields, false once the key
 * space is exhausted.
 */
static inline bool
fast_lpm_iter_next(struct fast_lpm_iter *it) {
	uint8_t key_size = it->key_size;
	uint8_t start[FAST_LPM_KEY_SIZE_MAX];
	uint32_t value;
	while (!it->done) {
		if (!fast_lpm_iter_piece(it, start, &value)) {
			it->done = true;
			if (it->has_run &&
			    it->run_value != FAST_LPM_VALUE_INVALID) {
				memcpy(it->cur_from, it->run_from, key_size);
				memset(it->cur_to, 0xff, key_size);
				it->cur_value = it->run_value;
				return true;
			}
			return false;
		}
		if (it->has_run && value == it->run_value) {
			continue;
		}
		bool yield =
			it->has_run && it->run_value != FAST_LPM_VALUE_INVALID;
		if (yield) {
			memcpy(it->cur_from, it->run_from, key_size);
			memcpy(it->cur_to, start, key_size);
			fast_lpm_key_dec(key_size, it->cur_to);
			it->cur_value = it->run_value;
		}
		memcpy(it->run_from, start, key_size);
		it->run_value = value;
		it->has_run = true;
		if (yield) {
			return true;
		}
	}
	return false;
}
