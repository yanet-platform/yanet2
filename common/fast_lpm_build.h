#pragma once

/*
 * Builds a frozen LPM from every insert at once.
 *
 * Inserts are staged in order in the LPM's memory; on overlap the later
 * insert wins, as with the page-tree LPM. The build turns them into sorted
 * disjoint ranges and writes the trie top-down into memory blocks of the
 * LPM's memory context, every node once, then releases the staged inserts.
 * Sorted disjoint input is taken as is; prefixes go through a radix sort
 * and one sweep; any other input through a sweep of sorted range ends.
 * Control plane only.
 */

#include <stdlib.h>

#include "fast_lpm.h"

typedef unsigned __int128 fast_lpm_u128;

// The staged inserts of an LPM in insert order.
struct fast_lpm_inserts {
	const fast_lpm_u128 *from;
	const fast_lpm_u128 *to;
	const uint32_t *value;
	size_t count;
	uint8_t key_size;
};

static inline fast_lpm_u128
fast_lpm_key_to_u128(uint8_t key_size, const uint8_t *key) {
	fast_lpm_u128 result = 0;
	for (uint8_t idx = 0; idx < key_size; ++idx) {
		result = result << 8 | key[idx];
	}
	return result;
}

static inline void
fast_lpm_u128_to_key(uint8_t key_size, fast_lpm_u128 value, uint8_t *key) {
	for (int idx = key_size - 1; idx >= 0; --idx) {
		key[idx] = (uint8_t)value;
		value >>= 8;
	}
}

// A disjoint range: from its start to the next range's start.
struct fast_lpm_range {
	fast_lpm_u128 start;
	uint32_t leaf;
};

static inline uint32_t
fast_lpm_leaf(uint32_t value) {
	return value << 1 | FAST_LPM_ENTRY_LEAF;
}

#define FAST_LPM_LEAF_INVALID UINT32_MAX

// Sorted disjoint ranges covering the key space from zero.
struct fast_lpm_ranges {
	struct fast_lpm_range *items;
	size_t count;
	fast_lpm_u128 next;
	bool done;
};

/*
 * Appends [next, upto] with the leaf, merging an equal neighbour.
 *
 * Nothing is appended when upto is below next or the key space is covered.
 */
static inline void
fast_lpm_ranges_emit(
	struct fast_lpm_ranges *ranges, fast_lpm_u128 upto, uint32_t leaf
) {
	if (ranges->done || upto < ranges->next) {
		return;
	}
	if (ranges->count == 0 ||
	    ranges->items[ranges->count - 1].leaf != leaf) {
		ranges->items[ranges->count++] =
			(struct fast_lpm_range){ranges->next, leaf};
	}
	ranges->next = upto + 1;
	if (ranges->next == 0) {
		ranges->done = true;
	}
}

static inline fast_lpm_u128
fast_lpm_key_max(uint8_t key_size) {
	return key_size == 16 ? ~(fast_lpm_u128)0
			      : ((fast_lpm_u128)1 << (8 * key_size)) - 1;
}

// Inserts that are sorted and disjoint already.
static inline bool
fast_lpm_inserts_sorted(const struct fast_lpm_inserts *inserts) {
	for (size_t idx = 1; idx < inserts->count; ++idx) {
		if (inserts->to[idx - 1] >= inserts->from[idx]) {
			return false;
		}
	}
	return true;
}

static inline void
fast_lpm_paint_sorted(
	const struct fast_lpm_inserts *inserts, struct fast_lpm_ranges *ranges
) {
	for (size_t idx = 0; idx < inserts->count; ++idx) {
		if (inserts->from[idx] > 0) {
			fast_lpm_ranges_emit(
				ranges,
				inserts->from[idx] - 1,
				FAST_LPM_LEAF_INVALID
			);
		}
		fast_lpm_ranges_emit(
			ranges,
			inserts->to[idx],
			fast_lpm_leaf(inserts->value[idx])
		);
	}
}

/*
 * The host bit count of an insert that is a prefix, -1 otherwise.
 *
 * A prefix spans a power of two keys and starts on a multiple of it.
 */
static inline int
fast_lpm_prefix_host_bits(fast_lpm_u128 from, fast_lpm_u128 to) {
	fast_lpm_u128 span = to - from;
	if ((span & (span + 1)) != 0 || (from & span) != 0) {
		return -1;
	}
	return __builtin_popcountll((uint64_t)span) +
	       __builtin_popcountll((uint64_t)(span >> 64));
}

