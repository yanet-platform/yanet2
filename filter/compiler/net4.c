#include "../rule.h"
#include "common/lpm.h"
#include "common/range_collector.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "helper.h"

////////////////////////////////////////////////////////////////////////////////

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
	struct value_table *table
) {
	uint32_t *values = ADDR_OF(&range_index->values);

	for (struct net4 *net4 = start; net4 < start + count; ++net4) {
		if (*(uint32_t *)net4->mask == 0x00000000)
			continue;
		uint32_t to =
			*(uint32_t *)net4->addr | ~*(uint32_t *)net4->mask;
		filter_key_inc(4, (uint8_t *)&to);

		uint32_t start =
			radix_lookup(&range_index->radix, 4, net4->addr);
		uint32_t stop = range_index->count;
		if (to != 0)
			stop = radix_lookup(
				&range_index->radix, 4, (uint8_t *)&to
			);

		for (uint32_t idx = start; idx < stop; ++idx) {
			if (value_table_touch(table, 0, values[idx]) < 0) {
				return -1;
			}
		}
	}

	return 0;
}

static inline void
net4_collect_registry(
	struct net4 *start,
	uint32_t count,
	struct lpm *lpm,
	struct value_registry *registry
) {
	for (struct net4 *net4 = start; net4 < start + count; ++net4) {
		uint32_t addr = *(uint32_t *)net4->addr;
		uint32_t mask = *(uint32_t *)net4->mask;
		uint32_t to = addr | ~mask;
		lpm4_collect_values(
			lpm,
			(uint8_t *)&addr,
			(uint8_t *)&to,
			lpm_collect_registry_iterator,
			registry
		);
	}
}

static inline int
collect_net4_values(
	struct memory_context *memory_context,
	const struct filter_rule *actions,
	uint32_t count,
	rule_get_net4_func get_net4,
	struct lpm *lpm,
	struct value_registry *registry
) {
	struct range_collector collector;
	if (range_collector_init(&collector, memory_context))
		goto error;

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {
		if (action->net4.src_count == 0 &&
		    action->net4.dst_count == 0) {
			continue;
		}

		struct net4 *nets;
		uint32_t net_count;
		get_net4(action, &nets, &net_count);

		for (struct net4 *net4 = nets; net4 < nets + net_count;
		     ++net4) {
			if (range4_collector_add(
				    &collector,
				    net4->addr,
				    __builtin_popcountll(*(uint32_t *)net4->mask
				    )
			    ))
				goto error_collector;
		}
	}
	if (lpm_init(lpm, memory_context)) {
		goto error_lpm;
	}
	struct range_index range_index;
	if (range_index_init(&range_index, memory_context)) {
		// FIXME error
		goto error_collector;
	}

	if (range_collector_collect(&collector, 4, lpm, &range_index)) {
		goto error_collector;
	}

	struct value_table table;
	if (value_table_init(&table, memory_context, 1, collector.count))
		goto error_vtab;

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {

		value_table_new_gen(&table);

		struct net4 *nets;
		uint32_t net_count;
		get_net4(action, &nets, &net_count);

		if (net4_collect_values(
			    nets, net_count, &range_index, &table
		    )) {
			// FIXME: error
		}
	}

	range_index_free(&range_index);

	value_table_compact(&table);
	lpm4_remap(lpm, &table);
	lpm4_compact(lpm);
	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {
		value_registry_start(registry);

		struct net4 *nets;
		uint32_t net_count;
		get_net4(action, &nets, &net_count);

		net4_collect_registry(nets, net_count, lpm, registry);
	}

	value_table_free(&table);
	range_collector_free(&collector, 4);
	return 0;

error_collector:
	range_collector_free(&collector, 4);
error_lpm:
	lpm_free(lpm);

error_vtab:

error:
	return -1;
}

////////////////////////////////////////////////////////////////////////////////

// Allows to initialize attribute for IPv4 source address.
int
FILTER_ATTR_COMPILER_INIT_FUNC(net4_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct lpm *lpm = memory_balloc(memory_context, sizeof(struct lpm));
	SET_OFFSET_OF(data, lpm);
	return collect_net4_values(
		memory_context,
		actions,
		actions_count,
		action_get_net4_src,
		lpm,
		registry
	);
}

// Allows to initialize attribute for IPv4 destination address.
int
FILTER_ATTR_COMPILER_INIT_FUNC(net4_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct lpm *lpm = memory_balloc(memory_context, sizeof(struct lpm));
	SET_OFFSET_OF(data, lpm);
	return collect_net4_values(
		memory_context,
		actions,
		actions_count,
		action_get_net4_dst,
		lpm,
		registry
	);
}

// Allows to free data for IPv4 classification.
static void
free_net4(void *data, struct memory_context *memory_context) {
	struct lpm *lpm = (struct lpm *)data;
	if (lpm == NULL)
		return;
	lpm_free(lpm);
	memory_bfree(memory_context, lpm, sizeof(struct lpm));
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
