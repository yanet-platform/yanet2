#include "lib/filter2/compiler/helper.h"
#include "common/registry.h"
#include "lib/filter2/filter.h"

#include <endian.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

// A fixed-width mask is a contiguous prefix when its inverse is a run of low
// bits, so incrementing that inverse carries all the way out and clears it.
static bool
mask_is_prefix64(uint64_t mask) {
	uint64_t inv = ~mask;
	return (inv & (inv + 1)) == 0;
}

bool
filter2_net4_mask_is_valid(const uint8_t mask[NET4_LEN]) {
	uint32_t bits;
	memcpy(&bits, mask, sizeof(bits));
	uint32_t inv = ~be32toh(bits);
	return (inv & (inv + 1)) == 0;
}

bool
filter2_net6_mask_is_valid(const uint8_t mask[NET6_LEN]) {
	uint64_t hi, lo;
	memcpy(&hi, mask, sizeof(hi));
	memcpy(&lo, mask + 8, sizeof(lo));
	return mask_is_prefix64(be64toh(hi)) && mask_is_prefix64(be64toh(lo));
}

// Host-side classification of registry ranges by content: ranges whose
// value lists are identical share a class id, so the joint stages can
// process each distinct (class, class) pair once and replay.
#define RANGE_CLASS_EMPTY 0xffffffffu

struct range_classes {
	uint32_t *class_of_range;
	uint32_t class_count;
};

static uint32_t
range_values_hash(const uint32_t *values, uint64_t count) {
	uint32_t h = 2166136261u;
	for (uint64_t idx = 0; idx < count; ++idx) {
		h = (h ^ values[idx]) * 16777619u;
	}
	return h ? h : 1;
}

static int
build_range_classes(
	const struct value_registry *registry, struct range_classes *out
) {
	uint32_t range_count = registry->range_count;
	out->class_of_range = malloc((range_count + 1) * 4);
	uint32_t cap = 16;
	while (cap < range_count * 2 + 16) {
		cap <<= 1;
	}
	uint32_t *slots = calloc(cap, 4);
	uint32_t *rep_of_class = NULL;
	if (out->class_of_range == NULL || slots == NULL) {
		goto error;
	}
	struct value_range *ranges = ADDR_OF(&registry->ranges);
	uint32_t class_count = 0;
	for (uint32_t range_idx = 0; range_idx < range_count; ++range_idx) {
		struct value_range *range = ranges + range_idx;
		if (range->count == 0) {
			// Every empty range is the same class by definition;
			// most ranges of a projected ruleset are empty and
			// none of them is worth a hash or a comparison.
			out->class_of_range[range_idx] = RANGE_CLASS_EMPTY;
			continue;
		}
		uint32_t *values = ADDR_OF(&range->values);
		uint32_t slot =
			range_values_hash(values, range->count) & (cap - 1);
		while (slots[slot] != 0) {
			uint32_t cls = slots[slot] - 1;
			struct value_range *rep = ranges + rep_of_class[cls];
			uint32_t *rep_values = ADDR_OF(&rep->values);
			if (rep->count == range->count &&
			    memcmp(rep_values,
				   values,
				   range->count * sizeof(uint32_t)) == 0) {
				out->class_of_range[range_idx] = cls;
				goto next_range;
			}
			slot = (slot + 1) & (cap - 1);
		}
		{
			uint32_t *grown =
				realloc(rep_of_class, (class_count + 1) * 4);
			if (grown == NULL) {
				goto error;
			}
			rep_of_class = grown;
		}
		slots[slot] = ++class_count;
		rep_of_class[class_count - 1] = range_idx;
		out->class_of_range[range_idx] = class_count - 1;
	next_range:;
	}
	out->class_count = class_count;
	free(slots);
	free(rep_of_class);
	return 0;

error:
	free(out->class_of_range);
	free(slots);
	free(rep_of_class);
	out->class_of_range = NULL;
	out->class_count = 0;
	return -1;
}

static void
free_range_classes(struct range_classes *classes) {
	free(classes->class_of_range);
	classes->class_of_range = NULL;
}

// Deduplicated pair key for one rule: the (class1, class2) combination.
struct pair_set {
	uint64_t *keys;
	uint32_t cap;
	uint32_t count;
	uint32_t just_inserted;
};

static int
pair_set_init(struct pair_set *set, uint32_t range_count) {
	set->cap = 16;
	while (set->cap < range_count * 2 + 16) {
		set->cap <<= 1;
	}
	set->keys = calloc(set->cap, 8);
	set->count = 0;
	set->just_inserted = 0;
	return set->keys == NULL ? -1 : 0;
}

static void
pair_set_free(struct pair_set *set) {
	free(set->keys);
	set->keys = NULL;
}