/*
 * One LSD radix pass over 64-bit words on bits [shift, shift + 13).
 *
 * Stable: equal digits keep their order.
 */
#define FAST_LPM_RADIX_BITS 13

static inline void
fast_lpm_radix_pass(
	const uint64_t *src,
	uint64_t *dst,
	size_t count,
	unsigned shift,
	uint32_t *counts
) {
	const uint32_t digits = 1u << FAST_LPM_RADIX_BITS;
	memset(counts, 0, digits * sizeof(*counts));
	for (size_t idx = 0; idx < count; ++idx) {
		counts[(src[idx] >> shift) & (digits - 1)]++;
	}
	uint32_t sum = 0;
	for (uint32_t digit = 0; digit < digits; ++digit) {
		uint32_t c = counts[digit];
		counts[digit] = sum;
		sum += c;
	}
	for (size_t idx = 0; idx < count; ++idx) {
		dst[counts[(src[idx] >> shift) & (digits - 1)]++] = src[idx];
	}
}

// An open prefix of the sweep: its last key and the latest insert over it.
struct fast_lpm_open {
	fast_lpm_u128 end;
	uint32_t seq;
};

/*
 * Sweeps prefixes sorted by (start, length, insert order).
 *
 * Prefixes nest or are disjoint, so in this order each lies inside the
 * open ones on the stack. Every stack entry carries the latest insert among
 * itself and the prefixes it lies in: that insert's value covers the keys
 * of the entry outside its nested prefixes.
 */
struct fast_lpm_sweep {
	const struct fast_lpm_inserts *inserts;
	struct fast_lpm_ranges *ranges;
	struct fast_lpm_open *stack;
	size_t depth;
};

static inline void
fast_lpm_sweep_close_before(struct fast_lpm_sweep *sweep, fast_lpm_u128 at) {
	while (sweep->depth && sweep->stack[sweep->depth - 1].end < at) {
		struct fast_lpm_open *top = &sweep->stack[sweep->depth - 1];
		fast_lpm_ranges_emit(
			sweep->ranges,
			top->end,
			fast_lpm_leaf(sweep->inserts->value[top->seq])
		);
		sweep->depth--;
	}
}

static inline void
fast_lpm_sweep_open(
	struct fast_lpm_sweep *sweep,
	fast_lpm_u128 start,
	fast_lpm_u128 end,
	uint32_t seq
) {
	fast_lpm_sweep_close_before(sweep, start);
	if (start > 0) {
		uint32_t leaf = FAST_LPM_LEAF_INVALID;
		if (sweep->depth) {
			leaf = fast_lpm_leaf(
				sweep->inserts->value
					[sweep->stack[sweep->depth - 1].seq]
			);
		}
		fast_lpm_ranges_emit(sweep->ranges, start - 1, leaf);
	}
	if (sweep->depth && sweep->stack[sweep->depth - 1].seq > seq) {
		seq = sweep->stack[sweep->depth - 1].seq;
	}
	sweep->stack[sweep->depth++] = (struct fast_lpm_open){end, seq};
}

static inline void
fast_lpm_sweep_finish(struct fast_lpm_sweep *sweep, fast_lpm_u128 max) {
	while (sweep->depth) {
		struct fast_lpm_open *top = &sweep->stack[sweep->depth - 1];
		fast_lpm_ranges_emit(
			sweep->ranges,
			top->end,
			fast_lpm_leaf(sweep->inserts->value[top->seq])
		);
		sweep->depth--;
	}
	fast_lpm_ranges_emit(sweep->ranges, max, FAST_LPM_LEAF_INVALID);
}

/*
 * Paints 4-byte prefixes: one word per insert, start << 30 | length << 24
 * | insert order, sorted on the start and length bits; the order bits are
 * already ascending and the passes are stable.
 */
