#pragma once

/*
 * Hybrid byte-stride LPM with a hashed top level for 8-byte range maps.
 *
 * Lookup probes a hash on the first hops key bytes for the page number of
 * the trie page those bytes lead to, then walks the remaining bytes from
 * that page. A miss falls back to the root walk, which is short by
 * construction. Values are bit-identical to lpm8_lookup on the same trie.
 *
 * Correctness of the shortcut: an entry exists for a prefix of hops bytes
 * exactly when every slot on its path in hops 0..hops-1 holds a page
 * pointer. The walk of any key sharing that prefix follows those exact
 * slots, so it cannot terminate before hop hops and lands on precisely
 * the page stored in the entry; continuing the standard walk there is the
 * suffix of the full walk. A miss means some slot on the prefix path is
 * flagged, so the fallback root walk terminates within hops loads. Entry
 * count equals the trie page count at level hops and is fixed at build
 * time; nothing is invalidated at run time because the table is built
 * with the trie and swapped atomically through shared memory with it.
 *
 * The hash side is a value_slot_index: a self-contained open-addressing
 * slot table with fixed 8-byte keys, modelled on common/str_index.h. Keys
 * are the hops prefix bytes zero-padded to eight; storage, hashing, and
 * equality operate on those raw bytes. All storage flows through the
 * memory_context passed to lpm_hash_init (the slot table and the trie via
 * memory_balloc), so the whole structure is usable from shared memory;
 * the slot table is a shared-memory relative pointer.
 */

#include <stdint.h>
#include <string.h>

#include "common/crc32.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "common/range_index.h"

// Slot load factor kept by value_slot_index.
//
// Matches str_index: a grow triggers when size reaches capacity /
// SPARSE_FACTOR (25% load). Each grow doubles capacity, so the table stays at
// the same load after the insert that triggered the grow.
#define VALUE_SLOT_INDEX_SPARSE_FACTOR 4

// Capacity of the first table allocation, taken on the first insert.
//
// A power of two so that hash & (capacity - 1) is a valid probe index from the
// very first grow. Zero capacity means the table is unallocated.
#define VALUE_SLOT_INDEX_INITIAL_CAPACITY 8

// One slot of the value_slot_index table.
//
// The slot IS the table: open addressing walks it directly. A slot is empty
// when value == LPM_VALUE_INVALID, so a freshly memset 0xff table is full of
// empty slots. Stored values are trie page numbers, which stay far below
// 0xffffffff for any real table.
struct lpm_hash_slot {
	uint8_t key[8];
	uint32_t value;
};

// Self-contained open-addressing slot table for fixed 8-byte keys.
//
// Mirrors struct str_index: the table is the slot array itself (no external
// values array, no read callback), capacity starts at zero, and the first
// insert grows it. All allocation is via memory_balloc through memory_context.
//
// capacity is always zero or a power of two, so the probe index is computed as
// hash & (capacity - 1) instead of hash % capacity.
struct value_slot_index {
	struct memory_context *memory_context;
	struct lpm_hash_slot *slots;
	uint32_t size;
	uint32_t capacity;
};

// Initialise an empty index.
//
// Capacity starts at zero; the first insert grows the table to
// VALUE_SLOT_INDEX_INITIAL_CAPACITY slots. Always returns 0.
static inline int
value_slot_index_init(
	struct value_slot_index *index, struct memory_context *memory_context
) {
	SET_OFFSET_OF(&index->memory_context, memory_context);
	SET_OFFSET_OF(&index->slots, NULL);
	index->capacity = 0;
	index->size = 0;

	return 0;
}

// Release the slot table.
//
// NULL-safe: a freshly initialised or already-finalised index has slots == NULL
// and is left untouched.
static inline void
value_slot_index_fini(struct value_slot_index *index) {
	struct lpm_hash_slot *slots = ADDR_OF(&index->slots);
	if (slots == NULL) {
		return;
	}

	memory_bfree(
		ADDR_OF(&index->memory_context),
		slots,
		sizeof(struct lpm_hash_slot) * index->capacity
	);
	SET_OFFSET_OF(&index->slots, NULL);
	index->capacity = 0;
	index->size = 0;
}

