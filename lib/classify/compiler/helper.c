#include "lib/classify/compiler/helper.h"

#include "common/registry.h"
#include "lib/classify/classify.h"
#include "lib/classify/compiler/declare.h"
#include <stdint.h>
#include <stdlib.h>

int
classify_decode(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *cls,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	struct vline *rule_map
) {
	const struct value_registry *registry = &cls->registry;
	const uint32_t *rule_to_group = cls->rule_groups;
	if (vline_init(
		    rule_map,
		    memory_context,
		    name,
		    value_registry_capacity(registry)
	    )) {
		return -1;
	}

	for (uint32_t idx = 0; idx < rule_map->size; ++idx) {
		*vline_get_ptr(rule_map, idx) = FILTER_RULE_INVALID;
	}

	/*
	 * The same class may be produced by several rules, the first one
	 * wins to preserve the rule ordering.
	 */
	struct value_range *ranges = ADDR_OF(&registry->ranges);
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		// A rule absent from the ruleset this map belongs to holds no
		// group even when the classes were enumerated over a wider
		// shared ruleset.
		if (rules[rule_idx] == NULL) {
			continue;
		}

		uint32_t group_idx = rule_to_group[rule_idx];
		if (group_idx == FILTER_GROUP_INVALID) {
			continue;
		}

		struct value_range *range = ranges + group_idx;
		uint32_t *values = ADDR_OF(&range->values);

		for (uint32_t idx = 0; idx < range->count; ++idx) {
			uint32_t *rule_idx_ptr =
				vline_get_ptr(rule_map, values[idx]);
			if (*rule_idx_ptr == FILTER_RULE_INVALID) {
				*rule_idx_ptr = rule_idx;
			}
		}
	}

	return 0;
}

/*
 * The rule coverage classes of a stage's value space over a rule
 * projection.
 *
 * Every value covered by the same set of projected rules gains one
 * class id, numbered from one; zero stays the not covered mark. The
 * map lands in shared memory as a value line over the stage's value
 * space.
 *
 * The stage must be a join output: its classes are numbered from
 * one, so zero never carries coverage. An attribute side may hold
 * class zero inside a range, and this routine would drop that
 * coverage silently.
 */
int
classify_coverage_map(
	struct memory_context *memory_context,
	const struct classifier *stage,
	uint32_t rule_count,
	const uint8_t *rule_mask,
	struct vline *map
) {
	const struct value_registry *registry = &stage->registry;
	const uint32_t *rule_to_group = stage->rule_groups;
	uint32_t value_space = value_registry_capacity(registry);
	const struct value_range *ranges = ADDR_OF(&registry->ranges);

	if (value_space <= 1) {
		return vline_init(map, memory_context, "filter:compact", 1);
	}

	uint32_t *counts = (uint32_t *)calloc(value_space, sizeof(uint32_t));
	uint32_t *gen = (uint32_t *)calloc(value_space, sizeof(uint32_t));
	if (counts == NULL || gen == NULL) {
		free(counts);
		free(gen);
		return -1;
	}

	// Per projected rule coverage marks over the group ranges; the
	// generation stamp keeps a rule covering a value through
	// duplicate range entries counted once, and the ascending rule
	// order sorts the lists.
	uint32_t cur_gen = 0;
	uint32_t *offsets = NULL;
	uint32_t *filled = NULL;
	uint32_t *lists = NULL;
	for (uint32_t pass = 0; pass < 2; ++pass) {
		for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
			uint32_t group = rule_to_group[rule_idx];
			if (group == FILTER_GROUP_INVALID) {
				continue;
			}
			if (rule_mask != NULL && !rule_mask[rule_idx]) {
				continue;
			}
			const struct value_range *range = ranges + group;
			uint32_t *values = ADDR_OF(&range->values);
			++cur_gen;
			for (uint32_t idx = 0; idx < range->count; ++idx) {
				uint32_t value = values[idx];
				if (gen[value] == cur_gen) {
					continue;
				}
				gen[value] = cur_gen;
				if (pass == 0) {
					++counts[value];
				} else {
					uint32_t slot =
						offsets[value] + filled[value];
					lists[slot] = rule_idx;
					++filled[value];
				}
			}
		}
		if (pass == 1) {
			break;
		}
		uint64_t total = 0;
		for (uint32_t value = 0; value < value_space; ++value) {
			total += counts[value];
		}
		if (total > UINT32_MAX) {
			goto error_free;
		}
		offsets = (uint32_t *)calloc(value_space + 1, sizeof(uint32_t));
		filled = (uint32_t *)calloc(value_space, sizeof(uint32_t));
		lists = (uint32_t *)malloc(
			sizeof(uint32_t) * (total ? total : 1)
		);
		if (offsets == NULL || filled == NULL || lists == NULL) {
			goto error_free;
		}
		for (uint32_t value = 0; value < value_space; ++value) {
			offsets[value + 1] = offsets[value] + counts[value];
		}
	}

	if (vline_init(map, memory_context, "filter:compact", value_space)) {
		goto error_free;
	}

	// Hash-cons the sorted rule lists into the class ids.
	uint32_t tbl_cap = 4;
	while (tbl_cap < value_space * 2) {
		tbl_cap <<= 1;
	}
	uint64_t *keys = (uint64_t *)calloc(tbl_cap, sizeof(uint64_t));
	uint32_t *slot_rep = (uint32_t *)calloc(tbl_cap, sizeof(uint32_t));
	if (keys == NULL || slot_rep == NULL) {
		free(keys);
		free(slot_rep);
		goto error_free;
	}

	uint32_t new_count = 0;
	for (uint32_t value = 1; value < value_space; ++value) {
		if (counts[value] == 0) {
			continue;
		}
		const uint32_t *list = lists + offsets[value];
		uint32_t n = counts[value];

		uint64_t hash = 1469598103934665603ULL;
		for (uint32_t idx = 0; idx < n; ++idx) {
			hash ^= list[idx];
			hash *= 1099511628211ULL;
		}
		if (hash == 0) {
			hash = 1;
		}

		uint32_t slot = (uint32_t)hash & (tbl_cap - 1);
		uint32_t match = 0;
		while (keys[slot] != 0) {
			if (keys[slot] == hash) {
				uint32_t other = slot_rep[slot];
				if (counts[other] == n &&
				    memcmp(lists + offsets[other],
					   list,
					   n * sizeof(uint32_t)) == 0) {
					match = other;
					break;
				}
			}
			slot = (slot + 1) & (tbl_cap - 1);
		}
		if (match != 0) {
			*vline_get_ptr(map, value) = *vline_get_ptr(map, match);
			continue;
		}
		keys[slot] = hash;
		slot_rep[slot] = value;
		*vline_get_ptr(map, value) = ++new_count;
	}

	free(keys);
	free(slot_rep);
	free(lists);
	free(offsets);
	free(filled);
	free(counts);
	free(gen);
	return 0;

