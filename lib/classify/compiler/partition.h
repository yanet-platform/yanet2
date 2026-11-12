#pragma once

/*
 * Region partition of a key domain of up to 64 bits by a set of prefixes.
 *
 * Every point of a region is covered by the same set of prefixes. The
 * domain splits into pieces at every prefix start and past every prefix
 * end; a piece carries its region value, and the pieces of one region
 * need not be adjacent - an enclosing prefix keeps one value on both
 * sides of a nested one. The sweep numbers the values in the order it
 * first emits them, so every table built over the partition depends
 * only on the prefix set and not on the order the prefixes arrive in.
 *
 * The partition is built from the prefixes sorted by their start and
 * length, and a key resolves into its piece through a binary search
 * over the sorted piece starts.
 */

#include "common/exp_array.h"
#include "common/lpm.h"
#include "common/memory.h"

#include <stdint.h>
#include <string.h>

#define CLASSIFY_PARTITION_INVALID 0xffffffffu

// The most prefixes a partition takes: its pieces, two per prefix and
// one more, are indexed in 32 bits.
#define CLASSIFY_PREFIXES_MAX ((UINT32_MAX - 1) / 2)

// A prefix of the key domain: its start key and its length in bits.
struct classify_prefix {
	uint64_t from;
	uint64_t len;
};

/*
 * Growable set of the prefixes of a partition build.
 *
 * A ruleset repeats prefixes, a network shared by many rules above all,
 * so the set keeps each one once: the open addressing slots index the
 * items by prefix, a slot holding the item index plus one or zero when
 * empty, and the item count and the memory scale with the distinct
 * prefixes rather than the occurrences.
 */
struct classify_prefixes {
	struct classify_prefix *items;
	uint64_t count;
	uint32_t *slots;
	uint64_t slot_count;
};

struct classify_partition {
	// Piece start keys, ascending; the first one is zero.
	uint64_t *starts;
	// Region value of every piece.
	uint32_t *values;
	uint32_t count;
	uint32_t capacity;
	uint32_t region_count;
	uint8_t key_bits;
};

static inline uint64_t
classify_key_max(uint8_t key_bits) {
	return key_bits == 64 ? ~(uint64_t)0 : ((uint64_t)1 << key_bits) - 1;
}

// The last key of the prefix of the given start and length.
static inline uint64_t
classify_prefix_to(uint8_t key_bits, uint64_t from, uint64_t len) {
	uint64_t key_max = classify_key_max(key_bits);
	return from | (len >= key_bits ? 0 : key_max >> len);
}

// Decodes a big endian key of up to 8 bytes.
static inline uint64_t
classify_key_load(const uint8_t *bytes, uint8_t key_size) {
	uint64_t key = 0;
	for (uint8_t idx = 0; idx < key_size; ++idx) {
		key = (key << 8) | bytes[idx];
	}
	return key;
}

// Encodes a key of the width big endian into the leading bytes.
static inline void
classify_key_store(uint64_t key, uint8_t key_bits, uint8_t bytes[8]) {
	uint64_t aligned = key << (64 - key_bits);
	for (uint8_t idx = 0; idx < 8; ++idx) {
		bytes[idx] = (uint8_t)(aligned >> (56 - 8 * idx));
	}
}

/*
 * A full avalanche hash of the prefix (the MurmurHash3 finalizer).
 *
 * The short prefixes of a wide key have all their low bits zero; every
 * input bit moves the low hash bits that pick the slot, so they spread.
 */
static inline uint64_t
classify_prefix_hash(uint64_t from, uint64_t len) {
	uint64_t hash = from ^ (len * 0x9e3779b97f4a7c15ull);
	hash ^= hash >> 33;
	hash *= 0xff51afd7ed558ccdull;
	hash ^= hash >> 33;
	hash *= 0xc4ceb9fe1a85ec53ull;
	return hash ^ (hash >> 33);
}

