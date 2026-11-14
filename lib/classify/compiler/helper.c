#include "lib/classify/compiler/helper.h"

#include "common/hash_index.h"
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
	for (uint32_t value = 0; value < value_space; ++value) {
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
		uint32_t match = UINT32_MAX;
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
		if (match != UINT32_MAX) {
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

static int
merge_pair_eq(uint32_t value, const void *data) {
	const struct merge_pair_ctx *pair_ctx =
		(const struct merge_pair_ctx *)data;
	return pair_ctx->pair_group1[value] != pair_ctx->group1 ||
	       pair_ctx->pair_group2[value] != pair_ctx->group2;
}

// Test builds collide the class hashes, so only the exact key compare keeps
// the classes apart, and count the cells the join interns.
#ifdef JOIN_TEST
#define JOIN_HASH_MASK 0xffu
uint64_t join_test_interned_cells;
#define JOIN_COUNT_INTERNED_CELL() (++join_test_interned_cells)
#else
#define JOIN_HASH_MASK UINT32_MAX
#define JOIN_COUNT_INTERNED_CELL() ((void)0)
#endif

// A pair set hashes as the xor of its pair hashes.
static inline uint32_t
join_pair_hash(uint32_t pair) {
	uint32_t hash = pair + 1;
	hash ^= hash >> 16;
	hash *= 0x85ebca6bu;
	hash ^= hash >> 13;
	hash *= 0xc2b2ae35u;
	return (hash ^ (hash >> 16)) & JOIN_HASH_MASK;
}

// A counted run of values.
struct join_span {
	const uint32_t *values;
	uint32_t count;
};

// The values held by exactly one of two ascending runs, ascending.
static inline uint32_t
join_xor(uint32_t *out, struct join_span a, struct join_span b) {
	uint32_t count = 0;
	uint32_t i = 0, j = 0;
	while (i < a.count && j < b.count) {
		if (a.values[i] < b.values[j]) {
			out[count++] = a.values[i++];
		} else if (a.values[i] > b.values[j]) {
			out[count++] = b.values[j++];
		} else {
			++i;
			++j;
		}
	}
	while (i < a.count) {
		out[count++] = a.values[i++];
	}
	while (j < b.count) {
		out[count++] = b.values[j++];
	}
	return count;
}

/*
 * A group as one side of its pairs.
 *
 * The side keeps its values or, over half of the dimension, their
 * complement; a column side also keeps the edges where its membership
 * changes.
 */
struct join_side {
	bool ready;
	bool flipped;
	struct join_span small;
	uint32_t *edges;
	uint32_t edge_count;
	// The complement, then the edges; it owns both.
	uint32_t *buffer;
};

// Fills the side of a group on its first use, a column side with its
// edges; it needs the shared scratch clear and leaves it clear.
//
// A group holds distinct values below the dimension; a repeated value would
// break the complement and cancel itself out of the pair sets.
static int
join_side_init(
	struct join_side *side,
	const struct value_range *range,
	uint32_t dim,
	uint8_t *marks,
	bool col
) {
	if (side->ready) {
		return 0;
	}
	side->ready = true;
	const uint32_t *values = ADDR_OF(&range->values);
	uint32_t count = (uint32_t)range->count;
	side->flipped = (uint64_t)count * 2 > dim;
	side->small =
		(struct join_span){values, side->flipped ? dim - count : count};
	if (!side->flipped && !col) {
		return 0;
	}

	// An edge lies between a member and a non-member, so a side and its
	// complement share them, at most two a value.
	size_t small = side->small.count;
	size_t complement = side->flipped ? small : 0;
	size_t edges = col ? 2 * small : 0;
	side->buffer = malloc((complement + edges + 1) * sizeof(uint32_t));
	if (side->buffer == NULL) {
		return -1;
	}
	side->edges = side->buffer + complement;
	for (uint32_t idx = 0; idx < count; ++idx) {
		marks[values[idx]] = 1;
	}
	if (side->flipped) {
		uint32_t *out = side->buffer;
		for (uint32_t value = 0; value < dim; ++value) {
			if (!marks[value]) {
				*out++ = value;
			}
		}
		side->small.values = side->buffer;
	}
	for (uint32_t idx = 0; col && idx < small; ++idx) {
		uint32_t value = side->small.values[idx];
		if (value > 0 && marks[value - 1] != marks[value]) {
			side->edges[side->edge_count++] = value;
		}
		if (value + 1 < dim && marks[value + 1] != marks[value]) {
			side->edges[side->edge_count++] = value + 1;
		}
	}
	for (uint32_t idx = 0; idx < count; ++idx) {
		marks[values[idx]] = 0;
	}
	return 0;
}

// The pairs holding each value of a dimension, ascending per value.
struct join_csr {
	size_t *offsets;
	uint32_t *items;
};

static int
join_csr_init(
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
			++csr->offsets[spans[pair].values[idx]];
		}
	}
	for (uint32_t value = 0; value < dim; ++value) {
		csr->offsets[value + 1] += csr->offsets[value];
	}
	csr->items = malloc((csr->offsets[dim] + 1) * sizeof(uint32_t));
	if (csr->items == NULL) {
		return -1;
	}
	// Filling backward moves every end to its start.
	for (uint32_t pair = pair_count; pair-- > 0;) {
		for (uint32_t idx = 0; idx < spans[pair].count; ++idx) {
			csr->items[--csr->offsets[spans[pair].values[idx]]] =
				pair;
		}
	}
	return 0;
}

