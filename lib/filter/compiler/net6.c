#include "../rule.h"

#include "common/lpm.h"
#include "common/range_collector.h"

#include "common/registry.h"

#include "../classifiers/net6.h"

#include "declare.h"

////////////////////////////////////////////////////////////////////////////////

typedef void (*action_get_net6_func)(
	const struct filter_rule *rule, struct net6 **net, uint32_t *count
);

static inline void
action_get_net6_src(
	const struct filter_rule *rule, struct net6 **net, uint32_t *count
) {
	*net = rule->net6.srcs;
	*count = rule->net6.src_count;
}

static inline void
action_get_net6_dst(
	const struct filter_rule *action, struct net6 **net, uint32_t *count
) {
	*net = action->net6.dsts;
	*count = action->net6.dst_count;
}

////////////////////////////////////////////////////////////////////////////////

typedef void (*net6_get_part_func)(
	struct net6 *net, uint8_t **addr, uint8_t **mask
);

static inline void
net6_get_hi_part(struct net6 *net, uint8_t **addr, uint8_t **mask) {
	*addr = net->addr;
	*mask = net->mask;
}

static inline void
net6_get_lo_part(struct net6 *net, uint8_t **addr, uint8_t **mask) {
	*addr = net->addr + 8;
	*mask = net->mask + 8;
}

////////////////////////////////////////////////////////////////////////////////

static inline void
net6_normalize(struct net6 *src, struct net6 *dst) {
	memcpy(dst->addr, src->addr, 16);
	memcpy(dst->mask, src->mask, 16);
	for (uint8_t idx = 0; idx < 16; ++idx)
		dst->addr[idx] &= src->mask[idx];
}

static inline int
collect_net6_range(
	struct memory_context *memory_context,
	const struct filter_rule **actions,
	uint32_t count,
	action_get_net6_func get_net6,
	net6_get_part_func get_part,
	struct lpm *lpm,
	struct range_index *ri
) {
	struct range_collector collector;
	if (range_collector_init(&collector, memory_context))
		goto error;

	for (const struct filter_rule **action_ptr = actions;
	     action_ptr < actions + count;
	     ++action_ptr) {

		if (*action_ptr == NULL)
			continue;
		const struct filter_rule *action = *action_ptr;

		struct net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);

		for (struct net6 *rule_net = nets; rule_net < nets + net_count;
		     ++rule_net) {
			struct net6 net6;
			net6_normalize(rule_net, &net6);

			uint8_t *addr;
			uint8_t *mask;
			get_part(&net6, &addr, &mask);

			if (range8_collector_add(
				    &collector,
				    addr,
				    __builtin_popcountll(*(uint64_t *)mask)
			    ))
				goto error_collector;
		}
	}
	if (lpm_init(lpm, memory_context)) {
		goto error_lpm;
	}

	if (range_index_init(ri, memory_context)) {
		goto error_ri_init;
	}

	if (range_collector_collect(&collector, 8, lpm, ri)) {
		goto error_collect;
	}

	range_collector_free(&collector, 8);

	return 0;

error_collect:
	range_index_free(ri);
	lpm_free(lpm);
	range_collector_free(&collector, 8);
	return -1;

error_ri_init:
	lpm_free(lpm);
	range_collector_free(&collector, 8);
	return -1;

error_lpm:
	lpm_free(lpm);

error_collector:
	range_collector_free(&collector, 8);

error:
	return -1;
}

/*
 * Resolve range index boundaries for 64-bit address and mask
 */
static inline void
net6_part_range_index_bound(
	const struct range_index *range_index,
	const uint8_t *addr,
	const uint8_t *mask,
	uint32_t *start,
	uint32_t *stop
) {
	uint8_t next[8];
	*(uint64_t *)next = *(uint64_t *)addr | ~*(uint64_t *)mask;
	filter_key_inc(8, next);
	*start = radix_lookup(&range_index->radix, 8, addr);
	*stop = range_index->count;
	if (*(uint64_t *)next != 0)
		*stop = radix_lookup(&range_index->radix, 8, next);
}