// The slot of the prefix: the one holding it or the empty one ending its
// probe sequence.
static inline uint64_t
classify_prefixes_slot(
	const struct classify_prefixes *prefixes, uint64_t from, uint64_t len
) {
	uint64_t mask = prefixes->slot_count - 1;
	uint64_t slot = classify_prefix_hash(from, len) & mask;
	while (prefixes->slots[slot] != 0) {
		const struct classify_prefix *item =
			prefixes->items + prefixes->slots[slot] - 1;
		if (item->from == from && item->len == len) {
			break;
		}
		slot = (slot + 1) & mask;
	}
	return slot;
}

// Doubles the slots, keeping them at most half full, and reindexes the
// items.
static inline int
classify_prefixes_grow(
	struct memory_context *memory_context,
	struct classify_prefixes *prefixes
) {
	uint64_t slot_count =
		prefixes->slot_count ? 2 * prefixes->slot_count : 64;
	uint32_t *slots =
		memory_balloc(memory_context, slot_count * sizeof(uint32_t));
	if (slots == NULL) {
		return -1;
	}
	memset(slots, 0, slot_count * sizeof(uint32_t));

	if (prefixes->slot_count != 0) {
		memory_bfree(
			memory_context,
			prefixes->slots,
			prefixes->slot_count * sizeof(uint32_t)
		);
	}
	prefixes->slots = slots;
	prefixes->slot_count = slot_count;

	for (uint64_t idx = 0; idx < prefixes->count; ++idx) {
		const struct classify_prefix *item = prefixes->items + idx;
		slots[classify_prefixes_slot(prefixes, item->from, item->len)] =
			idx + 1;
	}
	return 0;
}

/*
 * Adds a prefix unless the set already holds it; the start must carry no
 * bits past the length.
 */
static inline int
classify_prefixes_add(
	struct memory_context *memory_context,
	struct classify_prefixes *prefixes,
	uint64_t from,
	uint64_t len
) {
	if (2 * (prefixes->count + 1) > prefixes->slot_count &&
	    classify_prefixes_grow(memory_context, prefixes)) {
		return -1;
	}

	uint64_t slot = classify_prefixes_slot(prefixes, from, len);
	if (prefixes->slots[slot] != 0) {
		return 0;
	}
	if (prefixes->count >= CLASSIFY_PREFIXES_MAX) {
		return -1;
	}

	if (mem_array_expand_exp(
		    memory_context,
		    (void **)&prefixes->items,
		    sizeof(*prefixes->items),
		    &prefixes->count
	    )) {
		return -1;
	}

	prefixes->items[prefixes->count - 1] =
		(struct classify_prefix){from, len};
	prefixes->slots[slot] = prefixes->count;
	return 0;
}

static inline void
classify_prefixes_free(
	struct memory_context *memory_context,
	struct classify_prefixes *prefixes
) {
	mem_array_free_exp(
		memory_context,
		prefixes->items,
		sizeof(*prefixes->items),
		prefixes->count
	);
	if (prefixes->slot_count != 0) {
		memory_bfree(
			memory_context,
			prefixes->slots,
			prefixes->slot_count * sizeof(uint32_t)
		);
	}
	memset(prefixes, 0, sizeof(*prefixes));
}

/*
 * Moves the prefixes into the destination stably ordered by the byte of
 * the start at the shift, or by the length when the shift is negative.
 *
 * Returns zero without moving anything when every prefix falls into one
 * bucket: the order is already the one the pass would produce.
 */
static inline int
classify_prefixes_sort_pass(
	const struct classify_prefix *src,
	struct classify_prefix *dst,
	uint64_t count,
	int shift
) {
	uint64_t buckets[256];
	memset(buckets, 0, sizeof(buckets));

	for (uint64_t idx = 0; idx < count; ++idx) {
		uint8_t digit =
			shift < 0 ? src[idx].len : src[idx].from >> shift;
		++buckets[digit];
	}

	uint64_t offset = 0;
	for (uint32_t digit = 0; digit < 256; ++digit) {
		if (buckets[digit] == count) {
			return 0;
		}
		uint64_t size = buckets[digit];
		buckets[digit] = offset;
		offset += size;
	}

	for (uint64_t idx = 0; idx < count; ++idx) {
		uint8_t digit =
			shift < 0 ? src[idx].len : src[idx].from >> shift;
		dst[buckets[digit]++] = src[idx];
	}

	return 1;
}