static inline struct join_span
join_csr_get(const struct join_csr *csr, uint32_t value) {
	size_t offset = csr->offsets[value];
	return (struct join_span){
		csr->items + offset,
		(uint32_t)(csr->offsets[value + 1] - offset),
	};
}

/*
 * The classes interned by their exact pair keys.
 *
 * A key of up to two pairs is stored in place, a free place all-ones; a
 * longer one is an all-ones place and the pool offset of its count and
 * pairs. Twelve bytes a class keep the short keys out of the pool: a block
 * list of ten thousand sources by ten thousand destinations makes a
 * hundred million two-pair classes. The slots stay at most half full.
 */
struct join_class {
	uint32_t hash;
	uint32_t key[2];
};

struct join_slot {
	uint32_t hash;
	uint32_t id;
};

struct join_classes {
	struct join_class *items;
	uint32_t count;
	uint32_t mask;
	struct join_slot *slots;
	uint32_t *pool;
	size_t pool_count;
	size_t pool_capacity;
};

static struct join_span
join_class_key(const struct join_classes *classes, uint32_t id) {
	const uint32_t *key = classes->items[id].key;
	if (key[0] != UINT32_MAX || key[1] == UINT32_MAX) {
		return (struct join_span
		){key, (key[0] != UINT32_MAX) + (key[1] != UINT32_MAX)};
	}
	return (struct join_span){classes->pool + key[1] + 1,
				  classes->pool[key[1]]};
}

// Doubles the classes and rebuilds the slots over them.
static int
join_classes_grow(struct join_classes *classes) {
	if (classes->mask >= 1u << 30) {
		return -1;
	}
	uint32_t size = (classes->mask + 1) * 2;
	struct join_class *items =
		realloc(classes->items, (size_t)size / 2 * sizeof(*items));
	if (items == NULL) {
		return -1;
	}
	classes->items = items;
	free(classes->slots);
	classes->slots = malloc((size_t)size * sizeof(struct join_slot));
	if (classes->slots == NULL) {
		return -1;
	}
	memset(classes->slots, 0xff, (size_t)size * sizeof(struct join_slot));
	classes->mask = size - 1;
	for (uint32_t id = 0; id < classes->count; ++id) {
		uint32_t pos = items[id].hash & classes->mask;
		while (classes->slots[pos].id != UINT32_MAX) {
			pos = (pos + 1) & classes->mask;
		}
		classes->slots[pos] = (struct join_slot){items[id].hash, id};
	}
	return 0;
}

