#pragma once

#include "../rule.h"

#include "common/exp_array.h"
#include "common/hash_index.h"
#include "common/lpm.h"
#include "common/range_collector.h"
#include "common/registry.h"
#include "common/remap.h"

#include "lib/filter2/classifiers/net6.h"

#include "declare.h"

/*
 * Region based builder for the network attribute of the 128 bit value
 * domain.
 *
 * An address splits into the high and the low 64 bit halves; every rule
 * network is a prefix, so a network selects an interval of the high half
 * and an interval of the low one. Each half is partitioned into regions
 * on its own, the high and low regions of a network cross into the
 * combined regions of the address domain, and a dense high by low value
 * table carries the join for the query side: a packet address resolves
 * to one high and one low region value through the per half longest
 * prefix match, and the table holds the combined region of the pair.
 *
 * The networks repeat across the rules, so the builder groups the unique
 * normalized networks with a hash index and runs the same classification
 * the whole filter runs on rules: every unique network touches the
 * combined cells of its cross, the touch enumerates the combined
 * regions, and a registry with one range per network holds the region
 * values associated with the network. Rule iteration resolves the rule
 * networks through the hash index and walks the prebuilt ranges instead
 * of walking the crosses again. A network without a mask covers the
 * whole domain and contributes no distinction on its own, so it skips
 * the touch and its registry range holds every region.
 */

typedef void (*filter_rule_get_net6s_func)(
	const struct filter_rule *filter_rule, struct filter_net6s *net6s
);

struct filter_compile_net6s_attr {
	struct filter_compile_attr attr;
	struct filter_query_attr_net6 *query_attr;

	struct range_index ri_hi;
	struct range_index ri_lo;

	struct hash_index net_index;
	struct net6 *nets;
	uint32_t net_count;

	struct value_registry net_registry;
	uint32_t *region_values;
	uint32_t region_count;
};

struct filter_compile_attr_net6s_handlers {
	struct filter_compile_attr_handlers attr_handlers;
	filter_rule_get_net6s_func get_net6s;
};

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

static inline void
net6_normalize(const struct net6 *src, struct net6 *dst) {
	memcpy(dst->addr, src->addr, NET6_LEN);
	memcpy(dst->mask, src->mask, NET6_LEN);
	for (uint8_t idx = 0; idx < NET6_LEN; ++idx) {
		dst->addr[idx] &= src->mask[idx];
	}
}

static inline int
net6_is_any(const struct net6 *net) {
	return *(const uint64_t *)(net->mask) == 0 &&
	       *(const uint64_t *)(net->mask + 8) == 0;
}

static inline uint32_t
net6_hash(const struct net6 *net) {
	uint32_t hash = 0;
	for (uint32_t idx = 0; idx < NET6_LEN; ++idx) {
		hash = hash * 31 + net->addr[idx];
	}
	for (uint32_t idx = 0; idx < NET6_LEN; ++idx) {
		hash = hash * 31 + net->mask[idx];
	}
	return hash;
}

struct filter_net6_group_ctx {
	struct filter_compile_net6s_attr *attr;
	const struct net6 *net;
};

static inline int
filter_net6_group_eq(uint32_t value, const void *data) {
	const struct filter_net6_group_ctx *group_ctx = data;
	const struct net6 *group_net = group_ctx->attr->nets + value;

	return memcmp(group_net->addr, group_ctx->net->addr, NET6_LEN) != 0 ||
	       memcmp(group_net->mask, group_ctx->net->mask, NET6_LEN) != 0;
}

static inline int
filter_net6_nets_append(
	struct memory_context *memory_context,
	struct filter_compile_net6s_attr *attr,
	const struct net6 *net
) {
	uint64_t count = attr->net_count;
	if (mem_array_expand_exp(
		    memory_context, (void **)&attr->nets, sizeof(*net), &count
	    )) {
		return -1;
	}

	attr->nets[count - 1] = *net;
	attr->net_count = count;

	return 0;
}