error_free:
	free(lists);
	free(offsets);
	free(filled);
	free(counts);
	free(gen);
	return -1;
}

/*
 * Join with the left classes compacted by a coverage map.
 *
 * The left registry is cloned with its range values mapped through
 * the map, the joint table spans the compacted classes (plus the
 * zero row for the uncovered rest), and the map is handed out for
 * the runtime class translation.
 */
int
classify_join_compact(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *left,
	const struct classifier *right,
	uint32_t rule_count,
	const struct vline *compact_map,
	struct value_table *table,
	struct classifier *out
) {
	const struct value_registry *registry1 = &left->registry;
	uint32_t group_count = registry1->range_count;

	struct classifier left_compact = {0};
	struct value_registry *compact_registry = &left_compact.registry;
	int rc = -1;

	uint32_t *compact_groups = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * (rule_count ? rule_count : 1)
	);
	if (compact_groups == NULL) {
		return -1;
	}
	memcpy(compact_groups, left->rule_groups, sizeof(uint32_t) * rule_count
	);
	left_compact.rule_groups = compact_groups;

	if (value_registry_init(
		    compact_registry, memory_context, "filter:compact"
	    )) {
		memory_bfree(
			memory_context,
			compact_groups,
			sizeof(uint32_t) * (rule_count ? rule_count : 1)
		);
		return -1;
	}

	const struct value_range *ranges = ADDR_OF(&registry1->ranges);
	for (uint32_t group_idx = 0; group_idx < group_count; ++group_idx) {
		if (value_registry_start(compact_registry)) {
			goto error_free_compact;
		}
		const uint32_t *values = ADDR_OF(&ranges[group_idx].values);
		for (uint32_t idx = 0; idx < ranges[group_idx].count; ++idx) {
			uint32_t compact_value = vline_get(
				(struct vline *)compact_map, values[idx]
			);
			if (compact_value == 0) {
				continue;
			}
			if (value_registry_collect(
				    compact_registry, compact_value
			    )) {
				goto error_free_compact;
			}
		}
	}

	if (classify_join(
		    memory_context,
		    name,
		    &left_compact,
		    right,
		    rule_count,
		    table,
		    out
	    )) {
		goto error_free_compact;
	}
	rc = 0;

error_free_compact:
	value_registry_fini(compact_registry);
	memory_bfree(
		memory_context,
		left_compact.rule_groups,
		sizeof(uint32_t) * (rule_count ? rule_count : 1)
	);
	return rc;
}

//////////////////////////////////////////////////////////////////////////////

static inline uint32_t
merge_group_pair_hash(uint32_t left, uint32_t right) {
	uint32_t hash = left * 2654435761u;
	hash ^= right * 2246822519u;
	return hash * 3266489917u;
}

struct merge_pair_ctx {
	const uint32_t *pair_group1;
	const uint32_t *pair_group2;
	uint32_t group1;
	uint32_t group2;
};

struct final_row_ctx {
	const uint32_t *const *distinct;
	const uint32_t *row;
	uint32_t h_dim;
};

/*
 * Heap open-addressing set of the join build.
 *
 * The shared-memory arena only carries what the finished table
 * publishes, so the pair and row interning scratch lives and dies with
 * the build on the heap.
 */
struct join_index {
	uint32_t *hashes;
	uint32_t *slots;
	uint32_t mask;
};

static inline int
join_index_init(struct join_index *index, uint32_t expected) {
	uint32_t capacity = 1;
	while (capacity < expected * 2) {
		capacity <<= 1;
	}
	index->hashes = calloc(capacity, sizeof(uint32_t));
	index->slots = malloc((size_t)capacity * sizeof(uint32_t));
	index->mask = capacity - 1;
	if (index->hashes == NULL || index->slots == NULL) {
		return -1;
	}
	return 0;
}

// A stored hash of zero marks the empty slot, so a real zero hash is
// stored as one.
//
// The capacity holds at least twice the expected count, so the probe
// below always terminates on an empty slot. The equality predicate
// returns non-zero to accept the slot payload, zero to keep probing.
static inline uint32_t
join_index_find(
	const struct join_index *index,
	uint32_t hash,
	int (*eq)(uint32_t slot, const void *data),
	const void *data
) {
	uint32_t stored = hash ? hash : 1;
	for (uint32_t slot = stored & index->mask;;
	     slot = (slot + 1) & index->mask) {
		if (index->hashes[slot] == 0) {
			return UINT32_MAX;
		}
		if (index->hashes[slot] == stored &&
		    eq(index->slots[slot], data)) {
			return index->slots[slot];
		}
	}
}