static inline int
fast_lpm_paint_prefixes4(
	const struct fast_lpm_inserts *inserts, struct fast_lpm_ranges *ranges
) {
	size_t count = inserts->count;
	uint64_t *words = malloc(count * sizeof(*words));
	uint64_t *scratch = malloc(count * sizeof(*scratch));
	uint32_t *counts = malloc(sizeof(*counts) << FAST_LPM_RADIX_BITS);
	struct fast_lpm_open *stack = malloc((count + 1) * sizeof(*stack));
	int rc = -1;
	if (words == NULL || scratch == NULL || counts == NULL ||
	    stack == NULL) {
		errno = ENOMEM;
		goto out;
	}
	for (size_t idx = 0; idx < count; ++idx) {
		int host_bits = fast_lpm_prefix_host_bits(
			inserts->from[idx], inserts->to[idx]
		);
		words[idx] = (uint64_t)inserts->from[idx] << 30 |
			     (uint64_t)(32 - host_bits) << 24 | idx;
	}
	for (unsigned shift = 24; shift < 62; shift += FAST_LPM_RADIX_BITS) {
		fast_lpm_radix_pass(words, scratch, count, shift, counts);
		uint64_t *swap = words;
		words = scratch;
		scratch = swap;
	}
	struct fast_lpm_sweep sweep = {inserts, ranges, stack, 0};
	for (size_t idx = 0; idx < count; ++idx) {
		uint64_t word = words[idx];
		fast_lpm_u128 start = word >> 30;
		uint32_t len = (word >> 24) & 0x3f;
		fast_lpm_u128 end = start | ((1ull << (32 - len)) - 1);
		fast_lpm_sweep_open(
			&sweep, start, end, (uint32_t)(word & 0xffffff)
		);
	}
	fast_lpm_sweep_finish(&sweep, fast_lpm_key_max(4));
	rc = 0;
out:
	free(words);
	free(scratch);
	free(counts);
	free(stack);
	return rc;
}

/*
 * Paints prefixes of any key size: insert indices sorted by (start,
 * length) with stable LSD passes over 13-bit digits of a key word, the
 * length first, each start word from the low one; a digit equal on every
 * insert costs no pass.
 */
static inline int
fast_lpm_paint_prefixes(
	const struct fast_lpm_inserts *inserts, struct fast_lpm_ranges *ranges
) {
	size_t count = inserts->count;
	uint8_t key_size = inserts->key_size;
	// Per insert: index << 32 | digit word, re-keyed before every pass.
	uint64_t *order = malloc(count * sizeof(*order));
	uint64_t *scratch = malloc(count * sizeof(*scratch));
	uint8_t *len = malloc(count);
	uint32_t *counts = malloc(sizeof(*counts) << FAST_LPM_RADIX_BITS);
	struct fast_lpm_open *stack = malloc((count + 1) * sizeof(*stack));
	int rc = -1;
	if (order == NULL || scratch == NULL || len == NULL || counts == NULL ||
	    stack == NULL) {
		errno = ENOMEM;
		goto out;
	}
	for (size_t idx = 0; idx < count; ++idx) {
		len[idx] = (uint8_t)(8 * key_size - fast_lpm_prefix_host_bits(
							    inserts->from[idx],
							    inserts->to[idx]
						    ));
		order[idx] = (uint64_t)idx << 32;
	}
	// Digit positions from the least significant: the length, then the
	// start bits from the lowest.
	unsigned start_bits = 8 * key_size;
	for (int pass = -1; pass * FAST_LPM_RADIX_BITS < (int)start_bits;
	     ++pass) {
		uint32_t first = UINT32_MAX;
		bool same = true;
		for (size_t idx = 0; idx < count; ++idx) {
			uint32_t pos = (uint32_t)(order[idx] >> 32);
			uint32_t digit;
			if (pass < 0) {
				digit = len[pos];
			} else {
				digit = (uint32_t)(inserts->from[pos] >>
						   (pass * FAST_LPM_RADIX_BITS)
					) &
					((1u << FAST_LPM_RADIX_BITS) - 1);
			}
			order[idx] = (uint64_t)pos << 32 | digit;
			if (first == UINT32_MAX) {
				first = digit;
			}
			same &= digit == first;
		}
		if (same) {
			continue;
		}
		fast_lpm_radix_pass(order, scratch, count, 0, counts);
		uint64_t *swap = order;
		order = scratch;
		scratch = swap;
	}
	struct fast_lpm_sweep sweep = {inserts, ranges, stack, 0};
	for (size_t idx = 0; idx < count; ++idx) {
		uint32_t pos = (uint32_t)(order[idx] >> 32);
		fast_lpm_sweep_open(
			&sweep, inserts->from[pos], inserts->to[pos], pos
		);
	}
	fast_lpm_sweep_finish(&sweep, fast_lpm_key_max(key_size));
	rc = 0;
out:
	free(order);
	free(scratch);
	free(len);
	free(counts);
	free(stack);
	return rc;
}

// A range end of the general sweep: a start, or one past an end.
struct fast_lpm_event {
	fast_lpm_u128 at;
	// The insert order; the top bit marks an end.
	uint32_t seq;
};

#define FAST_LPM_EVENT_END 0x80000000u