// The class of a key, numbered next when new; all-ones when the classes
// cannot grow.
static uint32_t
join_classes_intern(
	struct join_classes *classes, uint32_t hash, struct join_span key
) {
	if (classes->count == (classes->mask + 1) / 2 &&
	    join_classes_grow(classes)) {
		return UINT32_MAX;
	}
	struct join_slot *slot;
	for (uint32_t pos = hash & classes->mask;;
	     pos = (pos + 1) & classes->mask) {
		slot = classes->slots + pos;
		if (slot->id == UINT32_MAX) {
			break;
		}
		if (slot->hash != hash) {
			continue;
		}
		struct join_span stored = join_class_key(classes, slot->id);
		if (stored.count == key.count &&
		    !memcmp(stored.values,
			    key.values,
			    (size_t)key.count * sizeof(uint32_t))) {
			return slot->id;
		}
	}

	struct join_class *class = classes->items + classes->count;
	*class = (struct join_class){hash, {UINT32_MAX, UINT32_MAX}};
	if (key.count <= 2) {
		memcpy(class->key,
		       key.values,
		       (size_t)key.count * sizeof(uint32_t));
	} else {
		// Pool offsets stay below all-ones.
		size_t end = classes->pool_count + 1 + key.count;
		if (end >= UINT32_MAX) {
			return UINT32_MAX;
		}
		if (end > classes->pool_capacity) {
			size_t capacity = end + end / 4;
			uint32_t *pool =
				realloc(classes->pool,
					capacity * sizeof(uint32_t));
			if (pool == NULL) {
				return UINT32_MAX;
			}
			classes->pool = pool;
			classes->pool_capacity = capacity;
		}
		class->key[1] = (uint32_t)classes->pool_count;
		classes->pool[classes->pool_count] = key.count;
		memcpy(classes->pool + classes->pool_count + 1,
		       key.values,
		       (size_t)key.count * sizeof(uint32_t));
		classes->pool_count = end;
	}
	*slot = (struct join_slot){hash, classes->count};
	return classes->count++;
}

// A painted pair in the list of one column of a row.
struct join_node {
	uint32_t pair;
	uint32_t next;
};

/*
 * The scratch of a join of the distinct pairs.
 *
 * With a' the small indicator of a side, a side over half of its
 * dimension is a = 1 + a' over GF(2), so the pairs covering a cell are
 * S = K + R(v) + C(h) + P(v, h): the pairs complemented on both sides,
 * the column-complemented pairs painting the row, the row-complemented
 * pairs painting the column and the pairs painting the cell. A class is
 * keyed by S + K, the sum of the other three.
 */
struct join {
	uint32_t v_dim;
	uint32_t h_dim;
	uint32_t pair_count;
	// The sides of every group, the left ones first.
	struct join_side *sides;
	uint32_t side_count;
	// The sides of each pair, in one block owned by the first.
	struct join_side **rows;
	struct join_side **cols;
	// The pairs painting each row; the C terms of each column.
	struct join_csr painted;
	struct join_csr col_terms;
	struct join_classes classes;
	// The paint list nodes of the prepared row, linked from its heads.
	struct join_node *nodes;
	uint32_t node_capacity;

	// The arrays from here on are carved from the block.
	uint32_t *block;
	uint32_t *col_hash;
	// The left counts of the pairs complemented by row, which cover every
	// row they do not paint.
	uint32_t *left_base;
	// The prepared row: the counts of the pairs telling each cell from
	// its left and upper neighbours, the paint lists of its columns and
	// its R terms.
	uint32_t *left;
	uint32_t *up;
	uint32_t *heads;
	uint32_t *row_terms;
	uint32_t row_term_count;
	uint32_t row_hash;
	// The K terms.
	uint32_t *common;
	uint32_t common_count;
	// The paint, the row and column terms and the key of one cell.
	uint32_t *paint;
	uint32_t *terms;
	uint32_t *key;
};

static void
join_fini(struct join *join) {
	for (uint32_t idx = 0; join->sides && idx < join->side_count; ++idx) {
		free(join->sides[idx].buffer);
	}
	free(join->sides);
	free(join->rows);
	free(join->painted.offsets);
	free(join->painted.items);
	free(join->col_terms.offsets);
	free(join->col_terms.items);
	free(join->classes.items);
	free(join->classes.slots);
	free(join->classes.pool);
	free(join->block);
	free(join->nodes);
}