static inline void
join_index_put(struct join_index *index, uint32_t hash, uint32_t value) {
	uint32_t stored = hash ? hash : 1;
	uint32_t slot = stored & index->mask;
	while (index->hashes[slot] != 0) {
		slot = (slot + 1) & index->mask;
	}
	index->hashes[slot] = stored;
	index->slots[slot] = value;
}

// A stored row matches the candidate when every cell agrees.
static int
join_row_eq(uint32_t row, const void *data) {
	const struct final_row_ctx *ctx = data;
	return memcmp(ctx->distinct[row],
		      ctx->row,
		      (size_t)ctx->h_dim * sizeof(uint32_t)) == 0;
}

// A pair matches the stored one of its slot when both group sides agree.
static int
join_pair_eq(uint32_t pair, const void *data) {
	const struct merge_pair_ctx *ctx = data;
	return ctx->pair_group1[pair] == ctx->group1 &&
	       ctx->pair_group2[pair] == ctx->group2;
}

/*
 * The fingerprint of a set of distinct pairs of a join.
 *
 * Every pair carries a pseudo-random 128-bit fingerprint of a seed and
 * its index, and a set is the XOR of its members, so the empty set is
 * zero. The fingerprints are a fixed function of the input; modelled as
 * independent uniform values, two distinct sets collide with probability
 * 2^-128, and a join of N classes merges two of them with probability
 * below N^2 / 2^129. Distinct pairs never share a fingerprint and none
 * is zero, so a merge needs sets differing in at least three pairs.
 * The seeds are fixed so that every build of a ruleset reproduces; the
 * bound holds for rulesets not constructed against them, as an
 * operator's are, and a ruleset built against them could force a failure
 * or a merge the counts miss.
 */
struct join_fingerprint {
	uint64_t lo;
	uint64_t hi;
};

// Narrowed only by the tests that force merges of pair sets: in every
// attempt, or in the first one only.
#ifndef JOIN_FINGERPRINT_MASK
#define JOIN_FINGERPRINT_MASK UINT64_MAX
#endif
#ifndef JOIN_FIRST_ATTEMPT_MASK
#define JOIN_FIRST_ATTEMPT_MASK UINT64_MAX
#endif

// Two consecutive splitmix64 outputs of a seed and a pair, so a retry
// or a give-up repeats on the same input.
static inline struct join_fingerprint
join_pair_fingerprint(uint32_t pair, uint32_t seed) {
	uint64_t words[2];
	for (uint64_t i = 0; i < 2; ++i) {
		uint64_t x =
			((uint64_t)seed << 33 | (2 * (uint64_t)pair + i + 1)) *
			0x9e3779b97f4a7c15ULL;
		x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9ULL;
		x = (x ^ (x >> 27)) * 0x94d049bb133111ebULL;
		words[i] = x ^ (x >> 31);
	}
	uint64_t mask =
		seed == 0 ? JOIN_FINGERPRINT_MASK & JOIN_FIRST_ATTEMPT_MASK
			  : JOIN_FINGERPRINT_MASK;
	return (struct join_fingerprint){words[0] & mask, words[1] & mask};
}

static inline void
join_fingerprint_xor(struct join_fingerprint *to, struct join_fingerprint z) {
	to->lo ^= z.lo;
	to->hi ^= z.hi;
}

static inline bool
join_fingerprint_eq(struct join_fingerprint a, struct join_fingerprint b) {
	return a.lo == b.lo && a.hi == b.hi;
}

/*
 * Per group scratch of one join side, filled on the first use by a pair.
 *
 * The painted side of a group is the smaller of its values and their
 * complement over the dimension. A range holds distinct values below the
 * registry capacity, as its collector keeps.
 */
struct join_group {
	const uint32_t *small;
	uint32_t *complement;
	uint32_t small_count;
	bool flipped;
	bool ready;
};

static int
join_group_prepare(
	struct join_group *group,
	const struct value_range *range,
	uint32_t dim,
	uint8_t *marks
) {
	if (group->ready) {
		return 0;
	}
	const uint32_t *values = ADDR_OF(&range->values);
	uint32_t count = (uint32_t)range->count;
	group->small = values;
	group->small_count = count;
	if ((uint64_t)count * 2 > dim) {
		uint32_t size = dim - count;
		group->complement =
			malloc((size ? size : 1) * sizeof(uint32_t));
		if (group->complement == NULL) {
			return -1;
		}
		for (uint32_t idx = 0; idx < count; ++idx) {
			marks[values[idx]] = 1;
		}
		uint32_t unmarked = 0;
		for (uint32_t value = 0; value < dim; ++value) {
			if (!marks[value] && unmarked < size) {
				group->complement[unmarked++] = value;
			}
			marks[value] = 0;
		}
		group->small = group->complement;
		group->small_count = unmarked;
		group->flipped = true;
	}
	group->ready = true;
	return 0;
}

static int
join_value_cmp(const void *a, const void *b) {
	uint32_t x = *(const uint32_t *)a;
	uint32_t y = *(const uint32_t *)b;
	return (x > y) - (x < y);
}

// The values of one item on one side of a join: a pair's painted rows,
// or a class's first column.
struct join_span {
	const uint32_t *values;
	uint32_t count;
};

// The items, pairs or classes, holding each value of a dimension,
// ascending per value.
struct join_csr {
	size_t *offsets;
	uint32_t *items;
};