static inline int
fast_lpm_event_cmp(const void *left, const void *right) {
	const struct fast_lpm_event *l = left;
	const struct fast_lpm_event *r = right;
	return l->at < r->at ? -1 : l->at > r->at;
}

// A max-heap of the insert orders covering the sweep position.
struct fast_lpm_heap {
	uint32_t *items;
	size_t count;
};

static inline void
fast_lpm_heap_push(struct fast_lpm_heap *heap, uint32_t seq) {
	size_t idx = heap->count++;
	while (idx && heap->items[(idx - 1) / 2] < seq) {
		heap->items[idx] = heap->items[(idx - 1) / 2];
		idx = (idx - 1) / 2;
	}
	heap->items[idx] = seq;
}

static inline void
fast_lpm_heap_pop(struct fast_lpm_heap *heap) {
	uint32_t last = heap->items[--heap->count];
	size_t idx = 0;
	for (;;) {
		size_t child = 2 * idx + 1;
		if (child >= heap->count) {
			break;
		}
		if (child + 1 < heap->count &&
		    heap->items[child + 1] > heap->items[child]) {
			child++;
		}
		if (heap->items[child] <= last) {
			break;
		}
		heap->items[idx] = heap->items[child];
		idx = child;
	}
	heap->items[idx] = last;
}

/*
 * Paints arbitrary inserts: at every range end the latest insert still
 * covering the key wins; ended inserts leave the heap lazily.
 */
static inline int
fast_lpm_paint_general(
	const struct fast_lpm_inserts *inserts, struct fast_lpm_ranges *ranges
) {
	size_t count = inserts->count;
	fast_lpm_u128 max = fast_lpm_key_max(inserts->key_size);
	struct fast_lpm_event *events = malloc(2 * count * sizeof(*events));
	bool *ended = calloc(count, sizeof(*ended));
	struct fast_lpm_heap heap = {malloc(count * sizeof(uint32_t)), 0};
	int rc = -1;
	if (events == NULL || ended == NULL || heap.items == NULL) {
		errno = ENOMEM;
		goto out;
	}
	size_t event_count = 0;
	for (size_t idx = 0; idx < count; ++idx) {
		events[event_count++] = (struct fast_lpm_event
		){inserts->from[idx], (uint32_t)idx};
		if (inserts->to[idx] != max) {
			events[event_count++] = (struct fast_lpm_event
			){inserts->to[idx] + 1,
			  (uint32_t)idx | FAST_LPM_EVENT_END};
		}
	}
	qsort(events, event_count, sizeof(*events), fast_lpm_event_cmp);
	ranges->items[ranges->count++] =
		(struct fast_lpm_range){0, FAST_LPM_LEAF_INVALID};
	for (size_t idx = 0; idx < event_count;) {
		fast_lpm_u128 at = events[idx].at;
		for (; idx < event_count && events[idx].at == at; ++idx) {
			uint32_t seq = events[idx].seq & ~FAST_LPM_EVENT_END;
			if (events[idx].seq & FAST_LPM_EVENT_END) {
				ended[seq] = true;
			} else {
				fast_lpm_heap_push(&heap, seq);
			}
		}
		while (heap.count && ended[heap.items[0]]) {
			fast_lpm_heap_pop(&heap);
		}
		uint32_t leaf = FAST_LPM_LEAF_INVALID;
		if (heap.count) {
			leaf = fast_lpm_leaf(inserts->value[heap.items[0]]);
		}
		struct fast_lpm_range *tail = &ranges->items[ranges->count - 1];
		if (tail->start == at) {
			tail->leaf = leaf;
		} else {
			ranges->items[ranges->count++] =
				(struct fast_lpm_range){at, leaf};
		}
		// A range taking its predecessor's leaf merges into it.
		if (ranges->count >= 2 &&
		    ranges->items[ranges->count - 1].leaf ==
			    ranges->items[ranges->count - 2].leaf) {
			ranges->count--;
		}
	}
	rc = 0;
out:
	free(events);
	free(ended);
	free(heap.items);
	return rc;
}

/*
 * The disjoint ranges of the inserts, covering the whole key space.
 *
 * The caller frees the items.
 */