// Probe the table for key8.
//
// Returns the stored value, or LPM_VALUE_INVALID when no slot holds key8.
// Hashing is CRC-32C over the full 8 bytes (binary keys may contain NUL, so
// strnlen must not be used), and equality is memcmp over 8 bytes.
static inline uint32_t
value_slot_index_lookup(
	const struct value_slot_index *index, const uint8_t *key8
) {
	if (!index->capacity) {
		return LPM_VALUE_INVALID;
	}

	uint32_t mask = index->capacity - 1;
	uint32_t hash = crc32(key8, 8, 0) & mask;

	const struct lpm_hash_slot *slots = ADDR_OF(&index->slots);
	while (slots[hash].value != LPM_VALUE_INVALID) {
		if (memcmp(slots[hash].key, key8, 8) == 0) {
			return slots[hash].value;
		}
		hash = (hash + 1) & mask;
	}
	return LPM_VALUE_INVALID;
}

// Place (key8, value) into the first free slot at or after the hash, without
// checking load.
//
// The caller must have ensured capacity is large enough (the public insert
// and the expand caller do this). Linear probing matches str_index.
static inline void
value_slot_index_insert_force(
	struct value_slot_index *index, const uint8_t *key8, uint32_t value
) {
	uint32_t mask = index->capacity - 1;
	uint32_t hash = crc32(key8, 8, 0) & mask;

	struct lpm_hash_slot *slots = ADDR_OF(&index->slots);
	while (slots[hash].value != LPM_VALUE_INVALID) {
		hash = (hash + 1) & mask;
	}

	memcpy(slots[hash].key, key8, 8);
	slots[hash].value = value;
	index->size += 1;
}

// Re-house the table at new_capacity and re-insert all live slots by rehashing
// their 8-byte keys.
//
// Mirrors str_index_expand. The new table is memset to 0xff so every slot
// starts empty (value == LPM_VALUE_INVALID), then each live old slot is
// re-inserted via value_slot_index_insert_force. The old table is released.
// Returns 0 on success, -1 on allocation failure (the index is left intact).
//
// new_capacity must be a power of two: the probe index is hash & (capacity -
// 1), so a non-power-of-two capacity would silently corrupt lookups.
static inline int
value_slot_index_expand(struct value_slot_index *index, uint32_t new_capacity) {
	struct lpm_hash_slot *new_slots = (struct lpm_hash_slot *)memory_balloc(
		ADDR_OF(&index->memory_context),
		sizeof(struct lpm_hash_slot) * new_capacity
	);
	if (new_slots == NULL) {
		return -1;
	}
	memset(new_slots, 0xff, sizeof(struct lpm_hash_slot) * new_capacity);

	struct lpm_hash_slot *old_slots = ADDR_OF(&index->slots);
	uint32_t old_capacity = index->capacity;

	SET_OFFSET_OF(&index->slots, new_slots);
	index->capacity = new_capacity;

	for (uint32_t pos = 0; pos < old_capacity; ++pos) {
		if (old_slots[pos].value == LPM_VALUE_INVALID) {
			continue;
		}
		value_slot_index_insert_force(
			index, old_slots[pos].key, old_slots[pos].value
		);
	}

	if (old_slots != NULL) {
		memory_bfree(
			ADDR_OF(&index->memory_context),
			old_slots,
			sizeof(struct lpm_hash_slot) * old_capacity
		);
	}
	return 0;
}

// Insert (key8, value), growing the table on demand.
//
// When size reaches capacity / SPARSE_FACTOR the table is doubled, or seeded
// at VALUE_SLOT_INDEX_INITIAL_CAPACITY on the first insert, keeping capacity a
// power of two. Returns 0 on success, -1 on allocation failure (the index is
// left intact).
static inline int
value_slot_index_insert(
	struct value_slot_index *index, const uint8_t *key8, uint32_t value
) {
	if (index->size >= index->capacity / VALUE_SLOT_INDEX_SPARSE_FACTOR) {
		uint32_t new_capacity =
			index->capacity ? index->capacity * 2
					: VALUE_SLOT_INDEX_INITIAL_CAPACITY;
		if (value_slot_index_expand(index, new_capacity)) {
			return -1;
		}
	}

	value_slot_index_insert_force(index, key8, value);
	return 0;
}

// Default hashed-top height.
//
// Six measured within a few percent of per-trie optimal on the production
// acl ruleset capture across all four net6 half-tries: hit rates 0.48..0.80
// with 345..3204 entries, cutting the dependent walk chain by 44..57%.
#define LPM_HASH_HOPS_DEFAULT 6