static int
join_csr_build(
	struct join_csr *csr,
	uint32_t dim,
	const struct join_span *spans,
	uint32_t pair_count
) {
	csr->offsets = calloc((size_t)dim + 1, sizeof(size_t));
	if (csr->offsets == NULL) {
		return -1;
	}
	for (uint32_t pair = 0; pair < pair_count; ++pair) {
		for (uint32_t idx = 0; idx < spans[pair].count; ++idx) {
			++csr->offsets[spans[pair].values[idx] + 1];
		}
	}
	for (uint32_t value = 0; value < dim; ++value) {
		csr->offsets[value + 1] += csr->offsets[value];
	}
	size_t total = csr->offsets[dim];
	csr->items = malloc((total ? total : 1) * sizeof(uint32_t));
	if (csr->items == NULL) {
		return -1;
	}
	// Filling advances every start to the next one, shifted back after.
	for (uint32_t pair = 0; pair < pair_count; ++pair) {
		for (uint32_t idx = 0; idx < spans[pair].count; ++idx) {
			csr->items[csr->offsets[spans[pair].values[idx]]++] =
				pair;
		}
	}
	for (uint32_t value = dim; value > 0; --value) {
		csr->offsets[value] = csr->offsets[value - 1];
	}
	csr->offsets[0] = 0;
	return 0;
}

static void
join_csr_free(struct join_csr *csr) {
	free(csr->items);
	free(csr->offsets);
	*csr = (struct join_csr){0};
}

// The seeds a join tries before it gives up on a merge of pair sets.
#define JOIN_SEED_ATTEMPTS 4

// A class of a join: its fingerprint, the first cell holding it and the
// count of its cells.
struct join_class {
	struct join_fingerprint fingerprint;
	uint32_t v;
	uint32_t h;
	uint64_t cells;
};

struct join_classes {
	struct join_class *items;
	struct join_index index;
	uint32_t count;
	uint32_t capacity;
};

struct join_class_ctx {
	const struct join_class *items;
	struct join_fingerprint fingerprint;
};

static int
join_class_eq(uint32_t id, const void *data) {
	const struct join_class_ctx *ctx = data;
	return join_fingerprint_eq(
		ctx->items[id].fingerprint, ctx->fingerprint
	);
}

// Doubles the class capacity and rebuilds the index over it, so the
// index never holds more than half its slots.
static int
join_classes_grow(struct join_classes *classes) {
	uint32_t capacity = classes->capacity ? classes->capacity * 2 : 1024;
	if (capacity > UINT32_MAX / 4) {
		return -1;
	}
	struct join_class *items =
		realloc(classes->items, (size_t)capacity * sizeof(*items));
	if (items == NULL) {
		return -1;
	}
	classes->items = items;
	free(classes->index.hashes);
	free(classes->index.slots);
	if (join_index_init(&classes->index, capacity)) {
		return -1;
	}
	classes->capacity = capacity;
	for (uint32_t id = 0; id < classes->count; ++id) {
		join_index_put(
			&classes->index, (uint32_t)items[id].fingerprint.lo, id
		);
	}
	return 0;
}

static void
join_classes_free_index(struct join_classes *classes) {
	free(classes->index.hashes);
	free(classes->index.slots);
	classes->index = (struct join_index){0};
}

// The class id of a fingerprint, a new one numbered next when unseen;
// an all-ones id when the classes cannot grow.
static uint32_t
join_classes_intern(
	struct join_classes *classes,
	struct join_fingerprint fingerprint,
	uint32_t v,
	uint32_t h
) {
	struct join_class_ctx ctx = {classes->items, fingerprint};
	uint32_t id = join_index_find(
		&classes->index, (uint32_t)fingerprint.lo, join_class_eq, &ctx
	);
	if (id != UINT32_MAX) {
		return id;
	}
	if (classes->count == classes->capacity && join_classes_grow(classes)) {
		return UINT32_MAX;
	}
	id = classes->count++;
	classes->items[id] = (struct join_class){fingerprint, v, h, 0};
	join_index_put(&classes->index, (uint32_t)fingerprint.lo, id);
	return id;
}

/*
 * The part of a join every seed shares: the distinct pairs, their
 * prepared groups and the rows each pair paints.
 *
 * A pair with both sides whole covers every cell, so such a join has no
 * uncovered cell to number zero.
 */
struct join_input {
	const struct value_range *ranges1;
	const struct value_range *ranges2;
	uint32_t v_dim;
	uint32_t h_dim;
	const uint32_t *pair_group1;
	const uint32_t *pair_group2;
	uint32_t pair_count;
	const struct join_group *groups1;
	const struct join_group *groups2;
	struct join_csr painted;
	bool covered;
};

/*
 * Paints the classes of a join under one seed into its table.
 *
 * A pair's coverage of a cell is a product a * b over GF(2); a side over
 * half its dimension is rewritten as a = 1 + a', adding row, column and
 * constant terms, so only products of small sides are painted. A cell
 * equal to its left or upper neighbour takes that neighbour's id, others
 * are interned. Unless a pair covers every cell the empty set takes id
 * zero, and with no uncovered cell the shift renumbers classes from zero.
 */
