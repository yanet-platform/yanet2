#include "ipfw.h"

#include <stdbool.h>

#include "common/memory.h"
#include "common/range_collector.h"
#include "common/registry.h"
#include "common/value.h"

typedef int (*action_check_collect)(struct filter_rule *action);

static inline int
action_check_has_v4(struct filter_rule *action) {
	return action->net4.src_count && action->net4.dst_count;
}

static inline int
action_check_has_v6(struct filter_rule *action) {
	return action->net6.src_count && action->net6.dst_count;
}

typedef void (*action_get_net4_func)(
	struct filter_rule *action, struct net4 **net, uint32_t *count
);

static void
action_get_net4_src(
	struct filter_rule *action, struct net4 **net, uint32_t *count
) {
	*net = action->net4.srcs;
	*count = action->net4.src_count;
}

static void
action_get_net4_dst(
	struct filter_rule *action, struct net4 **net, uint32_t *count
) {
	*net = action->net4.dsts;
	*count = action->net4.dst_count;
}

typedef void (*action_get_net6_func)(
	struct filter_rule *action, struct net6 **net, uint32_t *count
);

static void
action_get_net6_src(
	struct filter_rule *action, struct net6 **net, uint32_t *count
) {
	*net = action->net6.srcs;
	*count = action->net6.src_count;
}

static void
action_get_net6_dst(
	struct filter_rule *action, struct net6 **net, uint32_t *count
) {
	*net = action->net6.dsts;
	*count = action->net6.dst_count;
}

typedef void (*net6_get_part_func)(
	struct net6 *net, uint64_t *addr, uint64_t *mask
);

static void
net6_get_hi_part(struct net6 *net, uint64_t *addr, uint64_t *mask) {
	*addr = 0;
	for (size_t i = 0, shift = 0; i < 8; ++i, shift += 8) {
		*addr |= ((uint64_t)net->ip[8 + i]) << shift;
	}
	*mask = -1ull << (64 - net->pref_hi);
	*mask = be64toh(*mask);
	*addr &= *mask;
}

static void
net6_get_lo_part(struct net6 *net, uint64_t *addr, uint64_t *mask) {
	*addr = 0;
	for (size_t i = 0, shift = 0; i < 8; ++i, shift += 8) {
		*addr |= ((uint64_t)net->ip[8 + i]) << shift;
	}
	*mask = -1ull << (64 - net->pref_lo);
	*mask = be64toh(*mask);
	*addr &= *mask;
}

static inline int
lpm_collect_value_iterator(uint32_t value, void *data) {
	struct value_table *table = (struct value_table *)data;
	return value_table_touch(table, 0, value);
}

static inline void
net4_collect_values(
	struct net4 *start,
	uint32_t count,
	struct lpm *lpm,
	struct value_table *table
) {
	for (struct net4 *net4 = start; net4 < start + count; ++net4) {
		uint32_t addr = htobe32(net4->addr);
		uint32_t mask = htobe32(net4->mask);
		uint32_t to = addr | ~mask;
		lpm4_collect_values(
			lpm,
			(uint8_t *)&addr,
			(uint8_t *)&to,
			lpm_collect_value_iterator,
			table
		);
	}
}

static inline void
net6_collect_values(
	struct net6 *start,
	uint32_t count,
	net6_get_part_func get_part,
	struct lpm *lpm,
	struct value_table *table
) {
	for (struct net6 *net6 = start; net6 < start + count; ++net6) {
		uint64_t addr;
		uint64_t mask;
		get_part(net6, &addr, &mask);
		uint64_t to = addr | ~mask;
		lpm8_collect_values(
			lpm,
			(uint8_t *)&addr,
			(uint8_t *)&to,
			lpm_collect_value_iterator,
			table
		);
	}
}

struct net_collect_ctx {
	struct value_table *table;
	struct value_registry *registry;
};

static inline int
lpm_collect_registry_iterator(uint32_t value, void *data) {
	struct value_registry *registry = (struct value_registry *)data;
	return value_registry_collect(registry, value);
}