/*
 * Resolve hi and lo network partitions range index boundaries
 */
static inline void
net6_range_index_bounds(
	const struct range_index *range_index_hi,
	const struct range_index *range_index_lo,
	struct net6 *net6,
	uint32_t *start_hi,
	uint32_t *stop_hi,
	uint32_t *start_lo,
	uint32_t *stop_lo
) {
	uint8_t *from;
	uint8_t *mask;
	net6_get_hi_part(net6, &from, &mask);
	net6_part_range_index_bound(
		range_index_hi, from, mask, start_hi, stop_hi
	);
	net6_get_lo_part(net6, &from, &mask);
	net6_part_range_index_bound(
		range_index_lo, from, mask, start_lo, stop_lo
	);
}

/*
 * Touch each network group set.
 * Networks are distributed by groups where any network in a group has
 * exact the same set of rules the network contains in. The routine makes
 * new remap_table generation for each group and then touches all subitems
 * of networks containing in the group.
 */
static inline int
touch_network_ranges(
	struct memory_context *memory_context,
	struct net6 *all_nets,
	struct value_range *net_ranges,
	uint32_t net_range_count,
	const struct range_index *ri_hi,
	const struct range_index *ri_lo,
	struct value_table *value_table
) {

	struct remap_table remap_table;
	if (remap_table_init(
		    &remap_table,
		    memory_context,
		    (ri_hi->max_value + 1) * (ri_lo->max_value + 1)
	    )) {
		return -1;
	}

	uint32_t *values_hi = ADDR_OF(&ri_hi->values);
	uint32_t *values_lo = ADDR_OF(&ri_lo->values);

	for (uint32_t net_range_idx = 0; net_range_idx < net_range_count;
	     ++net_range_idx) {
		remap_table_new_gen(&remap_table);

		uint32_t *values = ADDR_OF(&net_ranges[net_range_idx].values);
		for (uint32_t idx = 0; idx < net_ranges[net_range_idx].count;
		     ++idx) {
			struct net6 net6 = all_nets[values[idx]];

			// Skip `any` network
			if (*(uint64_t *)net6.mask == 0 &&
			    *(uint64_t *)(net6.mask + 8) == 0)
				continue;

			uint32_t start_hi;
			uint32_t stop_hi;
			uint32_t start_lo;
			uint32_t stop_lo;

			net6_range_index_bounds(
				ri_hi,
				ri_lo,
				&net6,
				&start_hi,
				&stop_hi,
				&start_lo,
				&stop_lo
			);

			for (uint32_t idx_hi = start_hi; idx_hi < stop_hi;
			     ++idx_hi) {
				for (uint32_t idx_lo = start_lo;
				     idx_lo < stop_lo;
				     ++idx_lo) {
					uint32_t *value = value_table_get_ptr(
						value_table,
						values_hi[idx_hi],
						values_lo[idx_lo]
					);
					if (remap_table_touch(
						    &remap_table, *value, value
					    ) < 0) {
						goto error;
					}
				}
			}
		}
	}

	remap_table_compact(&remap_table);
	value_table_compact(value_table, &remap_table);
	remap_table_free(&remap_table);

	return 0;

error:
	remap_table_free(&remap_table);
	return -1;
}

static inline int
collect_network_values(
	struct net6 *all_nets,
	uint32_t all_net_count,
	const struct range_index *ri_hi,
	const struct range_index *ri_lo,
	struct value_table *value_table,
	struct value_registry *registry
) {
	uint32_t *values_hi = ADDR_OF(&ri_hi->values);
	uint32_t *values_lo = ADDR_OF(&ri_lo->values);

	for (uint32_t net_idx = 0; net_idx < all_net_count; ++net_idx) {
		struct net6 net6 = all_nets[net_idx];

		value_registry_start(registry);

		uint32_t start_hi;
		uint32_t stop_hi;
		uint32_t start_lo;
		uint32_t stop_lo;

		net6_range_index_bounds(
			ri_hi,
			ri_lo,
			&net6,
			&start_hi,
			&stop_hi,
			&start_lo,
			&stop_lo
		);

		for (uint32_t idx_hi = start_hi; idx_hi < stop_hi; ++idx_hi) {
			for (uint32_t idx_lo = start_lo; idx_lo < stop_lo;
			     ++idx_lo) {
				if (value_registry_collect(
					    registry,
					    value_table_get(
						    value_table,
						    values_hi[idx_hi],
						    values_lo[idx_lo]
					    )
				    )) {
					return -1;
				}
			}
		}
	}

	return 0;
}