// Returns the slot of a recorded key, inserting one when unseen.
static uint32_t
pair_set_find(struct pair_set *set, uint64_t key) {
	uint32_t slot = (uint32_t)((key * 0x9e3779b97f4a7c15ULL) >> 32) &
			(set->cap - 1);
	while (set->keys[slot] != 0) {
		if (set->keys[slot] == key + 1) {
			set->just_inserted = 0;
			return slot;
		}
		slot = (slot + 1) & (set->cap - 1);
	}
	set->just_inserted = 1;
	set->keys[slot] = key + 1;
	++set->count;
	return slot;
}

// Returns 1 and records the key when seen for the first time, 0 when the
// pair was already processed.
static uint32_t
pair_set_check(struct pair_set *set, uint64_t key) {
	pair_set_find(set, key);
	return set->just_inserted;
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

struct value_set_ctx {
	struct value_table *table;
};

static int
value_table_set_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	struct value_set_ctx *set_ctx = (struct value_set_ctx *)data;
	uint32_t *value = value_table_get_ptr(set_ctx->table, v1, v2);
	if (*value != FILTER_RULE_INVALID) {
		return 0;
	}
	*value = idx;

	return 0;
}

int
filter2_merge_and_set_registry_values(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table
) {
	if (value_table_init(
		    table,
		    memory_context,
		    "filter:joint",
		    value_registry_capacity(registry1),
		    value_registry_capacity(registry2)
	    )) {
		return -1;
	}

	for (uint64_t v_idx = 0; v_idx < value_registry_capacity(registry1);
	     ++v_idx) {
		for (uint64_t h_idx = 0;
		     h_idx < value_registry_capacity(registry2);
		     ++h_idx) {
			*value_table_get_ptr(table, v_idx, h_idx) =
				FILTER_RULE_INVALID;
		}
	}

	struct value_set_ctx set_ctx;
	set_ctx.table = table;

	// Rules whose range pairs repeat process the identical cell set with
	// a higher rule index; the write-only-if-invalid contract makes them
	// no-ops, so only the first rule of each distinct pair runs.
	struct range_classes classes1 = {0};
	struct range_classes classes2 = {0};
	struct pair_set pairs = {0};
	if (build_range_classes(registry1, &classes1) ||
	    build_range_classes(registry2, &classes2) ||
	    pair_set_init(&pairs, registry1->range_count)) {
		goto error_classes;
	}
	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		uint64_t key = (uint64_t)classes1.class_of_range[range_idx] *
				       classes2.class_count +
			       classes2.class_of_range[range_idx];
		if (!pair_set_check(&pairs, key)) {
			continue;
		}
		if (value_registry_join_range(
			    registry1,
			    registry2,
			    range_idx,
			    value_table_set_action,
			    &set_ctx
		    )) {
			pair_set_free(&pairs);
			free_range_classes(&classes1);
			free_range_classes(&classes2);
			goto error_join;
		}
	}
	pair_set_free(&pairs);
	free_range_classes(&classes1);
	free_range_classes(&classes2);

	return 0;

error_classes:
	free_range_classes(&classes1);
	free_range_classes(&classes2);

error_join:
	value_table_free(table);

	return -1;
}

struct collect_ctx {
	struct value_table *value_table;
	struct remap_table remap_table;
};

static int
value_table_touch_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	(void)idx;
	struct collect_ctx *collect_ctx = (struct collect_ctx *)data;

	uint32_t *value = value_table_get_ptr(collect_ctx->value_table, v1, v2);
	if (remap_table_touch(&collect_ctx->remap_table, *value, value) < 0) {
		return -1;
	}
	return 0;
}

static int
merge_registry_values(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table
) {
	if (value_table_init(
		    table,
		    memory_context,
		    "filter:joint",
		    value_registry_capacity(registry1),
		    value_registry_capacity(registry2)
	    )) {
		return -1;
	}

	struct collect_ctx collect_ctx;
	collect_ctx.value_table = table;
	if (remap_table_init(
		    &collect_ctx.remap_table,
		    memory_context,
		    value_registry_capacity(registry1) *
			    value_registry_capacity(registry2)
	    )) {
		goto error_remap_table;
	}

	// A repeated range pair would open a generation touching the same
	// cells as an earlier one, which cannot refine the partition; skip
	// it.
	struct range_classes classes1 = {0};
	struct range_classes classes2 = {0};
	struct pair_set pairs = {0};
	if (build_range_classes(registry1, &classes1) ||
	    build_range_classes(registry2, &classes2) ||
	    pair_set_init(&pairs, registry1->range_count)) {
		goto error_classes_touch;
	}
	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		uint64_t key = (uint64_t)classes1.class_of_range[range_idx] *
				       classes2.class_count +
			       classes2.class_of_range[range_idx];
		if (!pair_set_check(&pairs, key)) {
			continue;
		}
		remap_table_new_gen(&collect_ctx.remap_table);
		if (value_registry_join_range(
			    registry1,
			    registry2,
			    range_idx,
			    value_table_touch_action,
			    &collect_ctx
		    )) {
			pair_set_free(&pairs);
			free_range_classes(&classes1);
			free_range_classes(&classes2);
			goto error_join;
		}
	}
	pair_set_free(&pairs);
	free_range_classes(&classes1);
	free_range_classes(&classes2);

	remap_table_compact(&collect_ctx.remap_table);
	value_table_compact(table, &collect_ctx.remap_table);
	remap_table_free(&collect_ctx.remap_table);

	return 0;