static inline int
filter_net6_build_part(
	struct memory_context *memory_context,
	struct net6 *nets,
	uint32_t net_count,
	net6_get_part_func get_part,
	struct lpm *lpm,
	struct range_index *ri
) {
	struct range_collector collector;
	if (range_collector_init(&collector, memory_context)) {
		return -1;
	}

	for (uint32_t net_idx = 0; net_idx < net_count; ++net_idx) {
		uint8_t *addr;
		uint8_t *mask;
		get_part(nets + net_idx, &addr, &mask);

		if (range8_collector_add(
			    &collector,
			    addr,
			    __builtin_popcountll(*(uint64_t *)mask)
		    )) {
			goto error;
		}
	}

	if (lpm_init(lpm, memory_context, "filter:net6")) {
		goto error;
	}

	if (range_index_init(ri, memory_context)) {
		goto error_lpm;
	}

	if (range_collector_collect(&collector, 8, ri)) {
		goto error_range_index;
	}

	if (range_index_build_lpm(ri, 8, lpm)) {
		goto error_range_index;
	}

	range_collector_free(&collector, 8);

	return 0;

error_range_index:
	range_index_free(ri);

error_lpm:
	lpm_free(lpm);

error:
	range_collector_free(&collector, 8);

	return -1;
}

/*
 * Resolves the half region index range of a network half: the indexes of
 * every region of the half the network interval covers.
 */
static inline void
filter_net6_part_bounds(
	const struct range_index *ri,
	const uint8_t *part_addr,
	const uint8_t *part_mask,
	uint32_t *start,
	uint32_t *stop
) {
	uint8_t from[8];
	uint8_t to[8];
	for (uint32_t idx = 0; idx < 8; ++idx) {
		from[idx] = part_addr[idx];
		to[idx] = part_addr[idx] | ~part_mask[idx];
	}
	filter_key_inc(8, to);

	*start = radix_lookup(&ri->radix, 8, from);
	*stop = radix_lookup(&ri->radix, 8, to);
	if (*stop == 0) {
		/*
		 * The only chance get zero here is for the last one
		 * item.
		 */
		*stop = ri->count;
	}
}

/*
 * Iterates the combined cells of a network: the cross of its high region
 * range with its low region range.
 */
static inline int
filter_net6_net_regions_iter(
	struct filter_compile_net6s_attr *attr,
	const struct net6 *net,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	uint32_t start_hi;
	uint32_t stop_hi;
	filter_net6_part_bounds(
		&attr->ri_hi, net->addr, net->mask, &start_hi, &stop_hi
	);

	uint32_t start_lo;
	uint32_t stop_lo;
	filter_net6_part_bounds(
		&attr->ri_lo, net->addr + 8, net->mask + 8, &start_lo, &stop_lo
	);

	const uint32_t *values_hi = ADDR_OF(&attr->ri_hi.values);
	const uint32_t *values_lo = ADDR_OF(&attr->ri_lo.values);
	struct value_table *comb = &attr->query_attr->comb;

	for (uint32_t idx_hi = start_hi; idx_hi < stop_hi; ++idx_hi) {
		for (uint32_t idx_lo = start_lo; idx_lo < stop_lo; ++idx_lo) {
			if (iter_cb_func(
				    value_table_get_ptr(
					    comb,
					    values_hi[idx_hi],
					    values_lo[idx_lo]
				    ),
				    cb_func_data
			    ) < 0) {
				return -1;
			}
		}
	}

	return 0;
}

struct filter_net6_collect_ctx {
	struct value_registry *registry;
	uint32_t max_value;
};

static inline int
filter_net6_collect(uint32_t *value, void *data) {
	struct filter_net6_collect_ctx *collect_ctx = data;

	if (value_registry_collect(collect_ctx->registry, *value)) {
		return -1;
	}
	if (*value > collect_ctx->max_value) {
		collect_ctx->max_value = *value;
	}

	return 0;
}