/*
 * Collect all networks in a radix and distribute them into groups where
 * each groups contains networks with the same set of rules the networks
 * contains in.
 */
static inline int
build_net6_info(
	struct memory_context *memory_context,
	const struct filter_rule **actions,
	uint32_t action_count,
	action_get_net6_func get_net6,

	struct radix *net_radix,

	struct net6 **nets,
	uint64_t *net_count,

	struct value_range **net_ranges,
	uint32_t *net_range_count
) {
	*nets = NULL;
	*net_count = 0;

	*net_ranges = NULL;
	*net_range_count = 0;

	radix_init(net_radix, memory_context);

	for (const struct filter_rule **action_ptr = actions;
	     action_ptr < actions + action_count;
	     ++action_ptr) {

		if (*action_ptr == NULL)
			continue;
		const struct filter_rule *action = *action_ptr;

		struct net6 *rule_nets;
		uint32_t rule_net_count;
		get_net6(action, &rule_nets, &rule_net_count);

		for (struct net6 *rule_net = rule_nets;
		     rule_net < rule_nets + rule_net_count;
		     ++rule_net) {
			struct net6 net6;
			net6_normalize(rule_net, &net6);

			if (radix_lookup(net_radix, 32, net6.addr) !=
			    RADIX_VALUE_INVALID)
				continue;

			radix_insert(net_radix, 32, net6.addr, *net_count);
			if (mem_array_expand_exp(
				    memory_context,
				    (void **)nets,
				    sizeof(**nets),
				    net_count
			    )) {
				goto error_nets;
			}
			(*nets)[*net_count - 1] = net6;
		}
	}

	if (*net_count == 0) {
		return 0;
	}

	struct value_table net_table;
	if (value_table_init(&net_table, memory_context, 1, *net_count)) {
		goto error_nets;
	}

	struct remap_table net_remap;
	if (remap_table_init(&net_remap, memory_context, *net_count)) {
		goto error_table;
	}

	for (const struct filter_rule **action_ptr = actions;
	     action_ptr < actions + action_count;
	     ++action_ptr) {

		if (*action_ptr == NULL)
			continue;
		const struct filter_rule *action = *action_ptr;

		remap_table_new_gen(&net_remap);

		struct net6 *rule_nets;
		uint32_t rule_net_count;
		get_net6(action, &rule_nets, &rule_net_count);

		for (struct net6 *rule_net = rule_nets;
		     rule_net < rule_nets + rule_net_count;
		     ++rule_net) {
			struct net6 net6;
			net6_normalize(rule_net, &net6);

			uint32_t net_idx =
				radix_lookup(net_radix, 32, net6.addr);
			uint32_t *v =
				value_table_get_ptr(&net_table, 0, net_idx);
			if (remap_table_touch(&net_remap, *v, v) < 0) {
				goto error_touch;
			}
		}
	}

	remap_table_compact(&net_remap);
	value_table_compact(&net_table, &net_remap);
	remap_table_free(&net_remap);

	for (uint32_t idx = 0; idx < *net_count; ++idx)
		if (value_table_get(&net_table, 0, idx) >= *net_range_count)
			*net_range_count =
				value_table_get(&net_table, 0, idx) + 1;
	*net_ranges = (struct value_range *)memory_balloc(
		memory_context, sizeof(struct value_range) * *net_range_count
	);
	if (*net_ranges == NULL)
		goto error_table;

	memset(*net_ranges, 0, sizeof(struct value_range) * *net_range_count);
	for (uint32_t net_idx = 0; net_idx < *net_count; ++net_idx) {
		if (value_range_append(
			    memory_context,
			    *net_ranges +
				    value_table_get(&net_table, 0, net_idx),
			    net_idx
		    )) {
			goto error_append;
		}
	}

	value_table_free(&net_table);

	return 0;

error_append:
	for (uint32_t idx = 0; idx < *net_range_count; ++idx) {
		struct value_range *range = *net_ranges + idx;
		mem_array_free_exp(
			memory_context,
			ADDR_OF(&range->values),
			sizeof(uint32_t),
			range->count
		);
	}

	memory_bfree(
		memory_context,
		*net_ranges,
		sizeof(struct value_range) * *net_range_count
	);

	*net_ranges = NULL;
	*net_range_count = 0;

	goto error_table;

error_touch:
	remap_table_free(&net_remap);

error_table:
	value_table_free(&net_table);

error_nets:
	mem_array_free_exp(memory_context, *nets, sizeof(**nets), *net_count);
	*nets = NULL;
	*net_count = 0;

	radix_free(net_radix);

	return -1;
}