static int
join_paint(
	const struct join_input *in,
	uint32_t seed,
	struct value_table *table,
	struct join_classes *classes,
	uint32_t *shift
) {
	uint32_t v_dim = in->v_dim;
	uint32_t h_dim = in->h_dim;
	struct join_fingerprint *pair_terms =
		malloc((in->pair_count ? in->pair_count : 1) *
		       sizeof(*pair_terms));
	struct join_fingerprint *row_terms = calloc(v_dim, sizeof(*row_terms));
	struct join_fingerprint *col_terms = calloc(h_dim, sizeof(*col_terms));
	struct join_fingerprint *row = malloc(h_dim * sizeof(*row));
	struct join_fingerprint *above = malloc(h_dim * sizeof(*above));
	int rc = -1;
	if (pair_terms == NULL || row_terms == NULL || col_terms == NULL ||
	    row == NULL || above == NULL || join_classes_grow(classes)) {
		goto done;
	}

	struct join_fingerprint all_term = {0, 0};
	for (uint32_t pair = 0; pair < in->pair_count; ++pair) {
		const struct join_group *rows =
			in->groups1 + in->pair_group1[pair];
		const struct join_group *cols =
			in->groups2 + in->pair_group2[pair];
		struct join_fingerprint z = join_pair_fingerprint(pair, seed);
		pair_terms[pair] = z;
		for (uint32_t idx = 0; rows->flipped && idx < cols->small_count;
		     ++idx) {
			join_fingerprint_xor(col_terms + cols->small[idx], z);
		}
		for (uint32_t idx = 0; cols->flipped && idx < rows->small_count;
		     ++idx) {
			join_fingerprint_xor(row_terms + rows->small[idx], z);
		}
		if (rows->flipped && cols->flipped) {
			join_fingerprint_xor(&all_term, z);
		}
	}

	if (!in->covered) {
		join_classes_intern(
			classes, (struct join_fingerprint){0, 0}, 0, 0
		);
	}
	bool uncovered = false;
	for (uint32_t v = 0; v < v_dim; ++v) {
		struct join_fingerprint base = all_term;
		join_fingerprint_xor(&base, row_terms[v]);
		for (uint32_t h = 0; h < h_dim; ++h) {
			row[h] = base;
			join_fingerprint_xor(row + h, col_terms[h]);
		}
		for (size_t e = in->painted.offsets[v];
		     e < in->painted.offsets[v + 1];
		     ++e) {
			uint32_t pair = in->painted.items[e];
			const struct join_group *cols =
				in->groups2 + in->pair_group2[pair];
			for (uint32_t idx = 0; idx < cols->small_count; ++idx) {
				join_fingerprint_xor(
					row + cols->small[idx], pair_terms[pair]
				);
			}
		}
		uint32_t *cells = value_table_get_ptr(table, v, 0);
		const uint32_t *up =
			v > 0 ? value_table_get_ptr(table, v - 1, 0) : NULL;
		uint32_t run_id = UINT32_MAX;
		uint64_t run = 0;
		for (uint32_t h = 0; h < h_dim; ++h) {
			uint32_t id;
			if (h > 0 && join_fingerprint_eq(row[h], row[h - 1])) {
				id = cells[h - 1];
			} else if (v > 0 &&
				   join_fingerprint_eq(row[h], above[h])) {
				id = up[h];
			} else {
				id = join_classes_intern(classes, row[h], v, h);
				if (id == UINT32_MAX) {
					goto done;
				}
			}
			cells[h] = id;
			if (id != run_id) {
				if (run) {
					classes->items[run_id].cells += run;
				}
				run_id = id;
				run = 0;
			}
			++run;
			uncovered |= id == 0;
		}
		if (run) {
			classes->items[run_id].cells += run;
		}
		struct join_fingerprint *swap = above;
		above = row;
		row = swap;
	}

	*shift = !in->covered && !uncovered ? 1 : 0;
	for (uint32_t v = 0; *shift && v < v_dim; ++v) {
		uint32_t *cells = value_table_get_ptr(table, v, 0);
		for (uint32_t h = 0; h < h_dim; ++h) {
			--cells[h];
		}
	}
	rc = 0;

done:
	free(above);
	free(row);
	free(col_terms);
	free(row_terms);
	free(pair_terms);
	return rc;
}

/*
 * Collects each pair's classes into the registry, ascending; returns 1
 * on a detected merge of pair sets and -1 on a failure.
 *
 * A pair reads the fewest of its rectangle's cells or the first cells
 * of the classes in its rows or columns, never more than its rectangle
 * holds, plus marks over a side. The classes in a rectangle lie wholly
 * inside it and tile it, so their cells add up to it; a single merge
 * breaks that sum for some pair, while merges from one dependency among
 * the fingerprints may cancel.
 */
