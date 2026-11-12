#include <stdlib.h>

#include "../rule.h"
#include "common/fast_lpm_build.h"
#include "common/range_collector.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "helper.h"

typedef void (*rule_get_net4_func)(
	const struct filter_rule *rule, struct net4 **net, uint32_t *count
);

static inline void
action_get_net4_src(
	const struct filter_rule *rule, struct net4 **net, uint32_t *count
) {
	*net = rule->net4.srcs;
	*count = rule->net4.src_count;
}

static inline void
action_get_net4_dst(
	const struct filter_rule *action, struct net4 **net, uint32_t *count
) {
	*net = action->net4.dsts;
	*count = action->net4.dst_count;
}

static inline int
net4_collect_values(
	struct net4 *start,
	uint32_t count,
	struct range_index *range_index,
	struct value_table *table,
	struct remap_table *remap_table
) {
	uint32_t *values = ADDR_OF(&range_index->values);

	for (struct net4 *net4 = start; net4 < start + count; ++net4) {
		if (*(const net4_word *)net4->mask == 0x00000000) {
			continue;
		}
		uint32_t to = *(const net4_word *)net4->addr |
			      ~*(const net4_word *)net4->mask;
		filter_key_inc(4, (uint8_t *)&to);

		uint32_t start =
			radix_lookup(&range_index->radix, 4, net4->addr);
		uint32_t stop = range_index->count;
		if (to != 0) {
			stop = radix_lookup(
				&range_index->radix, 4, (uint8_t *)&to
			);
		}

		for (uint32_t idx = start; idx < stop; ++idx) {
			uint32_t *value =
				value_table_get_ptr(table, 0, values[idx]);
			if (remap_table_touch(remap_table, *value, value) < 0) {
				return -1;
			}
		}
	}

	return 0;
}

/*
 * Collects the classes of every network of a rule into the registry.
 *
 * A network spans whole ranges of the partition, in key order; a class
 * repeated by adjacent ranges is collected once.
 */
static inline int
net4_collect_registry(
	struct net4 *start,
	uint32_t count,
	const struct range_index *range_index,
	const uint32_t *classes,
	struct value_registry *registry
) {
	for (struct net4 *net4 = start; net4 < start + count; ++net4) {
		uint32_t to = *(const net4_word *)net4->addr |
			      ~*(const net4_word *)net4->mask;
		filter_key_inc(4, (uint8_t *)&to);

		uint32_t first =
			radix_lookup(&range_index->radix, 4, net4->addr);
		uint32_t stop = range_index->count;
		if (to != 0) {
			stop = radix_lookup(
				&range_index->radix, 4, (uint8_t *)&to
			);
		}

		uint32_t prev = RANGE_INDEX_VALUE_NONE;
		for (uint32_t idx = first; idx < stop; ++idx) {
			if (classes[idx] == prev) {
				continue;
			}
			prev = classes[idx];
			if (value_registry_collect(registry, prev)) {
				return -1;
			}
		}
	}

	return 0;
}