static int
join_init(
	struct join *join,
	const struct value_registry *registry1,
	const struct value_registry *registry2,
	const uint32_t *pair_group1,
	const uint32_t *pair_group2,
	uint32_t pair_count
) {
	uint32_t v_dim = value_registry_capacity(registry1);
	uint32_t h_dim = value_registry_capacity(registry2);
	size_t pairs = (size_t)pair_count + 1;
	*join = (struct join){
		.v_dim = v_dim,
		.h_dim = h_dim,
		.pair_count = pair_count,
		.side_count = registry1->range_count + registry2->range_count,
		.block = calloc(5 * (h_dim + pairs), sizeof(uint32_t)),
		.rows = malloc(2 * pairs * sizeof(struct join_side *)),
	};
	join->sides = calloc(join->side_count + 1, sizeof(struct join_side));
	uint8_t *marks = calloc(v_dim > h_dim ? v_dim : h_dim, 1);
	struct join_span *spans = malloc(2 * pairs * sizeof(*spans));
	int rc = -1;
	if (join->block == NULL || join->rows == NULL || join->sides == NULL ||
	    marks == NULL || spans == NULL) {
		goto done;
	}
	join->col_hash = join->block;
	join->left_base = join->col_hash + h_dim;
	join->left = join->left_base + h_dim;
	join->up = join->left + h_dim;
	join->heads = join->up + h_dim;
	join->row_terms = join->heads + h_dim;
	join->common = join->row_terms + pairs;
	join->paint = join->common + pairs;
	join->terms = join->paint + pairs;
	join->key = join->terms + pairs;
	join->cols = join->rows + pairs;

	const struct value_range *ranges1 = ADDR_OF(&registry1->ranges);
	const struct value_range *ranges2 = ADDR_OF(&registry2->ranges);
	struct join_side *sides2 = join->sides + registry1->range_count;
	struct join_span *col_spans = spans + pairs;
	for (uint32_t pair = 0; pair < pair_count; ++pair) {
		uint32_t group1 = pair_group1[pair];
		uint32_t group2 = pair_group2[pair];
		struct join_side *rows = join->sides + group1;
		struct join_side *cols = sides2 + group2;
		if (join_side_init(
			    rows, ranges1 + group1, v_dim, marks, false
		    ) ||
		    join_side_init(
			    cols, ranges2 + group2, h_dim, marks, true
		    )) {
			goto done;
		}
		join->rows[pair] = rows;
		join->cols[pair] = cols;
		spans[pair] = rows->small;
		col_spans[pair] = (struct join_span){0};
		if (!rows->flipped) {
			continue;
		}
		if (cols->flipped) {
			join->common[join->common_count++] = pair;
		}
		col_spans[pair] = cols->small;
		uint32_t hash = join_pair_hash(pair);
		for (uint32_t idx = 0; idx < cols->small.count; ++idx) {
			join->col_hash[cols->small.values[idx]] ^= hash;
		}
		for (uint32_t idx = 0; idx < cols->edge_count; ++idx) {
			++join->left_base[cols->edges[idx]];
		}
	}
	if (join_csr_init(&join->painted, v_dim, spans, pair_count) ||
	    join_csr_init(&join->col_terms, h_dim, col_spans, pair_count)) {
		goto done;
	}
	rc = 0;

done:
	free(spans);
	free(marks);
	return rc;
}

/*
 * Prepares a row for its cells.
 *
 * A cell differs from its left neighbour by the pairs covering the row
 * with an edge at the column, and from its upper one by the pairs with an
 * edge at the row covering the column. The wrapping sums are exact, the
 * true counts being at most the pair count.
 */
static int
join_row(struct join *join, uint32_t v) {
	uint32_t h_dim = join->h_dim;
	struct join_span painted = join_csr_get(&join->painted, v);
	memcpy(join->left, join->left_base, (size_t)h_dim * sizeof(uint32_t));
	memset(join->heads, 0xff, (size_t)h_dim * sizeof(uint32_t));
	join->row_term_count = 0;
	join->row_hash = 0;
	uint32_t node_count = 0;
	for (uint32_t idx = 0; idx < painted.count; ++idx) {
		uint32_t pair = painted.values[idx];
		const struct join_side *cols = join->cols[pair];
		if (cols->flipped) {
			join->row_terms[join->row_term_count++] = pair;
			join->row_hash ^= join_pair_hash(pair);
		}
		uint32_t sign = join->rows[pair]->flipped ? UINT32_MAX : 1;
		for (uint32_t edge = 0; edge < cols->edge_count; ++edge) {
			join->left[cols->edges[edge]] += sign;
		}
		if (node_count + cols->small.count > join->node_capacity) {
			// Node ids stay below all-ones.
			size_t capacity =
				2 * ((size_t)node_count + cols->small.count);
			if (capacity >= UINT32_MAX) {
				return -1;
			}
			struct join_node *nodes =
				realloc(join->nodes, capacity * sizeof(*nodes));
			if (nodes == NULL) {
				return -1;
			}
			join->nodes = nodes;
			join->node_capacity = (uint32_t)capacity;
		}
		for (uint32_t col = 0; col < cols->small.count; ++col) {
			uint32_t h = cols->small.values[col];
			join->nodes[node_count] =
				(struct join_node){pair, join->heads[h]};
			join->heads[h] = node_count++;
		}
	}

	if (v == 0) {
		return 0;
	}
	// The pairs with an edge at the row paint one of it and the row above.
	struct join_span above = join_csr_get(&join->painted, v - 1);
	uint32_t count = join_xor(join->key, painted, above);
	uint32_t base = 0;
	for (uint32_t idx = 0; idx < count; ++idx) {
		base += join->cols[join->key[idx]]->flipped;
	}
	for (uint32_t h = 0; h < h_dim; ++h) {
		join->up[h] = base;
	}
	for (uint32_t idx = 0; idx < count; ++idx) {
		const struct join_side *cols = join->cols[join->key[idx]];
		uint32_t sign = cols->flipped ? UINT32_MAX : 1;
		for (uint32_t col = 0; col < cols->small.count; ++col) {
			join->up[cols->small.values[col]] += sign;
		}
	}
	return 0;
}