static inline void
net4_collect_registry(
	struct net4 *start,
	uint32_t count,
	struct lpm *lpm,
	struct value_registry *registry
) {
	for (struct net4 *net4 = start; net4 < start + count; ++net4) {
		uint32_t addr = htobe32(net4->addr);
		uint32_t mask = htobe32(net4->mask);
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

static inline void
net6_collect_registry(
	struct net6 *start,
	uint32_t count,
	net6_get_part_func get_part,
	struct lpm *lpm,
	struct value_registry *registry
) {
	for (struct net6 *net6 = start; net6 < start + count; ++net6) {
		uint64_t addr;
		uint64_t mask;
		get_part(net6, &addr, &mask);
		uint64_t to = addr | ~mask;
		lpm8_collect_values(
			lpm,
			(uint8_t *)&addr,
			(uint8_t *)&to,
			lpm_collect_registry_iterator,
			registry
		);
	}
}

static int
value_table_touch_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	(void)idx;
	struct value_table *table = (struct value_table *)data;
	if (value_table_touch(table, v1, v2) < 0)
		return -1;
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
		    value_registry_capacity(registry1),
		    value_registry_capacity(registry2)
	    )) {
		return -1;
	}

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		value_table_new_gen(table);
		value_registry_join_range(
			registry1,
			registry2,
			range_idx,
			value_table_touch_action,
			table
		);
	}

	value_table_compact(table);

	return 0;
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
	struct value_registry *registry
) {
	if (value_registry_init(registry, memory_context)) {
		return -1;
	}

	struct value_collect_ctx collect_ctx;
	collect_ctx.table = table;
	collect_ctx.registry = registry;

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		value_registry_start(registry);
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

static int
merge_and_collect_registry(
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

struct value_set_ctx {
	struct filter_rule *actions;
	struct value_table *table;
	struct value_registry *registry;
};

static int
action_list_is_term(struct value_registry *registry, uint32_t range_idx) {
	struct value_range *range = ADDR_OF(&registry->ranges) + range_idx;
	if (range->count == 0)
		return 0;

	uint32_t action_id =
		ADDR_OF(&registry->values)[range->from + range->count - 1];
	return !(action_id & ACTION_NON_TERMINATE);
}

static int
value_table_set_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data) {
	struct value_set_ctx *set_ctx = (struct value_set_ctx *)data;
	uint32_t prev_value = value_table_get(set_ctx->table, v1, v2);

	if (!action_list_is_term(set_ctx->registry, prev_value)) {
		/*
		 * FIXME: we assume value table produces increasing sequence
		 * of values - this is important for value registry handling.
		 */
		int res = value_table_touch(set_ctx->table, v1, v2);

		if (res <= 0)
			return res;

		if (value_registry_start(set_ctx->registry))
			return -1;

		struct value_range *copy_range =
			ADDR_OF(&set_ctx->registry->ranges) + prev_value;

		for (uint32_t ridx = copy_range->from;
		     ridx < copy_range->from + copy_range->count;
		     ++ridx) {
			value_registry_collect(
				set_ctx->registry,
				ADDR_OF(&set_ctx->registry->values)[ridx]
			);
		}

		value_registry_collect(
			set_ctx->registry, set_ctx->actions[idx].action
		);
	}

	return 0;
}

static int
set_registry_values(
	struct memory_context *memory_context,
	struct filter_rule *actions,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
) {
	if (value_table_init(
		    table,
		    memory_context,
		    value_registry_capacity(registry1),
		    value_registry_capacity(registry2)
	    )) {
		return -1;
	}

	if (value_registry_init(registry, memory_context)) {
		goto error_registry;
	}

	if (value_registry_start(registry))
		return -1;

	struct value_set_ctx set_ctx;
	set_ctx.actions = actions;
	set_ctx.table = table;
	set_ctx.registry = registry;

	for (uint32_t range_idx = 0; range_idx < registry1->range_count;
	     ++range_idx) {
		value_table_new_gen(table);
		if (value_registry_join_range(
			    registry1,
			    registry2,
			    range_idx,
			    value_table_set_action,
			    &set_ctx
		    ))
			goto error_merge;
	}

	return 0;

error_merge:
	value_registry_free(registry);

error_registry:
	value_table_free(table);

	return 0;
}

static inline int
collect_net4_values(
	struct memory_context *memory_context,
	struct filter_rule *actions,
	uint32_t count,
	action_check_collect check_collect,
	action_get_net4_func get_net4,
	struct lpm *lpm,
	struct value_registry *registry
) {

	struct range_collector collector;
	if (range_collector_init(&collector, memory_context))
		goto error;

	for (struct filter_rule *action = actions; action < actions + count;
	     ++action) {

		if (!check_collect(action))
			continue;

		struct net4 *nets;
		uint32_t net_count;
		get_net4(action, &nets, &net_count);

		for (struct net4 *net4 = nets; net4 < nets + net_count;
		     ++net4) {
			uint32_t addr = htobe32(net4->addr);
			if (range4_collector_add(
				    &collector,
				    (uint8_t *)&addr,
				    __builtin_popcountll(net4->mask)
			    ))
				goto error_collector;
		}
	}
	if (lpm_init(lpm, memory_context)) {
		goto error_lpm;
	}
	if (range_collector_collect(&collector, 4, lpm)) {
		goto error_collector;
	}

	struct value_table table;
	if (value_table_init(&table, memory_context, 1, collector.count))
		goto error_vtab;

	for (struct filter_rule *action = actions; action < actions + count;
	     ++action) {

		if (!check_collect(action))
			continue;

		value_table_new_gen(&table);

		struct net4 *nets;
		uint32_t net_count;
		get_net4(action, &nets, &net_count);

		net4_collect_values(nets, net_count, lpm, &table);
	}

	value_table_compact(&table);
	lpm4_remap(lpm, &table);
	lpm4_compact(lpm);

	if (value_registry_init(registry, memory_context))
		goto error_reg;

	for (struct filter_rule *action = actions; action < actions + count;
	     ++action) {
		value_registry_start(registry);

		if (!check_collect(action))
			continue;

		struct net4 *nets;
		uint32_t net_count;
		get_net4(action, &nets, &net_count);

		net4_collect_registry(nets, net_count, lpm, registry);
	}

	value_table_free(&table);
	return 0;

error_reg:
	value_table_free(&table);
error_collector:
	range_collector_free(&collector, 4);
error_lpm:
	lpm_free(lpm);

error_vtab:

error:
	return -1;
}

static int
collect_net6_values(
	struct memory_context *memory_context,
	struct filter_rule *actions,
	uint32_t count,
	action_check_collect check_collect,
	action_get_net6_func get_net6,
	net6_get_part_func get_part,
	struct lpm *lpm,
	struct value_registry *registry
) {

	struct range_collector collector;
	if (range_collector_init(&collector, memory_context))
		goto error;

	for (struct filter_rule *action = actions; action < actions + count;
	     ++action) {

		if (!check_collect(action))
			continue;

		struct net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);

		for (struct net6 *net6 = nets; net6 < nets + net_count;
		     ++net6) {
			uint64_t addr;
			uint64_t mask;
			get_part(net6, &addr, &mask);

			if (range8_collector_add(
				    &collector,
				    (uint8_t *)&addr,
				    __builtin_popcountll(mask)
			    ))
				goto error_collector;
		}
	}
	if (lpm_init(lpm, memory_context)) {
		goto error_lpm;
	}
	if (range_collector_collect(&collector, 8, lpm)) {
		goto error_collector;
	}

	struct value_table table;
	if (value_table_init(&table, memory_context, 1, collector.count))
		goto error_vtab;

	for (struct filter_rule *action = actions; action < actions + count;
	     ++action) {

		if (!check_collect(action))
			continue;

		value_table_new_gen(&table);

		struct net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);

		net6_collect_values(nets, net_count, get_part, lpm, &table);
	}

	value_table_compact(&table);
	lpm8_remap(lpm, &table);
	lpm8_compact(lpm);

	if (value_registry_init(registry, memory_context))
		goto error_reg;

	for (struct filter_rule *action = actions; action < actions + count;
	     ++action) {
		value_registry_start(registry);

		if (!check_collect(action))
			continue;

		struct net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);

		net6_collect_registry(nets, net_count, get_part, lpm, registry);
	}

	value_table_free(&table);
	return 0;