static inline int
collect_net4_values(
	struct memory_context *memory_context,
	const struct filter_rule **actions,
	uint32_t count,
	rule_get_net4_func get_net4,
	struct fast_lpm *lpm,
	struct value_registry *registry
) {
	struct range_collector collector;
	if (range_collector_init(&collector, memory_context)) {
		goto error;
	}

	for (const struct filter_rule **action_ptr = actions;
	     action_ptr < actions + count;
	     ++action_ptr) {
		if (*action_ptr == NULL) {
			continue;
		}
		const struct filter_rule *action = *action_ptr;

		if (action->net4.src_count == 0 &&
		    action->net4.dst_count == 0) {
			continue;
		}

		struct net4 *nets;
		uint32_t net_count;
		get_net4(action, &nets, &net_count);

		for (struct net4 *net4 = nets; net4 < nets + net_count;
		     ++net4) {
			uint32_t mask = *(const net4_word *)net4->mask;
			if (range4_collector_add(
				    &collector,
				    net4->addr,
				    __builtin_popcountll(mask)
			    )) {
				goto error_collector;
			}
		}
	}
	uint32_t *classes = NULL;
	if (fast_lpm_init(lpm, memory_context, "lpm", 4)) {
		goto error_lpm;
	}
	struct range_index range_index;
	if (range_index_init(&range_index, memory_context)) {
		goto error_lpm;
	}

	if (range_collector_collect(&collector, 4, &range_index)) {
		goto error_range_collect;
	}

	struct value_table table;
	if (value_table_init(
		    &table, memory_context, "net4", 1, collector.count
	    )) {
		goto error_table;
	}

	struct remap_table remap_table;
	if (remap_table_init(&remap_table, memory_context, collector.count)) {
		goto error_remap;
	}

	for (const struct filter_rule **action_ptr = actions;
	     action_ptr < actions + count;
	     ++action_ptr) {

		remap_table_new_gen(&remap_table);

		if (*action_ptr == NULL) {
			continue;
		}

		const struct filter_rule *action = *action_ptr;

		struct net4 *nets;
		uint32_t net_count;
		get_net4(action, &nets, &net_count);

		if (net4_collect_values(
			    nets, net_count, &range_index, &table, &remap_table
		    )) {
			goto error_net_collect;
		}
	}

	remap_table_compact(&remap_table);
	value_table_compact(&table, &remap_table);

	// The region identifiers of the partition map to their classes
	// through the compacted table.
	const uint32_t *regions = ADDR_OF(&range_index.values);
	classes =
		malloc(sizeof(*classes) *
		       (range_index.count ? range_index.count : 1));
	if (classes == NULL) {
		goto error_net_collect;
	}
	for (uint32_t idx = 0; idx < range_index.count; ++idx) {
		classes[idx] = value_table_get(&table, 0, regions[idx]);
	}
	if (range_index_build_lpm_values(&range_index, 4, classes, lpm)) {
		goto error_net_collect;
	}

	for (const struct filter_rule **action_ptr = actions;
	     action_ptr < actions + count;
	     ++action_ptr) {
		// A value range should be created even for empty rules
		if (value_registry_start(registry)) {
			goto error_net_collect;
		}

		if (*action_ptr == NULL) {
			continue;
		}
		const struct filter_rule *action = *action_ptr;

		struct net4 *nets;
		uint32_t net_count;
		get_net4(action, &nets, &net_count);

		if (net4_collect_registry(
			    nets, net_count, &range_index, classes, registry
		    )) {
			goto error_net_collect;
		}
	}

	free(classes);
	remap_table_free(&remap_table);
	value_table_free(&table);
	range_index_free(&range_index);
	range_collector_free(&collector, 4);
	return 0;

error_net_collect:
	free(classes);
	remap_table_free(&remap_table);

error_remap:
	value_table_free(&table);

error_table:

error_range_collect:
	range_index_free(&range_index);

error_lpm:
	fast_lpm_free(lpm);

error_collector:
	range_collector_free(&collector, 4);

error:
	return -1;
}

// Allows to initialize attribute for IPv4 source address.
int
FILTER_ATTR_COMPILER_INIT_FUNC(net4_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct fast_lpm *lpm =
		memory_balloc(memory_context, sizeof(struct fast_lpm));
	if (lpm == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, lpm);
	if (collect_net4_values(
		    memory_context,
		    actions,
		    actions_count,
		    action_get_net4_src,
		    lpm,
		    registry
	    )) {
		SET_OFFSET_OF(data, NULL);
		memory_bfree(memory_context, lpm, sizeof(struct fast_lpm));
		return -1;
	}
	return 0;
}

// Allows to initialize attribute for IPv4 destination address.
int
FILTER_ATTR_COMPILER_INIT_FUNC(net4_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct fast_lpm *lpm =
		memory_balloc(memory_context, sizeof(struct fast_lpm));
	if (lpm == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, lpm);
	if (collect_net4_values(
		    memory_context,
		    actions,
		    actions_count,
		    action_get_net4_dst,
		    lpm,
		    registry
	    )) {
		SET_OFFSET_OF(data, NULL);
		memory_bfree(memory_context, lpm, sizeof(struct fast_lpm));
		return -1;
	}
	return 0;
}

// Allows to free data for IPv4 classification.
static void
free_net4(void *data, struct memory_context *memory_context) {
	struct fast_lpm *lpm = (struct fast_lpm *)data;
	if (lpm == NULL) {
		return;
	}
	fast_lpm_free(lpm);
	memory_bfree(memory_context, lpm, sizeof(struct fast_lpm));
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net4_src)(
	void *data, struct memory_context *memory_context
) {
	free_net4(data, memory_context);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net4_dst)(
	void *data, struct memory_context *memory_context
) {
	free_net4(data, memory_context);
}