error_join:
	remap_table_free(&collect_ctx.remap_table);
	goto error_remap_table;

error_classes_touch:
	// fall through
error_remap_table:
	value_table_free(table);

	return -1;
}

struct record_ctx {
	struct value_table *table;
	uint32_t *values;
	uint32_t count;
	uint32_t cap;
};

static int
value_table_record_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	(void)idx;
	struct record_ctx *record_ctx = (struct record_ctx *)data;
	if (record_ctx->count == record_ctx->cap) {
		uint32_t new_cap = record_ctx->cap * 2 + 16;
		uint32_t *grown = realloc(record_ctx->values, new_cap * 4);
		if (grown == NULL) {
			return -1;
		}
		record_ctx->values = grown;
		record_ctx->cap = new_cap;
	}
	record_ctx->values[record_ctx->count++] =
		value_table_get(record_ctx->table, v1, v2);
	return 0;
}

static int
collect_registry_values(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
) {
	if (value_registry_init(registry, memory_context, "filter:joint")) {
		return -1;
	}

	// The first rule of each distinct non-empty range pair walks the join
	// and records the value list; every later rule with the same pair
	// replays it. Empty pairs need no walk: they contribute no values.
	struct range_classes classes1 = {0};
	struct range_classes classes2 = {0};
	struct pair_set pairs = {0};
	if (build_range_classes(registry1, &classes1) ||
	    build_range_classes(registry2, &classes2) ||
	    pair_set_init(&pairs, registry1->range_count)) {
		goto error_classes_collect;
	}
	uint32_t pair_cap = pairs.cap;
	uint32_t **pair_values = calloc(pair_cap, sizeof(uint32_t *));
	uint32_t *pair_lengths = calloc(pair_cap, 4);
	if (pair_values == NULL || pair_lengths == NULL) {
		goto error_pairs;
	}

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		if (value_registry_start(registry)) {
			goto error_pairs;
		}
		uint64_t key = (uint64_t)classes1.class_of_range[range_idx] *
				       classes2.class_count +
			       classes2.class_of_range[range_idx];
		uint32_t slot = pair_set_find(&pairs, key);
		if (pair_values[slot] != NULL) {
			for (uint32_t idx = 0; idx < pair_lengths[slot];
			     ++idx) {
				if (value_registry_collect(
					    registry, pair_values[slot][idx]
				    )) {
					goto error_pairs;
				}
			}
			continue;
		}
		// First occurrence: walk the join, record the values, and
		// replay them for this rule as well.
		struct record_ctx record_ctx = {.table = table};
		if (value_registry_join_range(
			    registry1,
			    registry2,
			    range_idx,
			    value_table_record_action,
			    &record_ctx
		    )) {
			free(record_ctx.values);
			goto error_pairs;
		}
		pair_values[slot] = record_ctx.values;
		pair_lengths[slot] = record_ctx.count;
		for (uint32_t idx = 0; idx < record_ctx.count; ++idx) {
			if (value_registry_collect(
				    registry, record_ctx.values[idx]
			    )) {
				goto error_pairs;
			}
		}
	}

	for (uint32_t slot = 0; slot < pair_cap; ++slot) {
		free(pair_values[slot]);
	}
	free(pair_values);
	free(pair_lengths);
	pair_set_free(&pairs);
	free_range_classes(&classes1);
	free_range_classes(&classes2);

	return 0;

error_pairs:
	for (uint32_t slot = 0; slot < pair_cap; ++slot) {
		free(pair_values[slot]);
	}
	free(pair_values);
	free(pair_lengths);
error_classes_collect:
	pair_set_free(&pairs);
	free_range_classes(&classes1);
	free_range_classes(&classes2);
	value_registry_fini(registry);
	return -1;
}

int
filter2_merge_and_collect_registry(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
) {
	if (merge_registry_values(
		    memory_context, registry1, registry2, table
	    )) {
		return -1;
	}

	if (collect_registry_values(
		    memory_context, registry1, registry2, table, registry
	    )) {
		value_table_free(table);
		return -1;
	}

	return 0;
}