/*
 * Sorts the prefixes by start, a shorter prefix first on a shared start.
 *
 * A least significant digit radix sort: one pass over the length, then
 * one per start byte from the lowest.
 */
static inline int
classify_prefixes_sort(
	struct memory_context *memory_context,
	struct classify_prefixes *prefixes,
	uint8_t key_bits
) {
	uint64_t count = prefixes->count;
	if (count < 2) {
		return 0;
	}

	struct classify_prefix *scratch = memory_balloc(
		memory_context, count * sizeof(struct classify_prefix)
	);
	if (scratch == NULL) {
		return -1;
	}

	struct classify_prefix *src = prefixes->items;
	struct classify_prefix *dst = scratch;
	for (int shift = -8; shift < key_bits; shift += 8) {
		if (classify_prefixes_sort_pass(src, dst, count, shift)) {
			struct classify_prefix *swap = src;
			src = dst;
			dst = swap;
		}
	}
	if (src != prefixes->items) {
		memcpy(prefixes->items,
		       src,
		       count * sizeof(struct classify_prefix));
	}

	memory_bfree(
		memory_context, scratch, count * sizeof(struct classify_prefix)
	);

	return 0;
}

/*
 * The sweep state: the stack of the prefixes enclosing the current
 * position, outermost first, with the region value of each, and the
 * first key not yet emitted.
 */
struct classify_partition_sweep {
	struct classify_partition *partition;
	uint64_t pos;
	uint32_t depth;
	uint64_t to[65];
	uint32_t value[65];
};

// Emits the piece from the current position up to the key into the
// region of the innermost open prefix.
static inline void
classify_partition_sweep_emit(
	struct classify_partition_sweep *sweep, uint64_t to
) {
	struct classify_partition *partition = sweep->partition;
	uint32_t *value = &sweep->value[sweep->depth - 1];
	if (*value == CLASSIFY_PARTITION_INVALID) {
		*value = partition->region_count++;
	}

	partition->starts[partition->count] = sweep->pos;
	partition->values[partition->count] = *value;
	++partition->count;

	// The piece ending at the last key is the final one, so the
	// position past it is never read.
	sweep->pos = to + 1;
}

/*
 * Builds the partition of the prefix set; the items are sorted in place,
 * so the set takes no further prefixes.
 *
 * The sweep visits the prefixes in order, closing every open prefix that
 * ends before the next start and emitting the gap up to it into the
 * innermost still open one. A prefix ending where the innermost open one
 * ends shares its stack slot, and the slot starts a fresh region for the
 * remainder. A prefix covering the whole domain lands there before the
 * slot emits any piece, so it changes nothing.
 */
static inline int
classify_partition_build(
	struct memory_context *memory_context,
	uint8_t key_bits,
	struct classify_prefixes *prefixes,
	struct classify_partition *partition
) {
	memset(partition, 0, sizeof(*partition));
	partition->key_bits = key_bits;

	// The sort moves the items away from their slots: release the slots
	// before the sort scratch and the piece arrays take memory.
	if (prefixes->slot_count != 0) {
		memory_bfree(
			memory_context,
			prefixes->slots,
			prefixes->slot_count * sizeof(uint32_t)
		);
		prefixes->slots = NULL;
		prefixes->slot_count = 0;
	}

	if (classify_prefixes_sort(memory_context, prefixes, key_bits)) {
		return -1;
	}

	// Every prefix opens at most one piece before its start and closes
	// at most one at its end, beside the closing of the whole domain.
	uint64_t capacity = 2 * prefixes->count + 1;
	partition->starts =
		memory_balloc(memory_context, capacity * sizeof(uint64_t));
	partition->values =
		memory_balloc(memory_context, capacity * sizeof(uint32_t));
	partition->capacity = capacity;
	if (partition->starts == NULL || partition->values == NULL) {
		goto error;
	}

	struct classify_partition_sweep sweep;
	sweep.partition = partition;
	sweep.pos = 0;
	sweep.depth = 1;
	sweep.to[0] = classify_key_max(key_bits);
	sweep.value[0] = CLASSIFY_PARTITION_INVALID;

	for (uint64_t idx = 0; idx < prefixes->count; ++idx) {
		uint64_t from = prefixes->items[idx].from;
		uint64_t to = classify_prefix_to(
			key_bits, from, prefixes->items[idx].len
		);

		while (sweep.to[sweep.depth - 1] < from) {
			classify_partition_sweep_emit(
				&sweep, sweep.to[sweep.depth - 1]
			);
			--sweep.depth;
		}
		if (sweep.pos < from) {
			classify_partition_sweep_emit(&sweep, from - 1);
		}

		if (sweep.to[sweep.depth - 1] == to) {
			sweep.value[sweep.depth - 1] =
				CLASSIFY_PARTITION_INVALID;
			continue;
		}
		sweep.to[sweep.depth] = to;
		sweep.value[sweep.depth] = CLASSIFY_PARTITION_INVALID;
		++sweep.depth;
	}

	while (sweep.depth > 0) {
		classify_partition_sweep_emit(
			&sweep, sweep.to[sweep.depth - 1]
		);
		--sweep.depth;
	}

	return 0;

error:
	if (partition->starts != NULL) {
		memory_bfree(
			memory_context,
			partition->starts,
			capacity * sizeof(uint64_t)
		);
	}
	if (partition->values != NULL) {
		memory_bfree(
			memory_context,
			partition->values,
			capacity * sizeof(uint32_t)
		);
	}
	memset(partition, 0, sizeof(*partition));
	return -1;
}