static inline int
merge_net6_range(
	struct memory_context *memory_context,
	const struct filter_rule **actions,
	uint32_t count,
	action_get_net6_func get_net6,
	const struct range_index *ri_hi,
	const struct range_index *ri_lo,
	struct value_table *table,
	struct value_registry *registry
) {
	struct net6 *nets;
	uint64_t net_cnt = 0;
	struct radix net_radix;

	struct value_range *net_ranges;
	uint32_t net_range_count = 0;

	if (build_net6_info(
		    memory_context,
		    actions,
		    count,
		    get_net6,
		    &net_radix,
		    &nets,
		    &net_cnt,
		    &net_ranges,
		    &net_range_count
	    )) {
		return -1;
	}

	if (value_table_init(
		    table,
		    memory_context,
		    ri_hi->max_value + 1,
		    ri_lo->max_value + 1
	    )) {
		goto error_info;
	}

	if (touch_network_ranges(
		    memory_context,
		    nets,
		    net_ranges,
		    net_range_count,
		    ri_hi,
		    ri_lo,
		    table
	    )) {
		goto error_table;
	}

	struct value_registry net_registry;
	value_registry_init(&net_registry, memory_context);

	if (collect_network_values(
		    nets, net_cnt, ri_hi, ri_lo, table, &net_registry
	    )) {
		goto error_net_registry;
	}

	value_registry_init(registry, memory_context);

	for (const struct filter_rule **action_ptr = actions;
	     action_ptr < actions + count;
	     ++action_ptr) {
		// A value range should be created even for empty rules
		if (value_registry_start(registry))
			goto error_registry;

		if (*action_ptr == NULL)
			continue;
		const struct filter_rule *action = *action_ptr;

		struct net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);
		for (struct net6 *rule_net = nets; rule_net < nets + net_count;
		     ++rule_net) {
			struct net6 net6;
			net6_normalize(rule_net, &net6);

			uint32_t net_idx =
				radix_lookup(&net_radix, 32, net6.addr);

			struct value_range *rng =
				ADDR_OF(&net_registry.ranges) + net_idx;
			uint32_t *vls = ADDR_OF(&rng->values);
			for (uint32_t idx = 0; idx < rng->count; ++idx) {
				if (value_registry_collect(
					    registry, vls[idx]
				    )) {
					goto error_registry;
				}
			}
		}
	}

	radix_free(&net_radix);
	value_registry_fini(&net_registry);
	for (uint32_t idx = 0; idx < net_range_count; ++idx) {
		struct value_range *range = net_ranges + idx;
		mem_array_free_exp(
			memory_context,
			ADDR_OF(&range->values),
			sizeof(uint32_t),
			range->count
		);
	}

	memory_bfree(
		memory_context,
		net_ranges,
		sizeof(struct value_range) * net_range_count
	);

	mem_array_free_exp(memory_context, nets, sizeof(*nets), net_cnt);

	return 0;