static inline int
fast_lpm_inserts_ranges(
	const struct fast_lpm_inserts *inserts, struct fast_lpm_ranges *ranges
) {
	memset(ranges, 0, sizeof(*ranges));
	ranges->items =
		malloc((2 * inserts->count + 2) * sizeof(*ranges->items));
	if (ranges->items == NULL) {
		errno = ENOMEM;
		return -1;
	}
	int rc = 0;
	if (fast_lpm_inserts_sorted(inserts)) {
		fast_lpm_paint_sorted(inserts, ranges);
		fast_lpm_ranges_emit(
			ranges,
			fast_lpm_key_max(inserts->key_size),
			FAST_LPM_LEAF_INVALID
		);
	} else {
		bool prefixes = inserts->count < (1u << 24);
		for (size_t idx = 0; prefixes && idx < inserts->count; ++idx) {
			prefixes = fast_lpm_prefix_host_bits(
					   inserts->from[idx], inserts->to[idx]
				   ) >= 0;
		}
		if (prefixes && inserts->key_size == 4) {
			rc = fast_lpm_paint_prefixes4(inserts, ranges);
		} else if (prefixes) {
			rc = fast_lpm_paint_prefixes(inserts, ranges);
		} else {
			rc = fast_lpm_paint_general(inserts, ranges);
		}
	}
	if (rc) {
		free(ranges->items);
		ranges->items = NULL;
	}
	return rc;
}

/*
 * Trie emission: nodes go into memory blocks of power-of-two sizes; a node
 * never straddles two blocks.
 *
 * A sizing pass makes the same node decisions as the emission and gives
 * the trie size; each new block is the largest power of two the remaining
 * nodes fill, so the blocks hold the trie with little slack.
 */
#define FAST_LPM_CHUNK_MIN (2u << 10)
#define FAST_LPM_CHUNK_MAX (32u << 20)
// The link distance limit in 64-byte units: a signed 29-bit field.
#define FAST_LPM_LINK_LIMIT (1ll << 28)

struct fast_lpm_size {
	const struct fast_lpm_range *ranges;
	size_t full;
	size_t buckets;
};

static void
fast_lpm_size_subtree(
	struct fast_lpm_size *size,
	size_t first,
	size_t last,
	fast_lpm_u128 lo,
	uint8_t suffix
);

static inline void
fast_lpm_size_full(
	struct fast_lpm_size *size,
	size_t first,
	size_t last,
	fast_lpm_u128 lo,
	uint8_t suffix
) {
	const struct fast_lpm_range *ranges = size->ranges;
	size->full++;
	unsigned shift = 8 * (suffix - 1);
	size_t cur = first;
	uint32_t slot = 0;
	while (slot < 256) {
		fast_lpm_u128 slot_lo = lo + ((fast_lpm_u128)slot << shift);
		while (cur + 1 < last && ranges[cur + 1].start <= slot_lo) {
			cur++;
		}
		uint32_t end = 256;
		if (cur + 1 < last) {
			end = (uint32_t)((ranges[cur + 1].start - lo) >> shift);
		}
		if (end > slot) {
			slot = end;
			continue;
		}
		size_t next = cur + 1;
		fast_lpm_u128 slot_hi = slot_lo + ((fast_lpm_u128)1 << shift);
		while (next < last &&
		       (slot == 255 || ranges[next].start < slot_hi)) {
			next++;
		}
		fast_lpm_size_subtree(size, cur, next, slot_lo, suffix - 1);
		slot++;
		cur = next - 1;
	}
}

static void
fast_lpm_size_subtree(
	struct fast_lpm_size *size,
	size_t first,
	size_t last,
	fast_lpm_u128 lo,
	uint8_t suffix
) {
	uint32_t bounds = (uint32_t)(last - first - 1);
	if (bounds == 0) {
		return;
	}
	uint32_t bucket_bounds =
		suffix > 8 ? FAST_LPM_WIDE_BOUNDS : FAST_LPM_NARROW_BOUNDS;
	if (bounds <= bucket_bounds) {
		size->buckets++;
		return;
	}
	fast_lpm_size_full(size, first, last, lo, suffix);
}

struct fast_lpm_emit {
	struct fast_lpm *lpm;
	const struct fast_lpm_range *ranges;
	// The blocks of the new trie, linked through their heads.
	struct fast_lpm_chunk *chunks;
	struct fast_lpm_chunk *chunk;
	size_t used;
	// Node bytes not placed yet.
	size_t remaining;
	uint8_t key_size;
};

