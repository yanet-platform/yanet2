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
	const struct filter_rule *actions,
	uint32_t count,
	action_get_net6_func get_net6,
	net6_get_part_func get_part,
	struct lpm *lpm,
	struct range_index *ri
) {
	struct range_collector collector;
	if (range_collector_init(&collector, memory_context))
		goto error;

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {

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
		// FIXME error
		goto error_collector;
	}

	if (range_collector_collect(&collector, 8, lpm, ri)) {
		goto error_collector;
	}

	range_collector_free(&collector, 8);

	return 0;

error_lpm:

error_collector:

error:
	return -1;
}

static inline int
merge_net6_range(
	struct memory_context *memory_context,
	const struct filter_rule *actions,
	uint32_t count,
	action_get_net6_func get_net6,
	const struct range_index *ri_hi,
	const struct range_index *ri_lo,
	struct value_table *table,
	struct value_registry *registry
) {
	if (value_table_init(
		    table,
		    memory_context,
		    ri_hi->max_value + 1,
		    ri_lo->max_value + 1
	    )) {
		return -1;
	}

	uint32_t net_cnt = 0;

	struct radix rdx;
	radix_init(&rdx, memory_context);

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {

		value_table_new_gen(table);

		uint32_t *values_hi = ADDR_OF(&ri_hi->values);
		uint32_t *values_lo = ADDR_OF(&ri_lo->values);

		struct net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);

		for (struct net6 *rule_net = nets; rule_net < nets + net_count;
		     ++rule_net) {
			struct net6 net6;
			net6_normalize(rule_net, &net6);

			if (radix_lookup(&rdx, 32, net6.addr) !=
			    RADIX_VALUE_INVALID)
				continue;

			radix_insert(&rdx, 32, net6.addr, net_cnt++);

			uint8_t *from_hi;
			uint8_t *mask_hi;
			net6_get_hi_part(&net6, &from_hi, &mask_hi);
			uint8_t to_hi[8];
			*(uint64_t *)to_hi =
				*(uint64_t *)from_hi | ~*(uint64_t *)mask_hi;
			filter_key_inc(8, to_hi);
			uint32_t start_hi =
				radix_lookup(&ri_hi->radix, 8, from_hi);
			uint32_t stop_hi = ri_hi->count;
			if (*(uint64_t *)to_hi != 0)
				stop_hi = radix_lookup(&ri_hi->radix, 8, to_hi);

			uint8_t *from_lo;
			uint8_t *mask_lo;
			net6_get_lo_part(&net6, &from_lo, &mask_lo);
			uint8_t to_lo[8];
			*(uint64_t *)to_lo =
				*(uint64_t *)from_lo | ~*(uint64_t *)mask_lo;
			filter_key_inc(8, to_lo);
			uint32_t start_lo =
				radix_lookup(&ri_lo->radix, 8, from_lo);
			uint32_t stop_lo = ri_lo->count;
			if (*(uint64_t *)to_lo != 0)
				stop_lo = radix_lookup(&ri_lo->radix, 8, to_lo);

			if (!(*(uint64_t *)from_hi == 0 &&
			      *(uint64_t *)to_hi == 0 &&
			      *(uint64_t *)from_lo == 0 &&
			      *(uint64_t *)to_lo == 0)) {

				for (uint32_t idx_hi = start_hi;
				     idx_hi < stop_hi;
				     ++idx_hi) {
					for (uint32_t idx_lo = start_lo;
					     idx_lo < stop_lo;
					     ++idx_lo) {
						if (value_table_touch(
							    table,
							    values_hi[idx_hi],
							    values_lo[idx_lo]
						    ) < 0) {
							return -1;
						}
					}
				}
			}
		}
	}

	uint32_t *values_hi = ADDR_OF(&ri_hi->values);
	uint32_t *values_lo = ADDR_OF(&ri_lo->values);

	struct value_registry net_registry;
	value_registry_init(&net_registry, memory_context);

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {

		struct net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);

		for (struct net6 *rule_net = nets; rule_net < nets + net_count;
		     ++rule_net) {
			struct net6 net6;
			net6_normalize(rule_net, &net6);

			uint32_t net_idx = radix_lookup(&rdx, 32, net6.addr);
			if (net_idx < net_registry.range_count)
				continue;

			value_registry_start(&net_registry);

			uint8_t *from_hi;
			uint8_t *mask_hi;
			net6_get_hi_part(&net6, &from_hi, &mask_hi);
			uint8_t to_hi[8];
			*(uint64_t *)to_hi =
				*(uint64_t *)from_hi | ~*(uint64_t *)mask_hi;
			filter_key_inc(8, to_hi);
			uint32_t start_hi =
				radix_lookup(&ri_hi->radix, 8, from_hi);
			uint32_t stop_hi = ri_hi->count;
			if (*(uint64_t *)to_hi != 0)
				stop_hi = radix_lookup(&ri_hi->radix, 8, to_hi);

			uint8_t *from_lo;
			uint8_t *mask_lo;
			net6_get_lo_part(&net6, &from_lo, &mask_lo);
			uint8_t to_lo[8];
			*(uint64_t *)to_lo =
				*(uint64_t *)from_lo | ~*(uint64_t *)mask_lo;
			filter_key_inc(8, to_lo);
			uint32_t start_lo =
				radix_lookup(&ri_lo->radix, 8, from_lo);
			uint32_t stop_lo = ri_lo->count;
			if (*(uint64_t *)to_lo != 0)
				stop_lo = radix_lookup(&ri_lo->radix, 8, to_lo);

			for (uint32_t idx_hi = start_hi; idx_hi < stop_hi;
			     ++idx_hi) {
				for (uint32_t idx_lo = start_lo;
				     idx_lo < stop_lo;
				     ++idx_lo) {
					if (value_registry_collect(
						    &net_registry,
						    value_table_get(
							    table,
							    values_hi[idx_hi],
							    values_lo[idx_lo]
						    )
					    )) {
						return -1;
					}
				}
			}
		}
	}

	value_registry_init(registry, memory_context);

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {

		if (value_registry_start(registry))
			return -1;

		struct net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);
		for (struct net6 *rule_net = nets; rule_net < nets + net_count;
		     ++rule_net) {
			struct net6 net6;
			net6_normalize(rule_net, &net6);

			uint32_t net_idx = radix_lookup(&rdx, 32, net6.addr);

			struct value_range *rng =
				ADDR_OF(&net_registry.ranges) + net_idx;
			uint32_t *vls = ADDR_OF(&rng->values);
			for (uint32_t idx = 0; idx < rng->count; ++idx) {
				if (value_registry_collect(
					    registry, vls[idx]
				    )) {
					return -1;
				}
			}
		}
	}

	radix_free(&rdx);
	value_registry_free(&net_registry);

	// FIXME: free temporary resources

	return 0;
}

////////////////////////////////////////////////////////////////////////////////
// Initialization
////////////////////////////////////////////////////////////////////////////////

static inline int
init_net6(
	struct value_registry *registry,
	action_get_net6_func get_net6,
	void **data,
	const struct filter_rule *actions,
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
	memory_bfree(memory_context, net6, sizeof(struct net6_classifier));

	return -1;
}

// Allows to initialize attribute for IPv6 destination address.
int
FILTER_ATTR_COMPILER_INIT_FUNC(net6_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
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
	const struct filter_rule *rules,
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