static inline struct filter_compile_attr *
filter_compile_attr_net6s_create(
	struct memory_context *memory_context,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
) {
	struct filter_compile_attr_net6s_handlers *net6s_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_net6s_handlers,
			attr_handlers
		);

	struct filter_compile_net6s_attr *attr = memory_balloc(
		memory_context, sizeof(struct filter_compile_net6s_attr)
	);
	if (attr == NULL) {
		return NULL;
	}
	memset(attr, 0, sizeof(struct filter_compile_net6s_attr));

	/*
	 * Collect the unique normalized networks of the ruleset: the first
	 * occurrence extends the network array, the hash index holds the
	 * probe table over the network indexes.
	 */
	uint32_t net_total = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		struct filter_net6s net6s;
		net6s_handlers->get_net6s(rule, &net6s);
		net_total += net6s.count;
	}

	if (hash_index_init(&attr->net_index, memory_context, net_total)) {
		goto error_free;
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		struct filter_net6s net6s;
		net6s_handlers->get_net6s(rule, &net6s);

		for (uint32_t net_idx = 0; net_idx < net6s.count; ++net_idx) {
			struct net6 net6;
			net6_normalize(net6s.items + net_idx, &net6);

			struct filter_net6_group_ctx group_ctx = {
				attr,
				&net6,
			};
			uint32_t hash = net6_hash(&net6);
			uint32_t group = hash_index_lookup(
				&attr->net_index,
				hash,
				filter_net6_group_eq,
				&group_ctx
			);
			if (group == HASH_INDEX_INVALID) {
				if (hash_index_insert(
					    &attr->net_index,
					    hash,
					    attr->net_count
				    )) {
					goto error_free_nets;
				}
				if (filter_net6_nets_append(
					    memory_context, attr, &net6
				    )) {
					goto error_free_nets;
				}
			}
		}
	}

	/*
	 * Both halves are partitioned on their own over the unique
	 * networks, and the longest prefix match of every half resolves a
	 * packet address half into its half region value.
	 */
	attr->query_attr = (struct filter_query_attr_net6 *)memory_balloc(
		memory_context, sizeof(struct filter_query_attr_net6)
	);
	if (attr->query_attr == NULL) {
		goto error_free_nets;
	}
	memset(attr->query_attr, 0, sizeof(struct filter_query_attr_net6));

	if (filter_net6_build_part(
		    memory_context,
		    attr->nets,
		    attr->net_count,
		    net6_get_hi_part,
		    &attr->query_attr->hi,
		    &attr->ri_hi
	    )) {
		goto error_free_query;
	}

	if (filter_net6_build_part(
		    memory_context,
		    attr->nets,
		    attr->net_count,
		    net6_get_lo_part,
		    &attr->query_attr->lo,
		    &attr->ri_lo
	    )) {
		goto error_free_part_hi;
	}

	/*
	 * The dense high by low value table carries the join of the half
	 * region values into the combined regions.
	 */
	if (value_table_init(
		    &attr->query_attr->comb,
		    memory_context,
		    "filter:net6",
		    attr->ri_hi.max_value + 1,
		    attr->ri_lo.max_value + 1
	    )) {
		goto error_free_parts;
	}

	/*
	 * Enumerate the combined regions: every unique network with a mask
	 * touches the cells of its cross, so every cell gains a value
	 * specific to the set of networks covering it; compaction removes
	 * the gaps of unused values.
	 */
	{
		struct remap_table remap_table;
		if (remap_table_init(
			    &remap_table,
			    memory_context,
			    attr->query_attr->comb.v_dim *
				    attr->query_attr->comb.h_dim
		    )) {
			goto error_free_comb;
		}

		for (uint32_t net_idx = 0; net_idx < attr->net_count;
		     ++net_idx) {
			const struct net6 *net = attr->nets + net_idx;
			if (net6_is_any(net)) {
				continue;
			}

			remap_table_new_gen(&remap_table);

			if (filter_net6_net_regions_iter(
				    attr,
				    net,
				    filter_compile_attr_touch,
				    &remap_table
			    )) {
				remap_table_free(&remap_table);
				goto error_free_comb;
			}
		}

		remap_table_compact(&remap_table);
		value_table_compact(&attr->query_attr->comb, &remap_table);
		remap_table_free(&remap_table);
	}

	/*
	 * Collect the combined region values of every network into a
	 * registry, one range per network. A network without a mask covers
	 * the whole domain, so its range holds every region - the region
	 * count is known only after the masked networks are collected, and
	 * the masked ranges are filled first.
	 */
	if (value_registry_init(
		    &attr->net_registry, memory_context, "filter:net6:nets"
	    )) {
		goto error_free_comb;
	}

	{
		struct filter_net6_collect_ctx collect_ctx = {
			&attr->net_registry,
			0,
		};

		for (uint32_t net_idx = 0; net_idx < attr->net_count;
		     ++net_idx) {
			if (value_registry_start(&attr->net_registry)) {
				goto error_free_registry;
			}

			const struct net6 *net = attr->nets + net_idx;
			if (net6_is_any(net)) {
				continue;
			}

			if (filter_net6_net_regions_iter(
				    attr, net, filter_net6_collect, &collect_ctx
			    )) {
				goto error_free_registry;
			}
		}

		attr->region_count = collect_ctx.max_value + 1;
	}

	{
		struct value_range *ranges =
			ADDR_OF(&attr->net_registry.ranges);
		struct memory_context *registry_context =
			ADDR_OF(&attr->net_registry.memory_context);

		for (uint32_t net_idx = 0; net_idx < attr->net_count;
		     ++net_idx) {
			if (!net6_is_any(attr->nets + net_idx)) {
				continue;
			}

			for (uint32_t region_idx = 0;
			     region_idx < attr->region_count;
			     ++region_idx) {
				if (value_range_append(
					    registry_context,
					    ranges + net_idx,
					    region_idx
				    )) {
					goto error_free_registry;
				}
			}
		}
	}

	attr->region_values = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * attr->region_count
	);
	if (attr->region_values == NULL) {
		goto error_free_registry;
	}
	memset(attr->region_values, 0, sizeof(uint32_t) * attr->region_count);

	return &attr->attr;