struct lpm_hash {
	struct lpm lpm;
	struct value_slot_index index;
	uint8_t hops;
};

// Allocate an empty hybrid.
//
// hops is the hashed top height: the number of leading key bytes resolved by
// one hash probe on lookup. Values above 7 are clamped to 7, because an
// 8-byte trie never has pages below level 8. hops 0 disables the shortcut:
// every lookup falls back to the plain root walk. Returns 0 on success, -1 on
// LPM allocation failure (the index is finalised before returning).
static inline int
lpm_hash_init(
	struct lpm_hash *lpm_hash,
	struct memory_context *memory_context,
	uint8_t hops,
	const char *name
) {
	if (hops > 7) {
		hops = 7;
	}
	if (value_slot_index_init(&lpm_hash->index, memory_context) != 0) {
		return -1;
	}
	lpm_hash->hops = hops;

	if (lpm_init(&lpm_hash->lpm, memory_context, name) != 0) {
		// lpm_init attached its embedded memory_context as a child
		// of memory_context before failing, so the trie must be
		// released through lpm_free to detach it; skipping this leaks
		// a live child into the caller's context tree and breaks the
		// tree walk on free. Safe on a trie whose init stopped before
		// the first page: pages is NULL and the context is empty.
		lpm_free(&lpm_hash->lpm);
		value_slot_index_fini(&lpm_hash->index);
		return -1;
	}

	return 0;
}

// Release the index and the LPM trie.
//
// Safe on an lpm_hash whose init failed halfway: value_slot_index_fini is
// NULL-safe and lpm_free is safe on a zeroed trie.
static inline void
lpm_hash_fini(struct lpm_hash *lpm_hash) {
	lpm_free(&lpm_hash->lpm);
	value_slot_index_fini(&lpm_hash->index);
}

// Insert [from..to] -> value into the trie.
//
// The hash side records trie pages, not values, so inserts are plain trie
// inserts; the top table is derived once afterwards by lpm_hash_build_top.
// Returns 0 on success, -1 on trie allocation failure.
static inline int
lpm_hash_insert(
	struct lpm_hash *lpm_hash,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
) {
	return lpm8_insert(&lpm_hash->lpm, from, to, value);
}

// Resolve a page pointer to its page number.
//
// Pages of one chunk are contiguous in memory and the chunk array is indexed
// by page number, so the number is recovered by locating the owning chunk.
// Returns LPM_VALUE_INVALID when the page is not found, which cannot happen
// for a pointer reached from lpm->pages itself.
static inline uint32_t
lpm_page_index(const struct lpm *lpm, const struct lpm_page *page) {
	struct lpm_page **pages = ADDR_OF(&lpm->pages);
	uint32_t chunk_count =
		(lpm->page_count + LPM_CHUNK_SIZE - 1) / LPM_CHUNK_SIZE;

	for (uint32_t chunk_idx = 0; chunk_idx < chunk_count; ++chunk_idx) {
		struct lpm_page *chunk = ADDR_OF(&pages[chunk_idx]);
		if (page >= chunk && page < chunk + LPM_CHUNK_SIZE) {
			return chunk_idx * LPM_CHUNK_SIZE +
			       (uint32_t)(page - chunk);
		}
	}
	return LPM_VALUE_INVALID;
}

// Depth-first descent for lpm_hash_build_top.
//
// key holds the zero-padded path bytes; page is the page that path leads to.
// At depth hops the page number is recorded for the path. Between, only
// pointer slots are followed, which is exactly the entry-existence rule of
// the shortcut: an entry exists for a path of hops bytes iff every slot along
// it holds a page pointer. Returns 0 on success, -1 on failure.
static inline int
lpm_hash_top_descend(
	struct lpm_hash *lpm_hash,
	struct lpm_page *page,
	uint8_t depth,
	uint8_t key[8]
) {
	if (depth == lpm_hash->hops) {
		uint32_t page_idx = lpm_page_index(&lpm_hash->lpm, page);
		if (page_idx == LPM_VALUE_INVALID) {
			return -1;
		}
		return value_slot_index_insert(&lpm_hash->index, key, page_idx);
	}

	for (uint32_t slot = 0; slot < 256; ++slot) {
		union lpm_value *stored_value = page->values + slot;
		if (stored_value->value & LPM_VALUE_FLAG) {
			continue;
		}
		key[depth] = (uint8_t)slot;
		if (lpm_hash_top_descend(
			    lpm_hash,
			    ADDR_OF(&stored_value->page),
			    depth + 1,
			    key
		    )) {
			return -1;
		}
	}
	return 0;
}