static inline uint8_t *
fast_lpm_emit_alloc(struct fast_lpm_emit *emit, size_t size) {
	if (emit->chunk == NULL || emit->used + size > emit->chunk->size) {
		size_t want =
			(emit->remaining > size ? emit->remaining : size) +
			sizeof(struct fast_lpm_chunk);
		size_t chunk_size = FAST_LPM_CHUNK_MIN;
		while (chunk_size * 2 <= want && chunk_size < FAST_LPM_CHUNK_MAX
		) {
			chunk_size *= 2;
		}
		if (chunk_size < size + sizeof(struct fast_lpm_chunk)) {
			chunk_size *= 2;
		}
		struct fast_lpm_chunk *chunk =
			memory_balloc(&emit->lpm->memory_context, chunk_size);
		if (chunk == NULL) {
			errno = ENOMEM;
			return NULL;
		}
		chunk->size = chunk_size;
		SET_OFFSET_OF(&chunk->next, emit->chunks);
		emit->chunks = chunk;
		emit->chunk = chunk;
		emit->used = sizeof(*chunk);
	}
	uint8_t *node = (uint8_t *)emit->chunk + emit->used;
	emit->used += size;
	emit->remaining = emit->remaining > size ? emit->remaining - size : 0;
	return node;
}

// The link entry from a node to its child, 0 when out of reach.
static inline uint32_t
fast_lpm_link_entry(const uint8_t *node, const uint8_t *child, uint32_t kind) {
	int64_t units = (child - node) / FAST_LPM_NODE_ALIGN;
	if (units >= FAST_LPM_LINK_LIMIT || units < -FAST_LPM_LINK_LIMIT) {
		return 0;
	}
	return (uint32_t)(int32_t)units << 3 | kind;
}

/*
 * The entry for the subtree of the given suffix length starting at lo.
 *
 * Ranges [first, last) start inside the subtree, but the first one, which
 * may start before it and covers lo. A subtree of one range is a leaf, one
 * of a few boundaries a bucket and any other a full node; the root is
 * always full. Returns 0 on a failure with errno set.
 */
static uint32_t
fast_lpm_emit_subtree(
	struct fast_lpm_emit *emit,
	const uint8_t *parent,
	size_t first,
	size_t last,
	fast_lpm_u128 lo,
	uint8_t suffix
);

static inline uint32_t
fast_lpm_emit_bucket(
	struct fast_lpm_emit *emit,
	const uint8_t *parent,
	size_t first,
	size_t last,
	uint8_t suffix
) {
	const struct fast_lpm_range *ranges = emit->ranges;
	uint32_t bounds = (uint32_t)(last - first - 1);
	uint8_t *node = fast_lpm_emit_alloc(emit, FAST_LPM_BUCKET_SIZE);
	if (node == NULL) {
		return 0;
	}
	memset(node, 0, FAST_LPM_BUCKET_SIZE);
	uint32_t *values = (uint32_t *)node;
	if (suffix > 8) {
		for (uint32_t idx = 0; idx <= FAST_LPM_WIDE_BOUNDS; ++idx) {
			uint32_t pick = idx < bounds ? idx : bounds;
			values[idx] = ranges[first + pick].leaf;
		}
		uint64_t *b = (uint64_t *)(node + FAST_LPM_WIDE_BOUNDS_OFFSET);
		for (uint32_t idx = 0; idx < FAST_LPM_WIDE_BOUNDS; ++idx) {
			fast_lpm_u128 bound =
				idx < bounds ? ranges[first + 1 + idx].start
					     : ~(fast_lpm_u128)0;
			b[2 * idx] = (uint64_t)(bound >> 64);
			b[2 * idx + 1] = (uint64_t)bound;
		}
		uint32_t entry =
			fast_lpm_link_entry(parent, node, FAST_LPM_KIND_WIDE);
		if (entry == 0) {
			errno = ERANGE;
		}
		return entry;
	}
	for (uint32_t idx = 0; idx <= FAST_LPM_NARROW_BOUNDS; ++idx) {
		uint32_t pick = idx < bounds ? idx : bounds;
		values[FAST_LPM_NARROW_BOUNDS - idx] =
			ranges[first + pick].leaf;
	}
	uint8_t *b = node + FAST_LPM_NARROW_BOUNDS_OFFSET;
	for (uint32_t idx = 0; idx < FAST_LPM_NARROW_BOUNDS; ++idx) {
		fast_lpm_u128 bound = idx < bounds
					      ? ranges[first + 1 + idx].start
					      : ~(fast_lpm_u128)0;
		if (emit->key_size == 4) {
			((uint32_t *)b)[idx] = (uint32_t)bound ^ 0x80000000u;
		} else {
			((uint64_t *)b)[idx] =
				(uint64_t)bound ^ 0x8000000000000000ull;
		}
	}
	uint32_t entry =
		fast_lpm_link_entry(parent, node, FAST_LPM_KIND_NARROW);
	if (entry == 0) {
		errno = ERANGE;
	}
	return entry;
}