static inline void
classify_partition_free(
	struct memory_context *memory_context,
	struct classify_partition *partition
) {
	if (partition->capacity != 0) {
		memory_bfree(
			memory_context,
			partition->starts,
			partition->capacity * sizeof(uint64_t)
		);
		memory_bfree(
			memory_context,
			partition->values,
			partition->capacity * sizeof(uint32_t)
		);
	}
	memset(partition, 0, sizeof(*partition));
}

// The index of the first piece starting at or after the key.
static inline uint32_t
classify_partition_lower(
	const struct classify_partition *partition, uint64_t key
) {
	uint32_t low = 0;
	uint32_t high = partition->count;
	while (low < high) {
		uint32_t mid = low + (high - low) / 2;
		if (partition->starts[mid] < key) {
			low = mid + 1;
		} else {
			high = mid;
		}
	}
	return low;
}

/*
 * Resolves the index range [start, stop) of the pieces the inclusive key
 * interval covers; both interval ends must be piece boundaries.
 */
static inline void
classify_partition_bounds(
	const struct classify_partition *partition,
	uint64_t from,
	uint64_t to,
	uint32_t *start,
	uint32_t *stop
) {
	*start = classify_partition_lower(partition, from);
	*stop = to == classify_key_max(partition->key_bits)
			? partition->count
			: classify_partition_lower(partition, to + 1);
}

// The index of the piece starting exactly at the key, or the invalid
// marker when no piece starts there.
static inline uint32_t
classify_partition_find(
	const struct classify_partition *partition, uint64_t key
) {
	uint32_t idx = classify_partition_lower(partition, key);
	if (idx == partition->count || partition->starts[idx] != key) {
		return CLASSIFY_PARTITION_INVALID;
	}
	return idx;
}

/*
 * Builds the LPM of the partition with the caller values, one per piece
 * in piece order, into an initialised LPM.
 *
 * A piece carrying the invalid LPM value is left out of the LPM.
 */
static inline int
classify_partition_build_lpm(
	const struct classify_partition *partition,
	const uint32_t *values,
	struct lpm *lpm
) {
	uint8_t key_size = partition->key_bits / 8;
	uint64_t key_max = classify_key_max(partition->key_bits);

	for (uint32_t idx = 0; idx < partition->count; ++idx) {
		if (values[idx] == LPM_VALUE_INVALID) {
			continue;
		}

		uint64_t to = idx + 1 < partition->count
				      ? partition->starts[idx + 1] - 1
				      : key_max;
		uint8_t from_key[8];
		uint8_t to_key[8];
		classify_key_store(
			partition->starts[idx], partition->key_bits, from_key
		);
		classify_key_store(to, partition->key_bits, to_key);
		if (lpm_insert(lpm, key_size, from_key, to_key, values[idx])) {
			return -1;
		}
	}

	return 0;
}
