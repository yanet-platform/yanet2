#include "lib/filter/compiler/helper.h"
#include "common/registry.h"
#include "lib/filter/filter.h"

#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// For each range, finds the earliest range built from the same value sets.
//
// Ranges built from identical value sets produce identical results, so later
// passes can handle the first one and reuse it for the duplicates. The result
// maps each range to its earliest match; a range that matches only itself is a
// first occurrence. A null result means scratch memory ran out and every range
// must be processed.
static uint32_t *
registry_pair_representatives(
	struct value_registry *registry1, struct value_registry *registry2
) {
	uint64_t count = registry1->range_count;
	uint32_t *rep = (uint32_t *)malloc(count * sizeof(uint32_t));
	if (rep == NULL)
		return NULL;

	uint64_t need = (uint64_t)count * 2;
	uint64_t cap = 8;
	while (cap < need)
		cap <<= 1;

	uint32_t *slot = (uint32_t *)malloc(cap * sizeof(uint32_t));
	uint64_t *slot_hash = (uint64_t *)malloc(cap * sizeof(uint64_t));
	if (slot == NULL || slot_hash == NULL) {
		free(rep);
		free(slot);
		free(slot_hash);
		return NULL;
	}
	for (uint32_t idx = 0; idx < cap; ++idx)
		slot[idx] = UINT32_MAX;

	struct value_range *ranges1 = ADDR_OF(&registry1->ranges);
	struct value_range *ranges2 = ADDR_OF(&registry2->ranges);

	for (uint64_t idx = 0; idx < count; ++idx) {
		struct value_range *r1 = &ranges1[idx];
		struct value_range *r2 = &ranges2[idx];
		uint32_t *v1 = ADDR_OF(&r1->values);
		uint32_t *v2 = ADDR_OF(&r2->values);

		uint64_t hash = 1469598103934665603ULL;
		hash = (hash ^ r1->count) * 1099511628211ULL;
		for (uint64_t k = 0; k < r1->count; ++k)
			hash = (hash ^ v1[k]) * 1099511628211ULL;
		hash = (hash ^ r2->count) * 1099511628211ULL;
		for (uint64_t k = 0; k < r2->count; ++k)
			hash = (hash ^ v2[k]) * 1099511628211ULL;

		uint32_t pos = (uint32_t)(hash & (cap - 1));
		uint32_t found = UINT32_MAX;
		while (slot[pos] != UINT32_MAX) {
			uint32_t other = slot[pos];
			struct value_range *o1 = &ranges1[other];
			struct value_range *o2 = &ranges2[other];
			if (slot_hash[pos] == hash && o1->count == r1->count &&
			    o2->count == r2->count &&
			    (r1->count == 0 ||
			     memcmp(ADDR_OF(&o1->values),
				    v1,
				    r1->count * sizeof(uint32_t)) == 0) &&
			    (r2->count == 0 ||
			     memcmp(ADDR_OF(&o2->values),
				    v2,
				    r2->count * sizeof(uint32_t)) == 0)) {
				found = other;
				break;
			}
			pos = (pos + 1) & (cap - 1);
		}

		if (found != UINT32_MAX) {
			rep[idx] = found;
		} else {
			slot[pos] = (uint32_t)idx;
			slot_hash[pos] = hash;
			rep[idx] = (uint32_t)idx;
		}
	}

	free(slot);
	free(slot_hash);

	return rep;
}

int
init_dummy_registry(
	struct memory_context *memory_context,
	uint32_t actions,
	struct value_registry *registry
) {
	int res = value_registry_init(registry, memory_context);
	if (res < 0) {
		return res;
	}
	for (uint32_t i = 0; i < actions; ++i) {
		res = value_registry_start(registry);
		if (res < 0) {
			value_registry_fini(registry);
			return res;
		}
		res = value_registry_collect(registry, 0);
		if (res < 0) {
			value_registry_fini(registry);
			return res;
		}
	}
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

struct value_set_ctx {
	struct value_table *table;
};

static int
value_table_set_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	struct value_set_ctx *set_ctx = (struct value_set_ctx *)data;
	uint32_t *value = value_table_get_ptr(set_ctx->table, v1, v2);
	if (*value != FILTER_RULE_INVALID)
		return 0;
	*value = idx;

	return 0;
}

int
merge_and_set_registry_values(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table
) {
	if (value_table_init(
		    table,
		    memory_context,
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

	// A repeated value set only revisits cells an earlier range already
	// claimed with a smaller index, so it cannot change the result and is
	// skipped.
	uint32_t *rep = registry_pair_representatives(registry1, registry2);

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		if (rep != NULL && rep[range_idx] != range_idx)
			continue;
		if (value_registry_join_range(
			    registry1,
			    registry2,
			    range_idx,
			    value_table_set_action,
			    &set_ctx
		    ))
			goto error_join;
	}

	free(rep);

	return 0;

error_join:
	free(rep);
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
	if (remap_table_touch(&collect_ctx->remap_table, *value, value))
		return -1;
	return 0;
}

static int
merge_registry_values(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	const uint32_t *rep
) {
	if (value_table_init(
		    table,
		    memory_context,
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

	// A repeated value set refines the same cells the same way, so only the
	// first occurrence needs its own generation.
	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		if (rep != NULL && rep[range_idx] != range_idx)
			continue;
		remap_table_new_gen(&collect_ctx.remap_table);
		value_registry_join_range(
			registry1,
			registry2,
			range_idx,
			value_table_touch_action,
			&collect_ctx
		);
	}

	remap_table_compact(&collect_ctx.remap_table);
	value_table_compact(table, &collect_ctx.remap_table);
	remap_table_free(&collect_ctx.remap_table);

	return 0;

error_remap_table:
	value_table_free(table);

	return -1;
}

struct value_collect_ctx {
	struct value_table *table;
	struct value_registry *registry;
};

static int
value_table_collect_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	(void)idx;
	struct value_collect_ctx *collect_ctx =
		(struct value_collect_ctx *)data;
	return value_registry_collect(
		collect_ctx->registry,
		value_table_get(collect_ctx->table, v1, v2)
	);

	return 0;
}

static int
collect_registry_values(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry,
	const uint32_t *rep
) {
	if (value_registry_init(registry, memory_context)) {
		return -1;
	}

	struct value_collect_ctx collect_ctx;
	collect_ctx.table = table;
	collect_ctx.registry = registry;

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		if (value_registry_start(registry))
			return -1;

		// A repeated value set collects the same values, so copy the
		// first occurrence's result instead of walking the join again.
		if (rep != NULL && rep[range_idx] != range_idx) {
			struct value_range *src =
				ADDR_OF(&registry->ranges) + rep[range_idx];
			uint32_t *src_values = ADDR_OF(&src->values);
			uint64_t src_count = src->count;
			for (uint64_t k = 0; k < src_count; ++k) {
				if (value_registry_collect(
					    registry, src_values[k]
				    ))
					return -1;
			}
			continue;
		}

		value_registry_join_range(
			registry1,
			registry2,
			range_idx,
			value_table_collect_action,
			&collect_ctx
		);
	}

	return 0;
}

int
merge_and_collect_registry(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
) {
	uint32_t *rep = registry_pair_representatives(registry1, registry2);

	if (merge_registry_values(
		    memory_context, registry1, registry2, table, rep
	    )) {
		free(rep);
		return -1;
	}

	if (collect_registry_values(
		    memory_context, registry1, registry2, table, registry, rep
	    )) {
		free(rep);
		value_table_free(table);
		return -1;
	}

	free(rep);

	return 0;
}