// The class of a cell of the prepared row; all-ones on failure.
static uint32_t
join_cell(struct join *join, uint32_t h) {
	// The list holds the paint in descending order, so it fills from the
	// end.
	uint32_t *end = join->paint + join->pair_count;
	uint32_t *paint = end;
	uint32_t hash = join->row_hash ^ join->col_hash[h];
	for (uint32_t node = join->heads[h]; node != UINT32_MAX;
	     node = join->nodes[node].next) {
		*--paint = join->nodes[node].pair;
		hash ^= join_pair_hash(*paint);
	}
	struct join_span row = {join->row_terms, join->row_term_count};
	struct join_span terms = {
		join->terms,
		join_xor(join->terms, row, join_csr_get(&join->col_terms, h)),
	};
	uint32_t count = join_xor(
		join->key,
		terms,
		(struct join_span){paint, (uint32_t)(end - paint)}
	);
	return join_classes_intern(
		&join->classes, hash, (struct join_span){join->key, count}
	);
}

/*
 * Numbers every cell by its pair set, in order of first appearance.
 *
 * A cell equal to its left or upper neighbour takes its class, so a class
 * first appears at an interned cell. The cost is O(VH), the cells painted
 * by the small sides and the keys of the interned cells.
 */
static int
join_paint(struct join *join, struct value_table *table) {
	for (uint32_t v = 0; v < join->v_dim; ++v) {
		if (join_row(join, v)) {
			return -1;
		}
		uint32_t *cells = value_table_get_ptr(table, v, 0);
		const uint32_t *above =
			v ? value_table_get_ptr(table, v - 1, 0) : NULL;
		uint32_t id = 0;
		for (uint32_t h = 0; h < join->h_dim; ++h) {
			if (h == 0 || join->left[h] != 0) {
				if (v > 0 && join->up[h] == 0) {
					id = above[h];
				} else {
					JOIN_COUNT_INTERNED_CELL();
					id = join_cell(join, h);
					if (id == UINT32_MAX) {
						return -1;
					}
				}
			}
			cells[h] = id;
		}
	}
	// Only the class keys are read from here.
	free(join->classes.slots);
	join->classes.slots = NULL;
	return 0;
}