static int
join_collect(
	const struct join_input *in,
	struct value_table *table,
	const struct join_classes *classes,
	uint32_t shift,
	uint8_t *marks,
	struct value_registry *registry
) {
	// The classes as the table numbers them.
	const struct join_class *items = classes->items + shift;
	uint32_t count = classes->count - shift;
	uint32_t first = !in->covered && !shift ? 1 : 0;
	uint32_t *row_starts =
		malloc(((size_t)in->v_dim + 1) * sizeof(uint32_t));
	struct join_span *spans = calloc(count + 1, sizeof(struct join_span));
	uint32_t *found = malloc((count + 1) * sizeof(uint32_t));
	uint32_t *stamps = calloc(count + 1, sizeof(uint32_t));
	struct join_csr columns = {0};
	int rc = -1;
	if (row_starts == NULL || spans == NULL || found == NULL ||
	    stamps == NULL) {
		goto done;
	}
	for (uint32_t v = 0, id = first; v <= in->v_dim; ++v) {
		while (id < count && items[id].v < v) {
			++id;
		}
		row_starts[v] = id;
	}
	for (uint32_t id = first; id < count; ++id) {
		spans[id] = (struct join_span){&items[id].h, 1};
	}
	int built = join_csr_build(&columns, in->h_dim, spans, count);
	free(spans);
	spans = NULL;
	if (built) {
		goto done;
	}

	for (uint32_t pair = 0; pair < in->pair_count; ++pair) {
		const struct value_range *range1 =
			in->ranges1 + in->pair_group1[pair];
		const struct value_range *range2 =
			in->ranges2 + in->pair_group2[pair];
		const uint32_t *rows = ADDR_OF(&range1->values);
		const uint32_t *cols = ADDR_OF(&range2->values);
		uint64_t by_cells = range1->count * range2->count;
		uint64_t by_rows = 0;
		uint64_t by_cols = 0;
		for (uint64_t idx = 0; idx < range1->count; ++idx) {
			by_rows += row_starts[rows[idx] + 1] -
				   row_starts[rows[idx]];
		}
		for (uint64_t idx = 0; idx < range2->count; ++idx) {
			by_cols += columns.offsets[cols[idx] + 1] -
				   columns.offsets[cols[idx]];
		}
		uint32_t found_count = 0;
		if (by_cells <= by_rows && by_cells <= by_cols) {
			for (uint64_t i = 0; i < range1->count; ++i) {
				const uint32_t *cells =
					value_table_get_ptr(table, rows[i], 0);
				for (uint64_t j = 0; j < range2->count; ++j) {
					uint32_t id = cells[cols[j]];
					if (stamps[id] != pair + 1) {
						stamps[id] = pair + 1;
						found[found_count++] = id;
					}
				}
			}
		} else if (by_rows <= by_cols) {
			for (uint64_t j = 0; j < range2->count; ++j) {
				marks[cols[j]] = 1;
			}
			for (uint64_t i = 0; i < range1->count; ++i) {
				for (uint32_t id = row_starts[rows[i]];
				     id < row_starts[rows[i] + 1];
				     ++id) {
					if (marks[items[id].h]) {
						found[found_count++] = id;
					}
				}
			}
			for (uint64_t j = 0; j < range2->count; ++j) {
				marks[cols[j]] = 0;
			}
		} else {
			for (uint64_t i = 0; i < range1->count; ++i) {
				marks[rows[i]] = 1;
			}
			for (uint64_t j = 0; j < range2->count; ++j) {
				for (size_t e = columns.offsets[cols[j]];
				     e < columns.offsets[cols[j] + 1];
				     ++e) {
					uint32_t id = columns.items[e];
					if (marks[items[id].v]) {
						found[found_count++] = id;
					}
				}
			}
			for (uint64_t i = 0; i < range1->count; ++i) {
				marks[rows[i]] = 0;
			}
		}
		uint32_t ascending = 1;
		while (ascending < found_count &&
		       found[ascending - 1] < found[ascending]) {
			++ascending;
		}
		if (ascending < found_count) {
			qsort(found,
			      found_count,
			      sizeof(uint32_t),
			      join_value_cmp);
		}
		uint64_t cells = 0;
		for (uint32_t idx = 0; idx < found_count; ++idx) {
			cells += items[found[idx]].cells;
		}
		if (cells != by_cells) {
			rc = 1;
			goto done;
		}
		if (value_registry_start(registry)) {
			goto done;
		}
		for (uint32_t idx = 0; idx < found_count; ++idx) {
			if (value_registry_collect(registry, found[idx])) {
				goto done;
			}
		}
	}
	rc = 0;

done:
	join_csr_free(&columns);
	free(stamps);
	free(found);
	free(spans);
	free(row_starts);
	return rc;
}

/*
 * Joins two group registries into a joint stage of the classification.
 *
 * The name scopes the memory contexts of the join - the value table and
 * the stage registry - each titled with the name and its own artifact
 * leaf, so every context of one join of one classifier composition is
 * identified in the memory tree.
 *
 * Rules referencing the same group pair on both sides cover the same
 * cells, so the stage operates on distinct pairs instead of all rules.
 * A cell's class is the set of pairs covering it: the uncovered class
 * takes zero when some cell is uncovered, the rest are numbered by
 * first appearance in row-major order. On success the routine
 * initializes the value table holding the join results, the registry
 * with one range per pair holding the pair matching class identifiers
 * in ascending order, and fills the rule to pair mapping; a rule
 * without a value on either side keeps the invalid group mark. A
 * detected merge of pair sets repeats the classes under the next seed,
 * and the join fails once every seed merged.
 */