error_reg:
	value_table_free(&table);
error_collector:
	range_collector_free(&collector, 8);
error_lpm:
	lpm_free(lpm);

error_vtab:

error:
	return -1;
}

typedef void (*action_get_port_range_func)(
	struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
);

static inline void
get_port_range_src(
	struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
) {
	*ranges = action->transport.srcs;
	*count = action->transport.src_count;
}

static inline void
get_port_range_dst(
	struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
) {
	*ranges = action->transport.dsts;
	*count = action->transport.dst_count;
}

static inline int
collect_port_values(
	struct memory_context *memory_context,
	struct filter_rule *actions,
	uint32_t count,
	action_check_collect check_collect,
	action_get_port_range_func get_port_range,
	struct value_table *table,
	struct value_registry *registry
) {
	if (value_table_init(table, memory_context, 1, 65536))
		return -1;

	for (struct filter_rule *action = actions; action < actions + count;
	     ++action) {
		if (!check_collect(action))
			continue;

		value_table_new_gen(table);

		struct filter_port_range *port_ranges;
		uint32_t port_range_count;
		get_port_range(action, &port_ranges, &port_range_count);
		for (struct filter_port_range *ports = port_ranges;
		     ports < port_ranges + port_range_count;
		     ++ports) {
			if (ports->to - ports->from == 65535)
				continue;
			for (uint32_t port = ports->from; port <= ports->to;
			     ++port) {
				value_table_touch(table, 0, port);
			}
		}
	}

	value_table_compact(table);

	if (value_registry_init(registry, memory_context))
		goto error_reg;

	for (struct filter_rule *action = actions; action < actions + count;
	     ++action) {
		value_registry_start(registry);

		if (!check_collect(action))
			continue;

		struct filter_port_range *port_ranges;
		uint32_t port_range_count;
		get_port_range(action, &port_ranges, &port_range_count);
		for (struct filter_port_range *ports = port_ranges;
		     ports < port_ranges + port_range_count;
		     ++ports) {
			for (uint32_t port = ports->from; port <= ports->to;
			     ++port) {
				value_registry_collect(
					registry,
					value_table_get(table, 0, port)
				);
			}
		}
	}

	return 0;

error_reg:
	value_table_free(table);
	return -1;
}