static inline uint8_t *
fast_lpm_emit_full(
	struct fast_lpm_emit *emit,
	size_t first,
	size_t last,
	fast_lpm_u128 lo,
	uint8_t suffix
) {
	const struct fast_lpm_range *ranges = emit->ranges;
	uint8_t *node = fast_lpm_emit_alloc(emit, FAST_LPM_FULL_SIZE);
	if (node == NULL) {
		return NULL;
	}
	uint32_t *entries = (uint32_t *)node;
	unsigned shift = 8 * (suffix - 1);
	size_t cur = first;
	uint32_t slot = 0;
	while (slot < 256) {
		fast_lpm_u128 slot_lo = lo + ((fast_lpm_u128)slot << shift);
		while (cur + 1 < last && ranges[cur + 1].start <= slot_lo) {
			cur++;
		}
		// Slots before the next range start hold the current leaf.
		uint32_t end = 256;
		if (cur + 1 < last) {
			end = (uint32_t)((ranges[cur + 1].start - lo) >> shift);
		}
		if (end > slot) {
			for (; slot < end; ++slot) {
				entries[slot] = ranges[cur].leaf;
			}
			continue;
		}
		size_t next = cur + 1;
		fast_lpm_u128 slot_hi = slot_lo + ((fast_lpm_u128)1 << shift);
		while (next < last &&
		       (slot == 255 || ranges[next].start < slot_hi)) {
			next++;
		}
		uint32_t entry = fast_lpm_emit_subtree(
			emit, node, cur, next, slot_lo, suffix - 1
		);
		if (entry == 0) {
			return NULL;
		}
		entries[slot++] = entry;
		cur = next - 1;
	}
	return node;
}

static uint32_t
fast_lpm_emit_subtree(
	struct fast_lpm_emit *emit,
	const uint8_t *parent,
	size_t first,
	size_t last,
	fast_lpm_u128 lo,
	uint8_t suffix
) {
	uint32_t bounds = (uint32_t)(last - first - 1);
	if (bounds == 0) {
		return emit->ranges[first].leaf;
	}
	uint32_t bucket_bounds =
		suffix > 8 ? FAST_LPM_WIDE_BOUNDS : FAST_LPM_NARROW_BOUNDS;
	if (bounds <= bucket_bounds) {
		return fast_lpm_emit_bucket(emit, parent, first, last, suffix);
	}
	uint8_t *node = fast_lpm_emit_full(emit, first, last, lo, suffix);
	if (node == NULL) {
		return 0;
	}
	uint32_t entry = fast_lpm_link_entry(parent, node, FAST_LPM_KIND_FULL);
	if (entry == 0) {
		errno = ERANGE;
	}
	return entry;
}

/*
 * Writes the trie of the ranges and makes it the LPM's.
 *
 * The previous trie is released on success and kept on a failure.
 */
static inline int
fast_lpm_emit_trie(
	struct fast_lpm *lpm, const struct fast_lpm_range *ranges, size_t count
) {
	struct fast_lpm_size size = {.ranges = ranges};
	fast_lpm_size_full(&size, 0, count, 0, lpm->key_size);
	struct fast_lpm_emit emit = {
		.lpm = lpm,
		.ranges = ranges,
		.remaining = size.full * FAST_LPM_FULL_SIZE +
			     size.buckets * FAST_LPM_BUCKET_SIZE,
		.key_size = lpm->key_size,
	};
	uint8_t *root = fast_lpm_emit_full(&emit, 0, count, 0, lpm->key_size);
	if (root == NULL) {
		fast_lpm_free_chunks(&lpm->memory_context, emit.chunks);
		return -1;
	}
	fast_lpm_free_chunks(&lpm->memory_context, ADDR_OF(&lpm->chunks));
	SET_OFFSET_OF(&lpm->chunks, emit.chunks);
	SET_OFFSET_OF(&lpm->root, root);
	return 0;
}

/*
 * Prepares an LPM that maps every key to the invalid value.
 *
 * Inserts are staged until the build. Fails with EINVAL for a key size
 * other than 4, 8, 12 or 16 bytes and with ENOMEM; the LPM is safe to free
 * either way.
 */