int
classify_join(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *left,
	const struct classifier *right,
	uint32_t rule_count,
	struct value_table *table,
	struct classifier *out
) {
	const struct value_registry *registry1 = &left->registry;
	const struct value_registry *registry2 = &right->registry;
	uint32_t v_dim = value_registry_capacity(registry1);
	uint32_t h_dim = value_registry_capacity(registry2);
	struct value_registry *registry = &out->registry;

	if (classifier_init(out, memory_context, rule_count)) {
		return -1;
	}
	uint32_t *rule_to_group = out->rule_groups;

	// The table is releasable from here on and the scratch lives on the
	// heap, so every failure below unwinds through the common exit.
	memset(table, 0, sizeof(*table));
	uint32_t pair_alloc_count = rule_count ? rule_count : 1;
	uint32_t *pair_group1 = malloc(sizeof(uint32_t) * pair_alloc_count);
	uint32_t *pair_group2 = malloc(sizeof(uint32_t) * pair_alloc_count);
	struct join_span *spans =
		malloc(sizeof(struct join_span) * pair_alloc_count);
	struct join_group *groups1 =
		calloc(registry1->range_count + 1, sizeof(struct join_group));
	struct join_group *groups2 =
		calloc(registry2->range_count + 1, sizeof(struct join_group));
	struct join_input in = {
		.ranges1 = ADDR_OF(&registry1->ranges),
		.ranges2 = ADDR_OF(&registry2->ranges),
		.v_dim = v_dim,
		.h_dim = h_dim,
		.pair_group1 = pair_group1,
		.pair_group2 = pair_group2,
		.groups1 = groups1,
		.groups2 = groups2,
	};
	// Zeroed scratch over the larger dimension, left zeroed after use.
	uint8_t *marks = calloc(v_dim > h_dim ? v_dim : h_dim, 1);
	struct join_index pairs = {0};
	int rc = -1;

	char table_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(table_name, sizeof(table_name), name, "table");
	if (pair_group1 == NULL || pair_group2 == NULL || spans == NULL ||
	    groups1 == NULL || groups2 == NULL || marks == NULL ||
	    join_index_init(&pairs, pair_alloc_count) ||
	    value_table_init(table, memory_context, table_name, v_dim, h_dim)) {
		goto done;
	}

	uint32_t pair_count = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		uint32_t group1 = left->rule_groups[rule_idx];
		uint32_t group2 = right->rule_groups[rule_idx];
		rule_to_group[rule_idx] = FILTER_GROUP_INVALID;
		if (group1 == FILTER_GROUP_INVALID ||
		    group2 == FILTER_GROUP_INVALID) {
			continue;
		}
		struct merge_pair_ctx ctx = {
			pair_group1, pair_group2, group1, group2
		};
		uint32_t hash = merge_group_pair_hash(group1, group2);
		uint32_t pair =
			join_index_find(&pairs, hash, join_pair_eq, &ctx);
		if (pair == UINT32_MAX) {
			pair = pair_count++;
			join_index_put(&pairs, hash, pair);
			pair_group1[pair] = group1;
			pair_group2[pair] = group2;
		}
		rule_to_group[rule_idx] = pair;
	}
	in.pair_count = pair_count;

	for (uint32_t pair = 0; pair < pair_count; ++pair) {
		struct join_group *rows = groups1 + pair_group1[pair];
		struct join_group *cols = groups2 + pair_group2[pair];
		if (join_group_prepare(
			    rows, in.ranges1 + pair_group1[pair], v_dim, marks
		    ) ||
		    join_group_prepare(
			    cols, in.ranges2 + pair_group2[pair], h_dim, marks
		    )) {
			goto done;
		}
		spans[pair] = (struct join_span
		){rows->small, cols->small_count ? rows->small_count : 0};
		in.covered |= rows->flipped && !rows->small_count &&
			      cols->flipped && !cols->small_count;
	}
	if (join_csr_build(&in.painted, v_dim, spans, pair_count)) {
		goto done;
	}

	char registry_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(
		registry_name, sizeof(registry_name), name, "registry"
	);
	rc = 1;
	for (uint32_t seed = 0; rc == 1 && seed < JOIN_SEED_ATTEMPTS; ++seed) {
		struct join_classes classes = {0};
		uint32_t shift = 0;
		rc = join_paint(&in, seed, table, &classes, &shift);
		join_classes_free_index(&classes);
		if (rc == 0 && value_registry_init(
				       registry, memory_context, registry_name
			       )) {
			rc = -1;
		}
		if (rc == 0) {
			rc = join_collect(
				&in, table, &classes, shift, marks, registry
			);
		}
		free(classes.items);
		if (rc != 0) {
			value_registry_fini(registry);
			memset(registry, 0, sizeof(*registry));
		}
	}

done:
	for (uint64_t idx = 0; groups1 != NULL && idx < registry1->range_count;
	     ++idx) {
		free(groups1[idx].complement);
	}
	for (uint64_t idx = 0; groups2 != NULL && idx < registry2->range_count;
	     ++idx) {
		free(groups2[idx].complement);
	}
	join_csr_free(&in.painted);
	free(pairs.slots);
	free(pairs.hashes);
	free(marks);
	free(groups2);
	free(groups1);
	free(spans);
	free(pair_group2);
	free(pair_group1);
	if (rc != 0) {
		// A failed join leaves no state behind, whatever the stage.
		value_table_free(table);
		value_registry_fini(registry);
		memset(registry, 0, sizeof(*registry));
		memory_bfree(
			memory_context,
			out->rule_groups,
			sizeof(uint32_t) * pair_alloc_count
		);
		out->rule_groups = NULL;
	}
	return rc == 0 ? 0 : -1;
}

