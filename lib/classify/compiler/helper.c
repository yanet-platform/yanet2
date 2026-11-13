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

// Tests can collapse the lookup index without changing exact equality.
#ifndef JOIN_FINGERPRINT_MASK
#define JOIN_FINGERPRINT_MASK UINT32_MAX
#endif

static uint32_t
join_pair_hash(uint32_t pair) {
	uint32_t hash = pair + 1;
	hash ^= hash >> 16;
	hash *= 0x85ebca6bU;
	hash ^= hash >> 13;
	hash *= 0xc2b2ae35U;
	return hash ^ (hash >> 16);
}

static uint32_t
join_hash_mask(uint32_t hash) {
	return hash & JOIN_FINGERPRINT_MASK;
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
	uint32_t *boundary;
	uint32_t boundary_count;
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

// Complementing a side preserves its internal membership boundaries.
static int
join_group_boundaries(struct join_group *group, uint32_t dim) {
	if (group->boundary != NULL) {
		return 0;
	}
	size_t count = group->small_count;
	uint32_t *sorted = malloc((count ? count : 1) * sizeof(uint32_t));
	group->boundary = malloc((count ? 2 * count : 1) * sizeof(uint32_t));
	if (sorted == NULL || group->boundary == NULL) {
		free(sorted);
		return -1;
	}
	if (count) {
		memcpy(sorted, group->small, count * sizeof(uint32_t));
	}
	qsort(sorted, count, sizeof(uint32_t), join_value_cmp);
	for (size_t i = 0; i < count; ++i) {
		uint32_t value = sorted[i];
		if (value && (!i || sorted[i - 1] + 1 != value)) {
			group->boundary[group->boundary_count++] = value;
		}
		if (value + 1 < dim &&
		    (i + 1 == count || sorted[i + 1] != value + 1)) {
			group->boundary[group->boundary_count++] = value + 1;
		}
	}
	uint32_t *compact =
		realloc(group->boundary,
			(group->boundary_count ? group->boundary_count : 1) *
				sizeof(uint32_t));
	if (compact) {
		group->boundary = compact;
	}
	free(sorted);
	return 0;
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

// Short exact keys are inline; larger keys share an append-only pair pool.
struct join_class {
	uint32_t hash;
	uint32_t key[2];
};
struct join_classes {
	struct join_class *items;
	struct join_index index;
	uint32_t count, capacity;
	uint32_t *keys;
	size_t key_count, key_capacity;
};
struct join_class_ctx {
	const struct join_classes *classes;
	uint32_t hash;
	const uint32_t *key;
	uint32_t count;
};
static struct join_span
join_class_key(const struct join_classes *classes, uint32_t id) {
	const uint32_t *key = classes->items[id].key;
	uint32_t count =
		key[0] != UINT32_MAX ? (key[1] != UINT32_MAX ? 2 : 1) : 0;
	if (!count && key[1] != UINT32_MAX) {
		key = classes->keys + key[1];
		count = *key++;
	}
	return (struct join_span){key, count};
}

static int
join_class_eq(uint32_t id, const void *data) {
	const struct join_class_ctx *ctx = data;
	if (ctx->classes->items[id].hash != ctx->hash) {
		return 0;
	}
	struct join_span key = join_class_key(ctx->classes, id);
	return key.count == ctx->count &&
	       (!key.count || !memcmp(key.values,
				      ctx->key,
				      (size_t)key.count * sizeof(uint32_t)));
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
		join_index_put(&classes->index, (uint32_t)items[id].hash, id);
	}
	return 0;
}

static void
join_classes_free_index(struct join_classes *classes) {
	free(classes->index.hashes);
	free(classes->index.slots);
	classes->index = (struct join_index){0};
}

// The class id of an exact key, a new one numbered next when unseen;
// an all-ones id when the classes cannot grow.
static uint32_t
join_classes_intern(
	struct join_classes *classes, const struct join_class_ctx *ctx
) {
	uint32_t id = join_index_find(
		&classes->index, (uint32_t)ctx->hash, join_class_eq, ctx
	);
	if (id != UINT32_MAX) {
		return id;
	}
	if (classes->count == classes->capacity && join_classes_grow(classes)) {
		return UINT32_MAX;
	}
	uint32_t key[2] = {UINT32_MAX, UINT32_MAX};
	if (ctx->count <= 2) {
		for (uint32_t i = 0; i < ctx->count; ++i) {
			key[i] = ctx->key[i];
		}
	} else {
		size_t end = classes->key_count + (size_t)ctx->count + 1;
		if (end > UINT32_MAX) {
			return UINT32_MAX;
		}
		if (end > classes->key_capacity) {
			size_t cap = classes->key_capacity
					     ? classes->key_capacity +
						       classes->key_capacity / 4
					     : 1024;
			if (cap < end) {
				cap = end;
			}
			uint32_t *keys =
				realloc(classes->keys, cap * sizeof(uint32_t));
			if (!keys) {
				return UINT32_MAX;
			}
			classes->keys = keys;
			classes->key_capacity = cap;
		}
		key[1] = classes->key_count;
		classes->keys[classes->key_count++] = ctx->count;
		memcpy(classes->keys + classes->key_count,
		       ctx->key,
		       (size_t)ctx->count * sizeof(uint32_t));
		classes->key_count = end;
	}
	id = classes->count++;
	classes->items[id] = (struct join_class){ctx->hash, {key[0], key[1]}};
	join_index_put(&classes->index, (uint32_t)ctx->hash, id);
	return id;
}

// Three sorted streams encode the exact residual relative to the common term.
static uint32_t
join_key(
	uint32_t *key, const uint32_t *const streams[3], const size_t sizes[3]
) {
	const uint32_t *a = streams[0], *b = streams[1], *c = streams[2];
	const uint32_t *ae = a + sizes[0], *be = b + sizes[1],
		       *ce = c + sizes[2];
	uint32_t *out = key;
	while ((a < ae) + (b < be) + (c < ce) > 1) {
		uint32_t x = a < ae ? *a : UINT32_MAX;
		uint32_t y = b < be ? *b : UINT32_MAX;
		uint32_t z = c < ce ? *c : UINT32_MAX;
		uint32_t p = x < y ? x : y;
		if (z < p) {
			p = z;
		}
		unsigned matches = 0;
		if (x == p) {
			++a;
			++matches;
		}
		if (y == p) {
			++b;
			++matches;
		}
		if (z == p) {
			++c;
			++matches;
		}
		if (matches & 1) {
			*out++ = p;
		}
	}
	const uint32_t *last = a < ae ? a : b < be ? b : c;
	size_t count = a < ae	? (size_t)(ae - a)
		       : b < be ? (size_t)(be - b)
				: (size_t)(ce - c);
	memcpy(out, last, count * sizeof(uint32_t));
	return (uint32_t)(out - key) + count;
}

/*
 * The distinct pairs, their small sides, and their membership boundaries.
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
	struct join_csr row_terms, col_terms;
	bool covered;
};

/*
 * Builds exact classes from neighbor differences and canonical residual keys.
 *
 * A pair's coverage of a cell is a product a * b over GF(2); a side over
 * half its dimension is rewritten as a = 1 + a', adding row, column and
 * constant terms, so only products of small sides are painted. Neighbor
 * equality is certified by counting pairs covering exactly one endpoint.
 * Other cells are interned by their sorted residual pair sets. Unless a pair
 * covers every cell the empty set takes id zero, and with no uncovered cell the
 * shift renumbers classes from zero.
 */
static int
join_paint(
	const struct join_input *in,
	struct value_table *table,
	struct join_classes *classes,
	uint32_t *shift
) {
	uint32_t v_dim = in->v_dim;
	uint32_t h_dim = in->h_dim;
	uint32_t *pair_hash =
		malloc(((size_t)in->pair_count + 1) * sizeof(uint32_t));
	uint32_t *col_hash = calloc(h_dim, sizeof(uint32_t));
	// Unsigned arithmetic is exact modulo 2^32; the final difference count
	// lies between zero and the number of pairs, strictly below 2^32.
	uint32_t *horizontal = calloc(h_dim, sizeof(uint32_t));
	uint32_t *horizontal_base = calloc(h_dim, sizeof(uint32_t));
	uint32_t *vertical = calloc(h_dim, sizeof(uint32_t));
	uint32_t *key = malloc(((size_t)in->pair_count + 1) * sizeof(uint32_t));
	uint32_t *heads = malloc((size_t)h_dim * sizeof(uint32_t));
	uint32_t *paint_buffer =
		malloc(((size_t)in->pair_count + 1) * sizeof(uint32_t));
	struct paint_entry {
		uint32_t pair, next;
	};
	struct paint_entry *paint_keys = NULL;
	size_t paint_capacity = 0;
	int rc = -1;
	if (!key || !heads || !paint_buffer || !pair_hash || !col_hash) {
		goto done;
	}
	if (horizontal == NULL || horizontal_base == NULL || vertical == NULL ||
	    join_classes_grow(classes)) {
		goto done;
	}
	for (uint32_t p = 0; p < in->pair_count; ++p) {
		pair_hash[p] = join_pair_hash(p);
	}
	for (uint32_t h = 0; h < h_dim; ++h) {
		for (size_t i = in->col_terms.offsets[h];
		     i < in->col_terms.offsets[h + 1];
		     ++i) {
			col_hash[h] ^= pair_hash[in->col_terms.items[i]];
		}
	}
	for (uint32_t pair = 0; pair < in->pair_count; ++pair) {
		const struct join_group *rows =
			in->groups1 + in->pair_group1[pair];
		const struct join_group *cols =
			in->groups2 + in->pair_group2[pair];
		if (rows->flipped) {
			for (uint32_t i = 0; i < cols->boundary_count; ++i) {
				++horizontal_base[cols->boundary[i]];
			}
		}
	}

	if (!in->covered) {
		uint32_t count = 0;
		for (uint32_t p = 0; p < in->pair_count; ++p) {
			if (in->groups1[in->pair_group1[p]].flipped &&
			    in->groups2[in->pair_group2[p]].flipped) {
				key[count++] = p;
			}
		}
		uint32_t hash = 0;
		for (uint32_t i = 0; i < count; ++i) {
			hash ^= pair_hash[key[i]];
		}
		struct join_class_ctx ctx = {
			classes, join_hash_mask(hash), key, count
		};
		if (join_classes_intern(classes, &ctx) == UINT32_MAX) {
			goto done;
		}
	}
	bool uncovered = false;
	for (uint32_t v = 0; v < v_dim; ++v) {
		uint32_t row_hash = 0;
		for (size_t i = in->row_terms.offsets[v];
		     i < in->row_terms.offsets[v + 1];
		     ++i) {
			row_hash ^= pair_hash[in->row_terms.items[i]];
		}
		memset(heads, 255, (size_t)h_dim * sizeof(uint32_t));
		size_t total = 0;
		for (size_t e = in->painted.offsets[v];
		     e < in->painted.offsets[v + 1];
		     ++e) {
			total += in->groups2
					 [in->pair_group2[in->painted.items[e]]]
						 .small_count;
		}
		if (total > UINT32_MAX) {
			goto done;
		}
		if (total > paint_capacity || paint_keys == NULL) {
			size_t capacity = total > paint_capacity * 2
						  ? total
						  : paint_capacity * 2;
			if (!capacity) {
				capacity = 1;
			}
			struct paint_entry *next =
				realloc(paint_keys, capacity * sizeof(*next));
			if (!next) {
				goto done;
			}
			paint_keys = next;
			paint_capacity = capacity;
		}
		uint32_t painted_count = 0;
		memcpy(horizontal, horizontal_base, h_dim * sizeof(uint32_t));
		memset(vertical, 0, h_dim * sizeof(uint32_t));
		uint32_t vertical_base = 0;
		size_t current = in->painted.offsets[v],
		       current_end = in->painted.offsets[v + 1];
		size_t previous = v ? in->painted.offsets[v - 1] : current;
		size_t previous_end = in->painted.offsets[v];
		while (v && (current < current_end || previous < previous_end)
		) {
			uint32_t a = current < current_end
					     ? in->painted.items[current]
					     : UINT32_MAX;
			uint32_t b = previous < previous_end
					     ? in->painted.items[previous]
					     : UINT32_MAX;
			if (a == b) {
				++current;
				++previous;
				continue;
			}
			uint32_t pair;
			if (a < b) {
				pair = a;
				++current;
			} else {
				pair = b;
				++previous;
			}
			const struct join_group *cols =
				in->groups2 + in->pair_group2[pair];
			vertical_base += cols->flipped;
			uint32_t sign = cols->flipped ? UINT32_MAX : 1;
			for (uint32_t i = 0; i < cols->small_count; ++i) {
				vertical[cols->small[i]] += sign;
			}
		}
		for (size_t e = in->painted.offsets[v];
		     e < in->painted.offsets[v + 1];
		     ++e) {
			uint32_t pair = in->painted.items[e];
			const struct join_group *cols =
				in->groups2 + in->pair_group2[pair];
			uint32_t sign =
				in->groups1[in->pair_group1[pair]].flipped
					? UINT32_MAX
					: 1;
			for (uint32_t i = 0; i < cols->boundary_count; ++i) {
				horizontal[cols->boundary[i]] += sign;
			}
			for (uint32_t idx = 0; idx < cols->small_count; ++idx) {
				uint32_t h = cols->small[idx];
				paint_keys[painted_count] =
					(struct paint_entry){pair, heads[h]};
				heads[h] = painted_count++;
			}
		}
		uint32_t *cells = value_table_get_ptr(table, v, 0);
		const uint32_t *up =
			v > 0 ? value_table_get_ptr(table, v - 1, 0) : NULL;
		for (uint32_t h = 0; h < h_dim; ++h) {
			uint32_t id;
			if (h > 0 && horizontal[h] == 0) {
				id = cells[h - 1];
			} else if (v > 0 && vertical[h] + vertical_base == 0) {
				id = up[h];
			} else {
				uint32_t *paint_end =
					paint_buffer + in->pair_count;
				uint32_t *paint = paint_end;
				uint32_t hash = row_hash ^ col_hash[h];
				for (uint32_t e = heads[h]; e != UINT32_MAX;
				     e = paint_keys[e].next) {
					uint32_t p = paint_keys[e].pair;
					*--paint = p;
					hash ^= pair_hash[p];
				}
				const uint32_t *streams[3] = {
					in->row_terms.items +
						in->row_terms.offsets[v],
					in->col_terms.items +
						in->col_terms.offsets[h],
					paint
				};
				size_t sizes[3] = {
					in->row_terms.offsets[v + 1] -
						in->row_terms.offsets[v],
					in->col_terms.offsets[h + 1] -
						in->col_terms.offsets[h],
					(size_t)(paint_end - paint)
				};
				uint32_t count = join_key(key, streams, sizes);
				struct join_class_ctx ctx = {
					classes,
					join_hash_mask(hash),
					key,
					count
				};
				id = join_classes_intern(classes, &ctx);
				if (id == UINT32_MAX) {
					goto done;
				}
			}
			cells[h] = id;
			uncovered |= id == 0;
		}
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
	free(pair_hash);
	free(col_hash);
	free(key);
	free(heads);
	free(paint_buffer);
	free(paint_keys);
	free(horizontal);
	free(horizontal_base);
	free(vertical);
	return rc;
}

// Class order makes each output range sorted and unique without a collector.
static int
join_collect(
	const struct join_input *in,
	const struct join_classes *classes,
	uint32_t shift,
	struct value_registry *registry
) {
	uint32_t count = classes->count - shift;
	uint32_t first = !in->covered && !shift ? 1 : 0;
	uint32_t *common =
		malloc(((size_t)in->pair_count + 1) * sizeof(uint32_t));
	if (!common) {
		return -1;
	}
	uint32_t common_count = 0;
	int rc = -1;
	for (uint32_t p = 0; p < in->pair_count; ++p) {
		if (value_registry_start(registry)) {
			goto done;
		}
		if (in->groups1[in->pair_group1[p]].flipped &&
		    in->groups2[in->pair_group2[p]].flipped) {
			common[common_count++] = p;
		}
	}
	struct value_range *ranges = ADDR_OF(&registry->ranges);
	struct memory_context *context = ADDR_OF(&registry->memory_context);
	for (uint32_t id = first; id < count; ++id) {
		struct join_span key = join_class_key(classes, id + shift);
		uint32_t i = 0, j = 0;
		while (i < key.count || j < common_count) {
			uint32_t a = i < key.count ? key.values[i] : UINT32_MAX;
			uint32_t b = j < common_count ? common[j] : UINT32_MAX;
			if (a == b) {
				++i;
				++j;
				continue;
			}
			uint32_t pair;
			if (a < b) {
				pair = a;
				++i;
			} else {
				pair = b;
				++j;
			}
			if (value_range_append(context, ranges + pair, id)) {
				goto done;
			}
		}
	}
	registry->max_value = count > first ? count - 1 : 0;
	rc = 0;
done:
	free(common);
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
 * without a value on either side keeps the invalid group mark. Hash
 * collisions are resolved by comparing the complete canonical pair keys.
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
		calloc(pair_alloc_count, sizeof(struct join_span));
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
	free(pairs.slots);
	free(pairs.hashes);
	pairs = (struct join_index){0};

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
		if (join_group_boundaries(cols, h_dim)) {
			goto done;
		}
		spans[pair] =
			(struct join_span){rows->small, rows->small_count};
		in.covered |= rows->flipped && !rows->small_count &&
			      cols->flipped && !cols->small_count;
	}
	if (join_csr_build(&in.painted, v_dim, spans, pair_count)) {
		goto done;
	}

	for (uint32_t p = 0; p < pair_count; ++p) {
		const struct join_group *g = groups1 + pair_group1[p];
		spans[p] = (struct join_span
		){g->small, groups2[pair_group2[p]].flipped ? g->small_count : 0
		};
	}
	if (join_csr_build(&in.row_terms, v_dim, spans, pair_count)) {
		goto done;
	}
	for (uint32_t p = 0; p < pair_count; ++p) {
		const struct join_group *g = groups2 + pair_group2[p];
		spans[p] = (struct join_span
		){g->small, groups1[pair_group1[p]].flipped ? g->small_count : 0
		};
	}
	if (join_csr_build(&in.col_terms, h_dim, spans, pair_count)) {
		goto done;
	}

	free(spans);
	spans = NULL;
	free(marks);
	marks = NULL;

	char registry_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(
		registry_name, sizeof(registry_name), name, "registry"
	);
	{
		struct join_classes classes = {0};
		uint32_t shift = 0;
		rc = join_paint(&in, table, &classes, &shift);
		join_classes_free_index(&classes);
		if (rc == 0 && value_registry_init(
				       registry, memory_context, registry_name
			       )) {
			rc = -1;
		}
		if (rc == 0) {
			rc = join_collect(&in, &classes, shift, registry);
		}
		free(classes.items);
		free(classes.keys);
		if (rc != 0) {
			value_registry_fini(registry);
			memset(registry, 0, sizeof(*registry));
		}
	}

done:
	for (uint64_t idx = 0; groups1 != NULL && idx < registry1->range_count;
	     ++idx) {
		free(groups1[idx].complement);
		free(groups1[idx].boundary);
	}
	for (uint64_t idx = 0; groups2 != NULL && idx < registry2->range_count;
	     ++idx) {
		free(groups2[idx].complement);
		free(groups2[idx].boundary);
	}
	join_csr_free(&in.painted);
	join_csr_free(&in.row_terms);
	join_csr_free(&in.col_terms);
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
