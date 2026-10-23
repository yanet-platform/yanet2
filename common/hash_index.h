#pragma once

#include <stdint.h>
#include <string.h>

#include "memory.h"

#define HASH_INDEX_INVALID 0xffffffff
#define HASH_INDEX_SPARSE_FACTOR 4
// Largest capacity whose slot count does not wrap when scaled by the
// sparse factor; capacities beyond it are refused at every entry point.
#define HASH_INDEX_MAX_CAPACITY (UINT32_MAX / HASH_INDEX_SPARSE_FACTOR)

// One probe-table slot: the originating hash next to the value.
//
// Lookups compare the stored hash before invoking the equality callback,
// so probing past a foreign slot costs a compare instead of a callback
// round into the external array the value indexes.
struct hash_index_entry {
	uint32_t hash;
	uint32_t value;
};

// Equality predicate invoked during hash_index_lookup.
//
// Called for every probed slot carrying the looked-up hash until it
// returns 0 (match) or an empty slot is reached. value is the slot
// payload, typically an index into an external values array. data is the
// opaque pointer passed alongside the callback from the lookup call site.
// Return 0 to accept the slot, non-zero to keep probing.
typedef int (*hash_index_eq_func)(uint32_t value, const void *data);

struct hash_index {
	struct memory_context *memory_context;
	struct hash_index_entry *entries;
	uint32_t count;
	uint32_t capacity;
};

// Allocate a fixed-capacity hash index backed by memory_context.
static inline int
hash_index_init(
	struct hash_index *hash_index,
	struct memory_context *memory_context,
	uint32_t capacity
) {
	SET_OFFSET_OF(&hash_index->memory_context, memory_context);

	if (capacity == 0) {
		SET_OFFSET_OF(&hash_index->entries, NULL);
		hash_index->capacity = 0;
		hash_index->count = 0;
		return 0;
	}
	if (capacity > HASH_INDEX_MAX_CAPACITY) {
		SET_OFFSET_OF(&hash_index->entries, NULL);
		hash_index->capacity = 0;
		hash_index->count = 0;
		return -1;
	}

	struct hash_index_entry *entries =
		(struct hash_index_entry *)memory_balloc(
			ADDR_OF(&hash_index->memory_context),
			sizeof(struct hash_index_entry) * capacity *
				HASH_INDEX_SPARSE_FACTOR
		);
	if (entries == NULL) {
		SET_OFFSET_OF(&hash_index->entries, NULL);
		hash_index->capacity = 0;
		hash_index->count = 0;
		return -1;
	}

	memset(entries,
	       0xff,
	       sizeof(struct hash_index_entry) * capacity *
		       HASH_INDEX_SPARSE_FACTOR);

	SET_OFFSET_OF(&hash_index->entries, entries);
	hash_index->capacity = capacity;
	hash_index->count = 0;

	return 0;
}

static inline void
hash_index_fini(struct hash_index *hash_index) {
	struct hash_index_entry *entries = ADDR_OF(&hash_index->entries);
	if (entries == NULL) {
		return;
	}

	memory_bfree(
		ADDR_OF(&hash_index->memory_context),
		entries,
		sizeof(struct hash_index_entry) * hash_index->capacity *
			HASH_INDEX_SPARSE_FACTOR
	);
	SET_OFFSET_OF(&hash_index->entries, NULL);
	hash_index->capacity = 0;
	hash_index->count = 0;
}

// Rebuild the table for a new capacity, preserving every stored entry.
//
// The rehash lands in a fresh buffer and the live index is swapped only
// once every entry is placed, so a failure leaves the old table intact.
// The new capacity must be positive and fit the live entry count; a
// rehash to the current capacity is a successful no-op. A zero-capacity
// index gains its table here, so the rehash also serves as a late init.
static inline int
hash_index_rehash(struct hash_index *hash_index, uint32_t new_capacity) {
	if (new_capacity == 0 || new_capacity > HASH_INDEX_MAX_CAPACITY ||
	    new_capacity < hash_index->count) {
		return -1;
	}
	if (new_capacity == hash_index->capacity) {
		return 0;
	}

	struct memory_context *memory_context =
		ADDR_OF(&hash_index->memory_context);
	uint32_t new_slots = new_capacity * HASH_INDEX_SPARSE_FACTOR;
	struct hash_index_entry *new_entries =
		(struct hash_index_entry *)memory_balloc(
			memory_context,
			sizeof(struct hash_index_entry) * new_slots
		);
	if (new_entries == NULL) {
		return -1;
	}
	memset(new_entries, 0xff, sizeof(struct hash_index_entry) * new_slots);

	struct hash_index_entry *old_entries = ADDR_OF(&hash_index->entries);
	uint32_t old_slots = hash_index->capacity * HASH_INDEX_SPARSE_FACTOR;

	for (uint32_t pos = 0; pos < old_slots; ++pos) {
		if (old_entries[pos].value == HASH_INDEX_INVALID) {
			continue;
		}
		uint32_t bucket = old_entries[pos].hash % new_slots;
		while (new_entries[bucket].value != HASH_INDEX_INVALID) {
			bucket = (bucket + 1) % new_slots;
		}
		new_entries[bucket] = old_entries[pos];
	}

	SET_OFFSET_OF(&hash_index->entries, new_entries);
	hash_index->capacity = new_capacity;

	if (old_entries != NULL) {
		memory_bfree(
			memory_context,
			old_entries,
			sizeof(struct hash_index_entry) * old_slots
		);
	}
	return 0;
}

// Resolve the value stored under hash whose key the callback accepts.
//
// The hash must be derived from the key exactly as at insert time: slots
// storing a different hash are skipped without consulting the callback.
static inline uint32_t
hash_index_lookup(
	const struct hash_index *hash_index,
	uint32_t hash,
	hash_index_eq_func eq_func,
	const void *eq_func_data
) {
	if (!hash_index->capacity) {
		return HASH_INDEX_INVALID;
	}

	uint32_t bucket =
		hash % (hash_index->capacity * HASH_INDEX_SPARSE_FACTOR);
	const struct hash_index_entry *entries = ADDR_OF(&hash_index->entries);

	while (entries[bucket].value != HASH_INDEX_INVALID) {
		if (entries[bucket].hash == hash &&
		    eq_func(entries[bucket].value, eq_func_data) == 0) {
			return entries[bucket].value;
		}
		bucket = (bucket + 1) %
			 (hash_index->capacity * HASH_INDEX_SPARSE_FACTOR);
	}
	return HASH_INDEX_INVALID;
}

// Place value into the first free slot at or after hash % capacity.
//
// The index is fixed-capacity; insert returns -1 when it is full, 0 on
// success.
static inline int
hash_index_insert(
	struct hash_index *hash_index, uint32_t hash, uint32_t value
) {
	if (hash_index->count >= hash_index->capacity) {
		return -1;
	}

	uint32_t bucket =
		hash % (hash_index->capacity * HASH_INDEX_SPARSE_FACTOR);
	struct hash_index_entry *entries = ADDR_OF(&hash_index->entries);

	while (entries[bucket].value != HASH_INDEX_INVALID) {
		bucket = (bucket + 1) %
			 (hash_index->capacity * HASH_INDEX_SPARSE_FACTOR);
	}

	entries[bucket].hash = hash;
	entries[bucket].value = value;
	hash_index->count += 1;

	return 0;
}