// Starts a range per pair and lists every class in the ranges of its
// pairs; the classes go in id order, so every range comes out ascending.
static int
join_collect(struct join *join, struct value_registry *registry) {
	for (uint32_t pair = 0; pair < join->pair_count; ++pair) {
		if (value_registry_start(registry)) {
			return -1;
		}
	}
	struct value_range *ranges = ADDR_OF(&registry->ranges);
	struct memory_context *context = ADDR_OF(&registry->memory_context);
	struct join_span common = {join->common, join->common_count};
	for (uint32_t id = 0; id < join->classes.count; ++id) {
		uint32_t count = join_xor(
			join->key, join_class_key(&join->classes, id), common
		);
		for (uint32_t idx = 0; idx < count; ++idx) {
			if (value_range_append(
				    context, ranges + join->key[idx], id
			    )) {
				return -1;
			}
		}
	}
	registry->max_value = join->classes.count - 1;
	return 0;
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
 * cells, so the stage operates on distinct pairs instead of all rules. A
 * cell's class is the set of pairs covering it, numbered by first
 * appearance in row-major order. On success the routine initializes the
 * value table holding the join results, the registry with one range per
 * pair holding the pair matching class identifiers in ascending order,
 * and fills the rule to pair mapping; a rule without a value on either
 * side keeps the invalid group mark.
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
	const uint32_t *rule_to_group1 = left->rule_groups;
	const struct value_registry *registry2 = &right->registry;
	const uint32_t *rule_to_group2 = right->rule_groups;
	struct value_registry *registry = &out->registry;
	uint32_t *rule_to_group;

	if (classifier_init(out, memory_context, rule_count)) {
		return -1;
	}
	rule_to_group = out->rule_groups;

	// Pair collection scratch, emptied here so a failure of the table
	// below unwinds through the common exit.
	uint32_t pair_alloc_count = rule_count ? rule_count : 1;
	uint32_t *pair_group1 = NULL;
	uint32_t *pair_group2 = NULL;
	struct hash_index pair_index = {0};
	bool pair_index_live = false;
	struct join join = {0};

	char table_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(table_name, sizeof(table_name), name, "table");
	if (value_table_init(
		    table,
		    memory_context,
		    table_name,
		    value_registry_capacity(registry1),
		    value_registry_capacity(registry2)
	    )) {
		goto error_free_pairs;
	}

	/*
	 * Collect the distinct group pairs into a hash index: the group of
	 * the first registry and the group of the second one each rule
	 * references.
	 */
	pair_group1 = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * pair_alloc_count
	);
	if (pair_group1 == NULL) {
		goto error_free_pairs;
	}

	pair_group2 = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * pair_alloc_count
	);
	if (pair_group2 == NULL) {
		goto error_free_pairs;
	}

	if (hash_index_init(&pair_index, memory_context, rule_count)) {
		goto error_free_pairs;
	}
	pair_index_live = true;

	uint32_t pair_count = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		uint32_t group1 = rule_to_group1[rule_idx];
		uint32_t group2 = rule_to_group2[rule_idx];
		rule_to_group[rule_idx] = FILTER_GROUP_INVALID;

		if (group1 == FILTER_GROUP_INVALID ||
		    group2 == FILTER_GROUP_INVALID) {
			continue;
		}

		uint32_t hash = merge_group_pair_hash(group1, group2);
		struct merge_pair_ctx pair_ctx = {
			pair_group1,
			pair_group2,
			group1,
			group2,
		};

		uint32_t pair = hash_index_lookup(
			&pair_index, hash, merge_pair_eq, &pair_ctx
		);
		if (pair == HASH_INDEX_INVALID) {
			pair = pair_count;
			if (hash_index_insert(&pair_index, hash, pair)) {
				goto error_free_pairs;
			}
			pair_group1[pair] = group1;
			pair_group2[pair] = group2;
			++pair_count;
		}

		rule_to_group[rule_idx] = pair;
	}

	if (join_init(
		    &join,
		    registry1,
		    registry2,
		    pair_group1,
		    pair_group2,
		    pair_count
	    ) ||
	    join_paint(&join, table)) {
		goto error_free_pairs;
	}

	char registry_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(
		registry_name, sizeof(registry_name), name, "registry"
	);
	if (value_registry_init(registry, memory_context, registry_name)) {
		goto error_free_pairs;
	}

	if (join_collect(&join, registry)) {
		goto error_free_pairs;
	}

	join_fini(&join);
	hash_index_fini(&pair_index);
	memory_bfree(
		memory_context, pair_group2, sizeof(uint32_t) * pair_alloc_count
	);
	memory_bfree(
		memory_context, pair_group1, sizeof(uint32_t) * pair_alloc_count
	);

	return 0;

error_free_pairs:
	// One unwind in reverse allocation order, every release guarded:
	// a failed join leaves no state behind, whatever the stage.
	value_table_free(table);
	value_registry_fini(registry);
	memset(registry, 0, sizeof(*registry));
	memory_bfree(
		memory_context,
		out->rule_groups,
		sizeof(uint32_t) * (rule_count ? rule_count : 1)
	);
	out->rule_groups = NULL;
	join_fini(&join);
	if (pair_index_live) {
		hash_index_fini(&pair_index);
	}
	if (pair_group2 != NULL) {
		memory_bfree(
			memory_context,
			pair_group2,
			sizeof(uint32_t) * pair_alloc_count
		);
	}
	if (pair_group1 != NULL) {
		memory_bfree(
			memory_context,
			pair_group1,
			sizeof(uint32_t) * pair_alloc_count
		);
	}

	return -1;
}

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