error_free_registry:
	value_registry_fini(&attr->net_registry);

error_free_comb:
	value_table_free(&attr->query_attr->comb);

error_free_parts:
	range_index_free(&attr->ri_lo);
	lpm_free(&attr->query_attr->lo);

error_free_part_hi:
	range_index_free(&attr->ri_hi);
	lpm_free(&attr->query_attr->hi);

error_free_query:
	memory_bfree(
		memory_context,
		attr->query_attr,
		sizeof(struct filter_query_attr_net6)
	);

error_free_nets:
	mem_array_free_exp(
		memory_context, attr->nets, sizeof(struct net6), attr->net_count
	);
	hash_index_fini(&attr->net_index);

error_free:
	memory_bfree(
		memory_context, attr, sizeof(struct filter_compile_net6s_attr)
	);

	return NULL;
}

static inline uint32_t
filter_compile_attr_net6s_size(const struct filter_compile_attr *attr) {
	struct filter_compile_net6s_attr *net6s_attr =
		container_of(attr, struct filter_compile_net6s_attr, attr);

	return net6s_attr->region_count;
}

static inline int
filter_compile_attr_net6s_iter(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	(void)attr_handlers;

	struct filter_compile_net6s_attr *net6s_attr =
		container_of(attr, struct filter_compile_net6s_attr, attr);

	for (uint32_t idx = 0; idx < net6s_attr->region_count; ++idx) {
		if (iter_cb_func(
			    net6s_attr->region_values + idx, cb_func_data
		    ) < 0) {
			return -1;
		}
	}

	return 0;
}