int
filter_compiler_init(
	struct filter_compiler *filter,
	struct memory_context *memory_context,
	struct filter_rule *actions,
	uint32_t count
) {
	int res = memory_context_init_from(
		&filter->memory_context, memory_context, "filter"
	);
	if (res < 0) {
		return res;
	}

	struct value_registry src_net4_registry;
	res = collect_net4_values(
		&filter->memory_context,
		actions,
		count,
		action_check_has_v4,
		action_get_net4_src,
		&filter->src_net4,
		&src_net4_registry
	);
	if (res < 0) {
		return res;
	}

	struct value_registry dst_net4_registry;
	res = collect_net4_values(
		&filter->memory_context,
		actions,
		count,
		action_check_has_v4,
		action_get_net4_dst,
		&filter->dst_net4,
		&dst_net4_registry
	);
	if (res < 0) {
		return res;
	}

	struct value_registry src_port4_registry;
	res = collect_port_values(
		&filter->memory_context,
		actions,
		count,
		action_check_has_v4,
		get_port_range_src,
		&filter->src_port4,
		&src_port4_registry
	);
	if (res < 0) {
		return res;
	}

	struct value_registry dst_port4_registry;
	res = collect_port_values(
		&filter->memory_context,
		actions,
		count,
		action_check_has_v4,
		get_port_range_dst,
		&filter->dst_port4,
		&dst_port4_registry
	);
	if (res < 0) {
		return res;
	}

	struct value_registry transport_port4_registry;
	res = merge_and_collect_registry(
		&filter->memory_context,
		&src_port4_registry,
		&dst_port4_registry,
		&filter->v4_lookups.transport_port,
		&transport_port4_registry
	);
	if (res < 0) {
		return res;
	}

	struct value_registry net4_registry;
	res = merge_and_collect_registry(
		&filter->memory_context,
		&src_net4_registry,
		&dst_net4_registry,
		&filter->v4_lookups.network,
		&net4_registry
	);
	if (res < 0) {
		return res;
	}

	return set_registry_values(
		&filter->memory_context,
		actions,
		&net4_registry,
		&transport_port4_registry,
		&filter->v4_lookups.result,
		&filter->v4_lookups.result_registry
	);

#ifndef IPFW_SKIP_NET6

	struct value_registry src_net6_hi_registry;
	collect_net6_values(
		&filter->memory_context,
		actions,
		count,
		action_check_has_v6,
		action_get_net6_src,
		net6_get_hi_part,
		&filter->src_net6_hi,
		&src_net6_hi_registry
	);

	struct value_registry src_net6_lo_registry;
	collect_net6_values(
		&filter->memory_context,
		actions,
		count,
		action_check_has_v6,
		action_get_net6_src,
		net6_get_lo_part,
		&filter->src_net6_lo,
		&src_net6_lo_registry
	);

	struct value_registry dst_net6_hi_registry;
	collect_net6_values(
		&filter->memory_context,
		actions,
		count,
		action_check_has_v6,
		action_get_net6_dst,
		net6_get_hi_part,
		&filter->dst_net6_hi,
		&dst_net6_hi_registry
	);

	struct value_registry dst_net6_lo_registry;
	collect_net6_values(
		&filter->memory_context,
		actions,
		count,
		action_check_has_v6,
		action_get_net6_dst,
		net6_get_lo_part,
		&filter->dst_net6_lo,
		&dst_net6_lo_registry
	);

	struct value_registry src_port6_registry;
	collect_port_values(
		&filter->memory_context,
		actions,
		count,
		action_check_has_v6,
		get_port_range_src,
		&filter->src_port6,
		&src_port6_registry
	);

	struct value_registry dst_port6_registry;
	collect_port_values(
		&filter->memory_context,
		actions,
		count,
		action_check_has_v6,
		get_port_range_dst,
		&filter->dst_port6,
		&dst_port6_registry
	);

	struct value_registry net6_hi_registry;
	merge_and_collect_registry(
		&filter->memory_context,
		&src_net6_hi_registry,
		&dst_net6_hi_registry,
		&filter->v6_lookups.network_hi,
		&net6_hi_registry
	);

	struct value_registry net6_lo_registry;
	merge_and_collect_registry(
		&filter->memory_context,
		&src_net6_lo_registry,
		&dst_net6_lo_registry,
		&filter->v6_lookups.network_lo,
		&net6_lo_registry
	);

	struct value_registry transport_port6_registry;
	merge_and_collect_registry(
		&filter->memory_context,
		&src_port6_registry,
		&dst_port6_registry,
		&filter->v6_lookups.transport_port,
		&transport_port6_registry
	);

	struct value_registry net6_registry;
	merge_and_collect_registry(
		&filter->memory_context,
		&net6_hi_registry,
		&net6_lo_registry,
		&filter->v6_lookups.network,
		&net6_registry
	);

	set_registry_values(
		&filter->memory_context,
		actions,
		&net6_registry,
		&transport_port6_registry,
		&filter->v6_lookups.result,
		&filter->v6_lookups.result_registry
	);

#endif

	return 0;
}