static inline int
fast_lpm_init(
	struct fast_lpm *lpm,
	struct memory_context *memory_context,
	const char *name,
	uint8_t key_size
) {
	memset(lpm, 0, sizeof(*lpm));
	if (!fast_lpm_key_size_valid(key_size)) {
		errno = EINVAL;
		return -1;
	}
	memory_context_init_from(&lpm->memory_context, memory_context, name);
	lpm->key_size = key_size;
	struct fast_lpm_range all = {0, FAST_LPM_LEAF_INVALID};
	return fast_lpm_emit_trie(lpm, &all, 1);
}

// Grows the staged insert arrays to the capacity.
static inline int
fast_lpm_grow_staged(struct fast_lpm *lpm, uint64_t capacity) {
	struct memory_context *context = &lpm->memory_context;
	uint8_t *from = memory_balloc(context, capacity * 16);
	uint8_t *to = memory_balloc(context, capacity * 16);
	uint32_t *value = memory_balloc(context, capacity * sizeof(uint32_t));
	if (from == NULL || to == NULL || value == NULL) {
		memory_bfree(context, from, capacity * 16);
		memory_bfree(context, to, capacity * 16);
		memory_bfree(context, value, capacity * sizeof(uint32_t));
		errno = ENOMEM;
		return -1;
	}
	uint64_t count = lpm->staged_count;
	if (count) {
		memcpy(from, ADDR_OF(&lpm->staged_from), count * 16);
		memcpy(to, ADDR_OF(&lpm->staged_to), count * 16);
		memcpy(value,
		       ADDR_OF(&lpm->staged_value),
		       count * sizeof(uint32_t));
	}
	fast_lpm_free_staged(lpm);
	SET_OFFSET_OF(&lpm->staged_from, from);
	SET_OFFSET_OF(&lpm->staged_to, to);
	SET_OFFSET_OF(&lpm->staged_value, value);
	lpm->staged_count = count;
	lpm->staged_capacity = capacity;
	return 0;
}

/*
 * Stages the mapping of the keys of [from, to] to the value.
 *
 * On overlap the later insert wins. Keys are big-endian and of the LPM's
 * key size; a range whose start is past its end maps nothing. Fails with
 * EINVAL for a value at or above the invalid value, EBUSY once the LPM is
 * built, and ENOMEM.
 */
static inline int
fast_lpm_insert(
	struct fast_lpm *lpm,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
) {
	if (lpm->frozen) {
		errno = EBUSY;
		return -1;
	}
	if (value >= FAST_LPM_VALUE_INVALID) {
		errno = EINVAL;
		return -1;
	}
	fast_lpm_u128 lo = fast_lpm_key_to_u128(lpm->key_size, from);
	fast_lpm_u128 hi = fast_lpm_key_to_u128(lpm->key_size, to);
	if (lo > hi) {
		return 0;
	}
	if (lpm->staged_count == lpm->staged_capacity &&
	    fast_lpm_grow_staged(
		    lpm, lpm->staged_capacity ? lpm->staged_capacity * 2 : 16
	    )) {
		return -1;
	}
	uint64_t idx = lpm->staged_count++;
	memcpy(ADDR_OF(&lpm->staged_from) + 16 * idx, &lo, 16);
	memcpy(ADDR_OF(&lpm->staged_to) + 16 * idx, &hi, 16);
	ADDR_OF(&lpm->staged_value)[idx] = value;
	return 0;
}

/*
 * Compiles the staged inserts into the trie and freezes the LPM.
 *
 * The staged inserts are released on success. Fails with ENOMEM, or ERANGE
 * when memory blocks lie too far apart for a node link; on a failure the
 * LPM keeps its previous trie and staged inserts.
 */
static inline int
fast_lpm_build(struct fast_lpm *lpm) {
	if (lpm->frozen) {
		errno = EBUSY;
		return -1;
	}
	struct fast_lpm_inserts inserts = {
		.from = (const fast_lpm_u128 *)ADDR_OF(&lpm->staged_from),
		.to = (const fast_lpm_u128 *)ADDR_OF(&lpm->staged_to),
		.value = ADDR_OF(&lpm->staged_value),
		.count = lpm->staged_count,
		.key_size = lpm->key_size,
	};
	struct fast_lpm_ranges ranges;
	if (fast_lpm_inserts_ranges(&inserts, &ranges)) {
		return -1;
	}
	int rc = fast_lpm_emit_trie(lpm, ranges.items, ranges.count);
	free(ranges.items);
	if (rc) {
		return -1;
	}
	fast_lpm_free_staged(lpm);
	lpm->frozen = true;
	return 0;
}