static inline int
filter_compile_attr_net6s_rule_is_any(
	const struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {

	struct filter_compile_attr_net6s_handlers *net6s_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_net6s_handlers,
			attr_handlers
		);

	(void)attr;

	struct filter_net6s nets;
	net6s_handlers->get_net6s(rule, &nets);

	if (nets.count == 0) {
		return 1;
	}

	struct net6 net6_normalized;
	net6_normalize(nets.items + 0, &net6_normalized);

	return *(uint64_t *)(net6_normalized.mask + 0) == 0 &&
	       *(uint64_t *)(net6_normalized.mask + 8) == 0;
}

static inline int
filter_compile_attr_net6s_rule_iter(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	struct filter_compile_attr_net6s_handlers *net6s_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_net6s_handlers,
			attr_handlers
		);

	struct filter_compile_net6s_attr *net6s_attr =
		container_of(attr, struct filter_compile_net6s_attr, attr);

	struct filter_net6s nets;
	net6s_handlers->get_net6s(rule, &nets);

	/*
	 * Every rule network is grouped at creation, so each one resolves
	 * through the hash index into its registry range of prebuilt
	 * region values.
	 */
	for (uint32_t net_idx = 0; net_idx < nets.count; ++net_idx) {
		struct net6 net6;
		net6_normalize(nets.items + net_idx, &net6);

		struct filter_net6_group_ctx group_ctx = {
			net6s_attr,
			&net6,
		};
		uint32_t group = hash_index_lookup(
			&net6s_attr->net_index,
			net6_hash(&net6),
			filter_net6_group_eq,
			&group_ctx
		);
		if (group == HASH_INDEX_INVALID) {
			return -1;
		}

		const struct value_range *ranges =
			ADDR_OF(&net6s_attr->net_registry.ranges);
		const struct value_range *range = ranges + group;
		const uint32_t *values = ADDR_OF(&range->values);

		for (uint32_t idx = 0; idx < range->count; ++idx) {
			if (iter_cb_func(
				    net6s_attr->region_values + values[idx],
				    cb_func_data
			    ) < 0) {
				return -1;
			}
		}
	}

	return 0;
}

static inline void
filter_compile_attr_net6s_free(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_net6s_attr *net6s_attr =
		container_of(attr, struct filter_compile_net6s_attr, attr);

	range_index_free(&net6s_attr->ri_hi);
	range_index_free(&net6s_attr->ri_lo);

	if (net6s_attr->region_values != NULL) {
		memory_bfree(
			memory_context,
			net6s_attr->region_values,
			sizeof(uint32_t) * net6s_attr->region_count
		);
	}

	value_registry_fini(&net6s_attr->net_registry);

	mem_array_free_exp(
		memory_context,
		net6s_attr->nets,
		sizeof(struct net6),
		net6s_attr->net_count
	);
	hash_index_fini(&net6s_attr->net_index);

	if (net6s_attr->query_attr != NULL) {
		lpm_free(&net6s_attr->query_attr->hi);
		lpm_free(&net6s_attr->query_attr->lo);
		value_table_free(&net6s_attr->query_attr->comb);

		memory_bfree(
			memory_context,
			net6s_attr->query_attr,
			sizeof(struct filter_query_attr_net6)
		);
	}

	memory_bfree(
		memory_context, attr, sizeof(struct filter_compile_net6s_attr)
	);
}