// Builds immutable first-match rows without enumerating final classes.
//
// Only distinct group pairs are inverted onto the left classes. Each row is
// completed in rule order and interned by content in heap scratch; the
// distinct rows become the compact class list and the original left
// classes map onto it. Heap scratch is never published or charged to
// the shared-memory arena, and a failed build leaves every owned
// pointer of the table NULL, releasable by rule_table_free.
int
classify_join_rules(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *left,
	const struct classifier *right,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	struct rule_table *table
) {
	// The output is releasable from the first statement on: a failed
	// allocation below frees it exactly like a released table.
	memset(table, 0, sizeof(*table));
	uint32_t v_dim = value_registry_capacity(&left->registry);
	uint32_t h_dim = value_registry_capacity(&right->registry);

	size_t pair_size =
		(size_t)(rule_count ? rule_count : 1) * sizeof(uint32_t);
	uint32_t *group1 = malloc(pair_size);
	uint32_t *group2 = malloc(pair_size);
	uint32_t *first = malloc(pair_size);
	size_t *offsets = calloc((size_t)v_dim + 1, sizeof(size_t));
	size_t *cursor = malloc((size_t)v_dim * sizeof(size_t));
	uint32_t *row = malloc((size_t)h_dim * sizeof(uint32_t));
	uint32_t *dir_idx = malloc((size_t)v_dim * sizeof(uint32_t));
	uint32_t **distinct = malloc((size_t)v_dim * sizeof(uint32_t *));
	uint32_t *edges = NULL;
	struct join_index pairs = {0};
	struct join_index rows = {0};
	uint32_t distinct_count = 0;
	int rc = -1;
	if (group1 == NULL || group2 == NULL || first == NULL ||
	    offsets == NULL || cursor == NULL || row == NULL ||
	    dir_idx == NULL || distinct == NULL) {
		goto done;
	}
	if (rule_table_init(table, memory_context, name, v_dim, h_dim) ||
	    join_index_init(&pairs, rule_count ? rule_count : 1) ||
	    join_index_init(&rows, v_dim)) {
		goto done;
	}
	struct value_range *left_ranges = ADDR_OF(&left->registry.ranges);
	struct value_range *right_ranges = ADDR_OF(&right->registry.ranges);
	uint32_t pair_count = 0;
	for (uint32_t r = 0; r < rule_count; ++r) {
		uint32_t g1 = left->rule_groups[r];
		uint32_t g2 = right->rule_groups[r];
		if (rules[r] == NULL || g1 == FILTER_GROUP_INVALID ||
		    g2 == FILTER_GROUP_INVALID) {
			continue;
		}
		struct merge_pair_ctx ctx = {group1, group2, g1, g2};
		uint32_t hash = merge_group_pair_hash(g1, g2);
		if (join_index_find(&pairs, hash, join_pair_eq, &ctx) !=
		    UINT32_MAX) {
			continue;
		}
		join_index_put(&pairs, hash, pair_count);
		group1[pair_count] = g1;
		group2[pair_count] = g2;
		first[pair_count++] = r;
		const struct value_range *range = left_ranges + g1;
		const uint32_t *values = ADDR_OF(&range->values);
		for (uint64_t i = 0; i < range->count; ++i) {
			++offsets[values[i] + 1];
		}
	}
	for (uint32_t v = 0; v < v_dim; ++v) {
		offsets[v + 1] += offsets[v];
		cursor[v] = offsets[v];
	}
	size_t edge_count = offsets[v_dim];
	if (edge_count > SIZE_MAX / sizeof(uint32_t)) {
		goto done;
	}
	edges = malloc((edge_count ? edge_count : 1) * sizeof(uint32_t));
	if (edges == NULL) {
		goto done;
	}
	for (uint32_t p = 0; p < pair_count; ++p) {
		const struct value_range *range = left_ranges + group1[p];
		const uint32_t *values = ADDR_OF(&range->values);
		for (uint64_t i = 0; i < range->count; ++i) {
			edges[cursor[values[i]]++] = p;
		}
	}
	// A left class no projected pair covers resolves to the all-empty
	// row, interned once and shared by every uncovered class - a
	// projection covering few classes of a large family never walks
	// the whole class cross.
	uint32_t empty_class = UINT32_MAX;
	for (uint32_t v = 0; v < v_dim; ++v) {
		if (offsets[v] == offsets[v + 1]) {
			if (empty_class == UINT32_MAX) {
				memset(row,
				       0xff,
				       (size_t)h_dim * sizeof(uint32_t));
				uint32_t *stored =
					malloc((size_t)h_dim * sizeof(uint32_t)
					);
				if (stored == NULL) {
					goto done;
				}
				memcpy(stored,
				       row,
				       (size_t)h_dim * sizeof(uint32_t));
				distinct[distinct_count] = stored;
				join_index_put(&rows, 0, distinct_count);
				empty_class = distinct_count++;
			}
			dir_idx[v] = empty_class;
			continue;
		}
		memset(row, 0xff, (size_t)h_dim * sizeof(uint32_t));
		uint32_t hash = 0;
		for (size_t e = offsets[v]; e < offsets[v + 1]; ++e) {
			uint32_t p = edges[e];
			const struct value_range *range =
				right_ranges + group2[p];
			const uint32_t *columns = ADDR_OF(&range->values);
			for (uint64_t i = 0; i < range->count; ++i) {
				uint32_t h = columns[i];
				if (row[h] == FILTER_RULE_INVALID) {
					row[h] = first[p];
					hash ^= merge_group_pair_hash(
						h, first[p]
					);
				}
			}
		}
		struct final_row_ctx ctx = {
			(const uint32_t *const *)distinct, row, h_dim
		};
		uint32_t canonical =
			join_index_find(&rows, hash, join_row_eq, &ctx);
		if (canonical == UINT32_MAX) {
			uint32_t *stored =
				malloc((size_t)h_dim * sizeof(uint32_t));
			if (stored == NULL) {
				goto done;
			}
			memcpy(stored, row, (size_t)h_dim * sizeof(uint32_t));
			distinct[distinct_count] = stored;
			join_index_put(&rows, hash, distinct_count);
			dir_idx[v] = distinct_count++;
		} else {
			dir_idx[v] = canonical;
		}
	}

	// The compact class rows are published chunk by chunk out of the
	// heap scratch, and the class map goes in only once every row is
	// in, so a failure above leaves the map empty.
	struct memory_context *table_context = ADDR_OF(&table->memory_context);
	uint32_t **interned = memory_balloc(
		table_context, distinct_count * sizeof(uint32_t *)
	);
	if (interned == NULL) {
		goto done;
	}
	memset(interned, 0, distinct_count * sizeof(uint32_t *));
	SET_OFFSET_OF(&table->rows, interned);
	table->class_count = distinct_count;
	for (uint32_t d = 0; d < distinct_count; ++d) {
		uint32_t *stored = memory_balloc(
			table_context, (size_t)h_dim * sizeof(uint32_t)
		);
		if (stored == NULL) {
			goto done;
		}
		memcpy(stored, distinct[d], (size_t)h_dim * sizeof(uint32_t));
		SET_OFFSET_OF(interned + d, stored);
	}
	if (vline_init(&table->class_map, table_context, "classes", v_dim)) {
		goto done;
	}
	for (uint32_t v = 0; v < v_dim; ++v) {
		*vline_get_ptr(&table->class_map, v) = dir_idx[v];
	}
	rc = 0;
done:
	free(rows.hashes);
	free(rows.slots);
	free(pairs.hashes);
	free(pairs.slots);
	if (distinct != NULL) {
		for (uint32_t d = 0; d < distinct_count; ++d) {
			free(distinct[d]);
		}
	}
	free(distinct);
	free(dir_idx);
	free(edges);
	free(row);
	free(cursor);
	free(offsets);
	free(first);
	free(group2);
	free(group1);
	if (rc != 0) {
		rule_table_free(table);
	}
	return rc;
}