// Derive the hashed top table from the built trie.
//
// Must run after the last lpm_hash_insert: page numbers are assigned at page
// creation and never reused, so entries recorded now stay valid for the life
// of the trie. With hops 0 there is nothing to build. Returns 0 on success,
// -1 on allocation failure (the caller tears the hybrid down).
static inline int
lpm_hash_build_top(struct lpm_hash *lpm_hash) {
	if (!lpm_hash->hops) {
		return 0;
	}

	uint8_t key[8];
	memset(key, 0, sizeof(key));
	return lpm_hash_top_descend(
		lpm_hash, lpm_page(&lpm_hash->lpm, 0), 0, key
	);
}

// Copy the hops-byte prefix of key into a zero-padded probe key.
static inline void
lpm_hash_top_key(
	const struct lpm_hash *lpm_hash, const uint8_t *key, uint8_t top_key[8]
) {
	memcpy(top_key, key, lpm_hash->hops);
	memset(top_key + lpm_hash->hops, 0, 8 - lpm_hash->hops);
}

// Probe the top hash, then finish the walk from the hit page or the root.
//
// On a hit the answer cannot lie above hop hops (entry-existence rule), so
// walking hops..7 from the stored page returns the full walk's value. On a
// miss the root walk terminates within hops loads, so the fallback stays
// short. Both paths return exactly lpm8_lookup's value.
static inline uint32_t
lpm_hash_lookup(const struct lpm_hash *lpm_hash, const uint8_t *key) {
	uint8_t top_key[8];
	lpm_hash_top_key(lpm_hash, key, top_key);

	uint32_t page_idx = value_slot_index_lookup(&lpm_hash->index, top_key);
	if (page_idx == LPM_VALUE_INVALID) {
		return lpm8_lookup(&lpm_hash->lpm, key);
	}

	struct lpm_page *page = lpm_page(&lpm_hash->lpm, page_idx);
	union lpm_value *value = NULL;
	for (uint8_t hop = lpm_hash->hops; hop < 8; ++hop) {
		value = page->values + key[hop];
		if (value->value & LPM_VALUE_FLAG) {
			break;
		}
		// An intermediate node (flag clear) always has a child page,
		// so the relative pointer is never NULL here — the same
		// contract as lpm_lookup.
		page = ADDR_OF_NONNULL(&value->page);
	}

	return LPM_VALUE_GET(value->value);
}

struct lpm_hash_insert_ctx {
	struct lpm_hash *lpm_hash;
};

// range_index_build callback: forwards each [from..to] -> value range to
// lpm_hash_insert.
//
// key_size is always 8 here (the only lpm_hash interface), so it is ignored.
static inline int
lpm_hash_insert_cb(
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value,
	void *data
) {
	(void)key_size;
	struct lpm_hash_insert_ctx *ctx = (struct lpm_hash_insert_ctx *)data;
	return lpm_hash_insert(ctx->lpm_hash, from, to, value);
}

/*
 * Build an lpm_hash from a range_index.
 *
 * Inserts every range into the trie in one pass, then derives the hashed top
 * table from the finished trie with lpm_hash_build_top. Returns 0 on success,
 * -1 on failure (the lpm_hash is finalised before returning so the caller
 * need not clean it up).
 */
static inline int
range_index_build_lpm_hash(
	const struct range_index *range_index,
	struct memory_context *memory_context,
	uint8_t hops,
	const char *name,
	struct lpm_hash *lpm_hash
) {
	if (lpm_hash_init(lpm_hash, memory_context, hops, name) != 0) {
		return -1;
	}

	struct lpm_hash_insert_ctx insert_ctx = {lpm_hash};
	if (range_index_build(
		    range_index, 8, lpm_hash_insert_cb, &insert_ctx
	    )) {
		lpm_hash_fini(lpm_hash);
		return -1;
	}

	if (lpm_hash_build_top(lpm_hash)) {
		lpm_hash_fini(lpm_hash);
		return -1;
	}

	return 0;
}