static inline struct filter_query_attr *
filter_compile_attr_net6s_commit(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	(void)memory_context;
	struct filter_compile_net6s_attr *net6s_attr =
		container_of(attr, struct filter_compile_net6s_attr, attr);

	/*
	 * The compile stages assign the final class values to the combined
	 * regions; the join table cells still hold the region identifiers
	 * and resolve through the region storage.
	 */
	struct value_table *comb = &net6s_attr->query_attr->comb;
	for (uint32_t v_idx = 0; v_idx < comb->v_dim; ++v_idx) {
		for (uint32_t h_idx = 0; h_idx < comb->h_dim; ++h_idx) {
			uint32_t *value =
				value_table_get_ptr(comb, v_idx, h_idx);
			*value = net6s_attr->region_values[*value];
		}
	}

	struct filter_query_attr_net6 *query_attr = net6s_attr->query_attr;
	net6s_attr->query_attr = NULL;

	filter_compile_attr_net6s_free(memory_context, attr);

	return &query_attr->attr;
}

static inline uint32_t
filter_compile_attr_net6_hash(
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {
	const struct filter_compile_attr_net6s_handlers *net6s_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_net6s_handlers,
			attr_handlers
		);

	struct filter_net6s nets;
	net6s_handlers->get_net6s(rule, &nets);

	uint32_t hash = nets.count;
	for (uint32_t idx = 0; idx < nets.count; ++idx) {
		struct net6 net6_normalized;
		net6_normalize(nets.items + idx, &net6_normalized);
		hash = hash * 31 + net6_hash(&net6_normalized);
	}
	return hash;
}

static inline int
filter_compile_attr_net6_compare(
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *first,
	const struct filter_rule *second
) {
	const struct filter_compile_attr_net6s_handlers *net6s_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_net6s_handlers,
			attr_handlers
		);

	struct filter_net6s first_nets;
	struct filter_net6s second_nets;
	net6s_handlers->get_net6s(first, &first_nets);
	net6s_handlers->get_net6s(second, &second_nets);

	if (first_nets.count != second_nets.count) {
		return 1;
	}

	for (uint32_t idx = 0; idx < first_nets.count; ++idx) {
		struct net6 first_net6_normalized;
		struct net6 second_net6_normalized;
		net6_normalize(first_nets.items + idx, &first_net6_normalized);
		net6_normalize(
			second_nets.items + idx, &second_net6_normalized
		);

		if (memcmp(first_net6_normalized.addr,
			   second_net6_normalized.addr,
			   sizeof(first_net6_normalized.addr)) != 0) {
			return 1;
		}
		if (memcmp(first_net6_normalized.mask,
			   second_net6_normalized.mask,
			   sizeof(first_net6_normalized.mask)) != 0) {
			return 1;
		}
	}

	return 0;
}

static const struct filter_compile_attr_handlers filter_compile_get_net6s = {
	.create = filter_compile_attr_net6s_create,
	.size = filter_compile_attr_net6s_size,
	.iter = filter_compile_attr_net6s_iter,
	.rule_iter = filter_compile_attr_net6s_rule_iter,
	.rule_is_any = filter_compile_attr_net6s_rule_is_any,
	.hash = filter_compile_attr_net6_hash,
	.compare = filter_compile_attr_net6_compare,
	.commit = filter_compile_attr_net6s_commit,
	.free_compile = filter_compile_attr_net6s_free,
	.free_query = filter_query_attr_net6_free,
};

static inline void
get_net6s_src(const struct filter_rule *rule, struct filter_net6s *net6s) {
	net6s->count = rule->net6.src_count;
	net6s->items = rule->net6.srcs;
}

static inline void
get_net6s_dst(const struct filter_rule *rule, struct filter_net6s *net6s) {
	net6s->count = rule->net6.dst_count;
	net6s->items = rule->net6.dsts;
}

static const struct filter_compile_attr_net6s_handlers
	filter_compile_attr_net6_src = {
		.attr_handlers = filter_compile_get_net6s,
		.get_net6s = get_net6s_src,
};

static const struct filter_compile_attr_net6s_handlers
	filter_compile_attr_net6_dst = {
		.attr_handlers = filter_compile_get_net6s,
		.get_net6s = get_net6s_dst,
};