error_registry:
	value_registry_fini(registry);

error_net_registry:
	value_registry_fini(&net_registry);

error_table:
	value_table_free(table);

error_info:
	for (uint32_t idx = 0; idx < net_range_count; ++idx) {
		struct value_range *range = net_ranges + idx;
		mem_array_free_exp(
			memory_context,
			ADDR_OF(&range->values),
			sizeof(uint32_t),
			range->count
		);
	}

	memory_bfree(
		memory_context,
		net_ranges,
		sizeof(struct value_range) * net_range_count
	);

	mem_array_free_exp(memory_context, nets, sizeof(*nets), net_cnt);

	radix_free(&net_radix);

	return -1;
}

////////////////////////////////////////////////////////////////////////////////
// Initialization
////////////////////////////////////////////////////////////////////////////////

static inline int
init_net6(
	struct value_registry *registry,
	action_get_net6_func get_net6,
	void **data,
	const struct filter_rule **actions,
	size_t count,
	struct memory_context *memory_context
) {
	struct net6_classifier *net6 =
		memory_balloc(memory_context, sizeof(struct net6_classifier));
	if (net6 == NULL)
		return -1;
	SET_OFFSET_OF(data, net6);

	struct range_index ri_hi;
	if (collect_net6_range(
		    memory_context,
		    actions,
		    count,
		    get_net6,
		    net6_get_hi_part,
		    &net6->hi,
		    &ri_hi
	    )) {
		goto error_hi;
	}

	struct range_index ri_lo;
	if (collect_net6_range(
		    memory_context,
		    actions,
		    count,
		    get_net6,
		    net6_get_lo_part,
		    &net6->lo,
		    &ri_lo
	    )) {
		goto error_lo;
	}

	if (merge_net6_range(
		    memory_context,
		    actions,
		    count,
		    get_net6,
		    &ri_hi,
		    &ri_lo,
		    &net6->comb,
		    registry
	    )) {
		goto error_merge;
	}

	range_index_free(&ri_hi);
	range_index_free(&ri_lo);

	return 0;

error_merge:
	range_index_free(&ri_lo);
	lpm_free(&net6->lo);

error_lo:
	range_index_free(&ri_hi);
	lpm_free(&net6->hi);

error_hi:
	SET_OFFSET_OF(data, NULL);
	memory_bfree(memory_context, net6, sizeof(struct net6_classifier));

	return -1;
}

// Allows to initialize attribute for IPv6 destination address.
int
FILTER_ATTR_COMPILER_INIT_FUNC(net6_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t actions_count,
	struct memory_context *memory_context
) {
	return init_net6(
		registry,
		action_get_net6_src,
		data,
		rules,
		actions_count,
		memory_context
	);
}

// Allows to initialize attribute for IPv6 source address.
int
FILTER_ATTR_COMPILER_INIT_FUNC(net6_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t actions_count,
	struct memory_context *memory_context
) {
	return init_net6(
		registry,
		action_get_net6_dst,
		data,
		rules,
		actions_count,
		memory_context
	);
}

////////////////////////////////////////////////////////////////////////////////
// Free
////////////////////////////////////////////////////////////////////////////////

// Allows to free data for IPv6 classification.
static inline void
free_net6(void *data, struct memory_context *memory_context) {
	if (data == NULL)
		return;
	struct net6_classifier *c = (struct net6_classifier *)data;
	if (c == NULL)
		return;
	lpm_free(&c->lo);
	lpm_free(&c->hi);
	value_table_free(&c->comb);
	memory_bfree(memory_context, c, sizeof(struct net6_classifier));
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net6_src)(
	void *data, struct memory_context *memory_context
) {
	free_net6(data, memory_context);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net6_dst)(
	void *data, struct memory_context *memory_context
) {
	free_net6(data, memory_context);
}
