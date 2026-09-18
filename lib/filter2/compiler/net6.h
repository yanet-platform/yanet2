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
 * prefix match, and the table holds the combined region of the pair. A
 * high region row without any low half distinction carries its combined
 * region in a uniform value line over the rows instead - the query
 * skips the low half lookup for it and the enumeration below touches
 * the single line value instead of every cell of the row.
 *
 * The builder runs in three layers. The networks repeat across the
 * rules, so the first layer groups the unique normalized networks with
 * a hash index and runs the classification once per network: the
 * network touches the combined cells of its cross, the touch enumerates
 * the combined regions, and a registry with one range per network holds
 * the region values assigned to the network. A network without a mask
 * covers the whole domain and contributes no distinction on its own, so
 * it skips the touch and its range holds every region.
 *
 * The second layer groups the rules by their network list as authored -
 * the bytes are hashed and compared as stored, with no sorting or
 * normalization: canonicalizing the lists is a preparation stage
 * outside the library. Every group unions the value ranges of its
 * member networks into one range of its own, so the rule iteration
 * resolves the rule list through the hash index and replays the values
 * assigned to its networks instead of walking the crosses again.
 */

typedef void (*filter_rule_get_net6s_func)(
	const struct filter_rule *filter_rule, struct filter_net6s *net6s
);

struct filter_compile_net6s_attr {
	struct filter_compile_attr attr;
	struct filter_query_attr_net6 *query_attr;

	struct range_index ri_hi;
	struct range_index ri_lo;

	// Rows of the join table with a low half distinction; the compile
	// time map drives the region enumeration and the commit packing.
	uint8_t *row_split;
	uint32_t row_count;
	// Whole row values of the enumeration: a network covering whole
	// rows through a wildcard low half touches the single line value
	// instead of every cell of the row. The line dies at commit, the
	// resolved classes move into the high half trie values.
	struct vline uniform;

	struct hash_index net_index;
	struct net6 *nets;
	uint32_t net_count;

	struct value_registry net_registry;
	uint32_t *region_values;
	uint32_t region_count;

	struct hash_index set_index;
	// Authored list bytes of every group representative: the grouping
	// probes compare against them, so the rule iteration resolves its
	// group without the rules array.
	const uint8_t **set_data;
	uint32_t *set_len;
	uint32_t *set_reps;
	uint32_t set_count;
	uint32_t set_alloc_count;

	struct value_registry set_registry;
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

// A list without networks, or holding a network without a mask, covers
// the whole address domain.
static inline int
net6s_is_any(const struct filter_net6s *nets) {
	if (nets->count == 0) {
		return 1;
	}
	for (uint32_t idx = 0; idx < nets->count; ++idx) {
		if (net6_is_any(nets->items + idx)) {
			return 1;
		}
	}
	return 0;
}

static inline void
net6s_get_set(
	filter_rule_get_net6s_func get_net6s,
	const struct filter_rule *rule,
	const uint8_t **data,
	uint32_t *len
) {
	struct filter_net6s net6s;
	get_net6s(rule, &net6s);
	*data = (const uint8_t *)net6s.items;
	*len = net6s.count * sizeof(*net6s.items);
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

static inline uint32_t
filter_net6_set_hash(const uint8_t *data, uint32_t len) {
	uint32_t hash = 2166136261u;
	for (uint32_t idx = 0; idx < len; ++idx) {
		hash = (hash ^ data[idx]) * 16777619u;
	}
	return hash ? hash : 1;
}

struct filter_net6_set_ctx {
	const uint8_t *data;
	uint32_t len;
	const uint8_t **set_data;
	const uint32_t *set_len;
};

static inline int
filter_net6_set_eq(uint32_t value, const void *data) {
	const struct filter_net6_set_ctx *set_ctx = data;

	return set_ctx->set_len[value] != set_ctx->len ||
	       memcmp(set_ctx->set_data[value], set_ctx->data, set_ctx->len) !=
		       0;
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

// Resolves both half region index ranges of a normalized network at
// once.
static inline void
filter_net6_net_bounds(
	struct filter_compile_net6s_attr *attr,
	const struct net6 *net,
	uint32_t bounds[4]
) {
	filter_net6_part_bounds(
		&attr->ri_hi, net->addr, net->mask, &bounds[0], &bounds[1]
	);
	filter_net6_part_bounds(
		&attr->ri_lo,
		net->addr + 8,
		net->mask + 8,
		&bounds[2],
		&bounds[3]
	);
}

/*
 * Iterates the combined regions of a network: the cross of its high
 * region range with its low region range.
 *
 * A high region row without any low half distinction carries its value
 * in the uniform value line instead of the join table cells, so a
 * network covering whole rows through a wildcard low half iterates the
 * single line value of every uniform row of its range.
 */
static inline int
filter_net6_net_regions_iter(
	struct filter_compile_net6s_attr *attr,
	const struct net6 *net,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	uint32_t bounds[4];
	filter_net6_net_bounds(attr, net, bounds);

	const uint32_t *values_hi = ADDR_OF(&attr->ri_hi.values);
	const uint32_t *values_lo = ADDR_OF(&attr->ri_lo.values);
	struct value_table *comb = &attr->query_attr->comb;
	const uint8_t *row_split = attr->row_split;
	struct vline *uniform = &attr->uniform;

	const int full_lo = bounds[2] == 0 && bounds[3] == attr->ri_lo.count;

	for (uint32_t idx_hi = bounds[0]; idx_hi < bounds[1]; ++idx_hi) {
		const uint32_t row = values_hi[idx_hi];

		if (full_lo && !row_split[row]) {
			if (iter_cb_func(
				    vline_get_ptr(uniform, row), cb_func_data
			    ) < 0) {
				return -1;
			}
			continue;
		}

		for (uint32_t idx_lo = bounds[2]; idx_lo < bounds[3];
		     ++idx_lo) {
			if (iter_cb_func(
				    value_table_get_ptr(
					    comb, row, values_lo[idx_lo]
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

// Resolves the network of a rule into its registry range of assigned
// region values through the network hash index.
static inline int
filter_net6_net_range(
	struct filter_compile_net6s_attr *attr,
	const struct net6 *net,
	const struct value_range **range
) {
	struct filter_net6_group_ctx group_ctx = {
		attr,
		net,
	};
	uint32_t group = hash_index_lookup(
		&attr->net_index,
		net6_hash(net),
		filter_net6_group_eq,
		&group_ctx
	);
	if (group == HASH_INDEX_INVALID) {
		return -1;
	}

	const struct value_range *ranges = ADDR_OF(&attr->net_registry.ranges);
	*range = ranges + group;
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

	attr->query_attr = (struct filter_query_attr_net6 *)memory_balloc(
		memory_context, sizeof(struct filter_query_attr_net6)
	);
	if (attr->query_attr == NULL) {
		goto error_free;
	}
	memset(attr->query_attr, 0, sizeof(struct filter_query_attr_net6));

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
		goto error_free_query;
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
	if (filter_net6_build_part(
		    memory_context,
		    attr->nets,
		    attr->net_count,
		    net6_get_hi_part,
		    &attr->query_attr->hi,
		    &attr->ri_hi
	    )) {
		goto error_free_nets;
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
	 * region values into the combined regions. A high region row
	 * without any low half distinction carries its value in the
	 * uniform value line instead, so the line joins the same
	 * enumeration below as one more value of the domain.
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

	attr->row_count = attr->query_attr->comb.v_dim;
	if (vline_init(
		    &attr->uniform,
		    memory_context,
		    "filter:net6:rows",
		    attr->row_count
	    )) {
		goto error_free_comb;
	}

	attr->row_split = memory_balloc(memory_context, attr->row_count);
	if (attr->row_split == NULL) {
		goto error_free_uniform;
	}
	memset(attr->row_split, 0, attr->row_count);

	/*
	 * A row keeps the whole row value only while no network ever
	 * covers a strict sub range of the low half within the row; the
	 * marking pass visits the high range of every such network once,
	 * before any region enumeration.
	 */
	{
		const uint32_t *values_hi = ADDR_OF(&attr->ri_hi.values);

		for (uint32_t net_idx = 0; net_idx < attr->net_count;
		     ++net_idx) {
			const struct net6 *net = attr->nets + net_idx;
			if (net6_is_any(net)) {
				continue;
			}

			uint32_t bounds[4];
			filter_net6_net_bounds(attr, net, bounds);
			if (bounds[2] == 0 && bounds[3] == attr->ri_lo.count) {
				continue;
			}

			for (uint32_t idx_hi = bounds[0]; idx_hi < bounds[1];
			     ++idx_hi) {
				attr->row_split[values_hi[idx_hi]] = 1;
			}
		}
	}

	/*
	 * Enumerate the combined regions once per unique network: the
	 * network touches the values of its cross, so every covered value
	 * gains a class specific to the set of networks covering it;
	 * compaction removes the gaps of unused values.
	 */
	{
		struct remap_table remap_table;
		if (remap_table_init(
			    &remap_table,
			    memory_context,
			    attr->query_attr->comb.v_dim *
					    attr->query_attr->comb.h_dim +
				    attr->row_count
		    )) {
			goto error_free_row_split;
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
				goto error_free_row_split;
			}
		}

		remap_table_compact(&remap_table);
		value_table_compact(&attr->query_attr->comb, &remap_table);
		// The line values join the cells in one region space, so the
		// compaction must renumber them through the same table: a raw
		// line key left beside the compacted cell ids would collide
		// with an unrelated cell region.
		for (uint32_t row = 0; row < attr->row_count; ++row) {
			uint32_t *value = vline_get_ptr(&attr->uniform, row);
			*value = remap_table_compacted(&remap_table, *value);
		}
		remap_table_free(&remap_table);
	}

	/*
	 * Assign the combined region values of every network into a
	 * registry, one range per network. A network without a mask covers
	 * the whole domain, so its range holds every region - the region
	 * count is known only after the masked networks are collected, and
	 * their ranges are filled first.
	 */
	if (value_registry_init(
		    &attr->net_registry, memory_context, "filter:net6:nets"
	    )) {
		goto error_free_row_split;
	}

	{
		struct filter_net6_collect_ctx collect_ctx = {
			&attr->net_registry,
			0,
		};

		for (uint32_t net_idx = 0; net_idx < attr->net_count;
		     ++net_idx) {
			if (value_registry_start(&attr->net_registry)) {
				goto error_free_net_registry;
			}

			const struct net6 *net = attr->nets + net_idx;
			if (net6_is_any(net)) {
				continue;
			}

			if (filter_net6_net_regions_iter(
				    attr, net, filter_net6_collect, &collect_ctx
			    )) {
				goto error_free_net_registry;
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
					goto error_free_net_registry;
				}
			}
		}
	}

	attr->region_values = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * attr->region_count
	);
	if (attr->region_values == NULL) {
		goto error_free_net_registry;
	}
	memset(attr->region_values, 0, sizeof(uint32_t) * attr->region_count);

	/*
	 * Group the rules by their network list as authored: the hash index
	 * holds the probe table over the group indexes, the representative
	 * array holds the first rule of every group.
	 */
	uint32_t rule_present = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		if (rules[rule_idx] != NULL) {
			++rule_present;
		}
	}

	if (hash_index_init(&attr->set_index, memory_context, rule_present)) {
		goto error_free_region;
	}

	attr->set_alloc_count = rule_present ? rule_present : 1;
	attr->set_reps = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * attr->set_alloc_count
	);
	attr->set_data = (const uint8_t **)memory_balloc(
		memory_context, sizeof(const uint8_t *) * attr->set_alloc_count
	);
	attr->set_len = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * attr->set_alloc_count
	);
	if (attr->set_reps == NULL || attr->set_data == NULL ||
	    attr->set_len == NULL) {
		goto error_free_sets;
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		const uint8_t *data;
		uint32_t len;
		net6s_get_set(net6s_handlers->get_net6s, rule, &data, &len);

		struct filter_net6_set_ctx set_ctx = {
			data,
			len,
			attr->set_data,
			attr->set_len,
		};
		uint32_t hash = filter_net6_set_hash(data, len);
		uint32_t set = hash_index_lookup(
			&attr->set_index, hash, filter_net6_set_eq, &set_ctx
		);
		if (set == HASH_INDEX_INVALID) {
			if (hash_index_insert(
				    &attr->set_index, hash, attr->set_count
			    )) {
				goto error_free_sets;
			}
			attr->set_reps[attr->set_count] = rule_idx;
			attr->set_data[attr->set_count] = data;
			attr->set_len[attr->set_count] = len;
			++attr->set_count;
		}
	}

	/*
	 * Union the value ranges of the member networks of every group into
	 * a registry with one range per group: a group without networks
	 * covers the whole domain and takes every region directly, a group
	 * with a network without a mask reaches every region through the
	 * range of that network.
	 */
	if (value_registry_init(
		    &attr->set_registry, memory_context, "filter:net6:groups"
	    )) {
		goto error_free_sets;
	}

	for (uint32_t set_idx = 0; set_idx < attr->set_count; ++set_idx) {
		if (value_registry_start(&attr->set_registry)) {
			goto error_free_set_registry;
		}

		struct filter_net6s nets;
		net6s_handlers->get_net6s(
			rules[attr->set_reps[set_idx]], &nets
		);

		if (nets.count == 0) {
			for (uint32_t region_idx = 0;
			     region_idx < attr->region_count;
			     ++region_idx) {
				if (value_registry_collect(
					    &attr->set_registry, region_idx
				    )) {
					goto error_free_set_registry;
				}
			}
			continue;
		}

		for (uint32_t net_idx = 0; net_idx < nets.count; ++net_idx) {
			struct net6 net6;
			net6_normalize(nets.items + net_idx, &net6);

			const struct value_range *range;
			if (filter_net6_net_range(attr, &net6, &range)) {
				goto error_free_set_registry;
			}

			const uint32_t *values = ADDR_OF(&range->values);
			for (uint32_t idx = 0; idx < range->count; ++idx) {
				if (value_registry_collect(
					    &attr->set_registry, values[idx]
				    )) {
					goto error_free_set_registry;
				}
			}
		}
	}

	return &attr->attr;

error_free_set_registry:
	value_registry_fini(&attr->set_registry);

error_free_sets:
	if (attr->set_reps != NULL) {
		memory_bfree(
			memory_context,
			attr->set_reps,
			sizeof(uint32_t) * attr->set_alloc_count
		);
	}
	if (attr->set_data != NULL) {
		memory_bfree(
			memory_context,
			attr->set_data,
			sizeof(const uint8_t *) * attr->set_alloc_count
		);
	}
	if (attr->set_len != NULL) {
		memory_bfree(
			memory_context,
			attr->set_len,
			sizeof(uint32_t) * attr->set_alloc_count
		);
	}
	hash_index_fini(&attr->set_index);

error_free_region:
	if (attr->region_values != NULL) {
		memory_bfree(
			memory_context,
			attr->region_values,
			sizeof(uint32_t) * attr->region_count
		);
	}

error_free_net_registry:
	value_registry_fini(&attr->net_registry);

error_free_row_split:
	if (attr->row_split != NULL) {
		memory_bfree(memory_context, attr->row_split, attr->row_count);
	}

error_free_uniform:
	vline_free(&attr->uniform);

error_free_comb:
	value_table_free(&attr->query_attr->comb);

error_free_parts:
	range_index_free(&attr->ri_lo);
	lpm_free(&attr->query_attr->lo);

error_free_part_hi:
	range_index_free(&attr->ri_hi);
	lpm_free(&attr->query_attr->hi);

error_free_nets:
	mem_array_free_exp(
		memory_context, attr->nets, sizeof(struct net6), attr->net_count
	);
	hash_index_fini(&attr->net_index);

error_free_query:
	memory_bfree(
		memory_context,
		attr->query_attr,
		sizeof(struct filter_query_attr_net6)
	);

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
	(void)attr;

	struct filter_compile_attr_net6s_handlers *net6s_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_net6s_handlers,
			attr_handlers
		);

	struct filter_net6s nets;
	net6s_handlers->get_net6s(rule, &nets);

	return net6s_is_any(&nets);
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

	/*
	 * Every rule list is grouped at creation, so the authored bytes of
	 * the list resolve through the hash index into the range of values
	 * assigned to the networks of the group.
	 */
	const uint8_t *data;
	uint32_t len;
	net6s_get_set(net6s_handlers->get_net6s, rule, &data, &len);

	struct filter_net6_set_ctx set_ctx = {
		data,
		len,
		net6s_attr->set_data,
		net6s_attr->set_len,
	};
	uint32_t set = hash_index_lookup(
		&net6s_attr->set_index,
		filter_net6_set_hash(data, len),
		filter_net6_set_eq,
		&set_ctx
	);
	if (set == HASH_INDEX_INVALID) {
		return -1;
	}

	const struct value_range *ranges =
		ADDR_OF(&net6s_attr->set_registry.ranges);
	const struct value_range *range = ranges + set;
	const uint32_t *values = ADDR_OF(&range->values);

	for (uint32_t idx = 0; idx < range->count; ++idx) {
		if (iter_cb_func(
			    net6s_attr->region_values + values[idx],
			    cb_func_data
		    ) < 0) {
			return -1;
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

	value_registry_fini(&net6s_attr->set_registry);

	if (net6s_attr->set_alloc_count != 0) {
		memory_bfree(
			memory_context,
			net6s_attr->set_reps,
			sizeof(uint32_t) * net6s_attr->set_alloc_count
		);
		memory_bfree(
			memory_context,
			net6s_attr->set_data,
			sizeof(const uint8_t *) * net6s_attr->set_alloc_count
		);
		memory_bfree(
			memory_context,
			net6s_attr->set_len,
			sizeof(uint32_t) * net6s_attr->set_alloc_count
		);
	}
	hash_index_fini(&net6s_attr->set_index);

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

	if (net6s_attr->row_split != NULL) {
		memory_bfree(
			memory_context,
			net6s_attr->row_split,
			net6s_attr->row_count
		);
	}

	if (net6s_attr->query_attr != NULL) {
		lpm_free(&net6s_attr->query_attr->hi);
		lpm_free(&net6s_attr->query_attr->lo);
		value_table_free(&net6s_attr->query_attr->comb);
		vline_free(&net6s_attr->uniform);

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

// Rebuild helper of the commit below: the high half trie value walk
// over the region boundaries with the values resolved per region.
struct net6_lpm_revalue_ctx {
	struct lpm *lpm;
	const uint32_t *values;
	uint8_t prev_from[LPM_KEY_SIZE_MAX];
	uint32_t prev_value;
};

static inline int
net6_lpm_revalue_cb(
	uint8_t key_size, const uint8_t *from, uint32_t index, void *data
) {
	struct net6_lpm_revalue_ctx *ctx = data;

	if (ctx->prev_value != LPM_VALUE_INVALID) {
		uint8_t to[key_size];
		memcpy(to, from, key_size);
		filter_key_dec(key_size, to);
		if (lpm_insert(
			    ctx->lpm,
			    key_size,
			    ctx->prev_from,
			    to,
			    ctx->prev_value
		    )) {
			return -1;
		}
	}

	memcpy(ctx->prev_from, from, key_size);
	ctx->prev_value = ctx->values[index];
	return 0;
}

// Overwrites the high half trie values in place: every region interval
// receives its resolved value, a uniform region its final class, a
// split region its marked dense row.
static inline int
net6_lpm_revalue(
	struct lpm *lpm, const struct range_index *ri, const uint32_t *values
) {
	struct net6_lpm_revalue_ctx ctx;
	ctx.lpm = lpm;
	ctx.values = values;
	ctx.prev_value = LPM_VALUE_INVALID;

	if (radix_walk(&ri->radix, 8, net6_lpm_revalue_cb, &ctx)) {
		return -1;
	}

	if (ctx.prev_value != LPM_VALUE_INVALID) {
		uint8_t to[8];
		memset(to, 0xff, 8);
		if (lpm_insert(lpm, 8, ctx.prev_from, to, ctx.prev_value)) {
			return -1;
		}
	}

	return 0;
}

static inline struct filter_query_attr *
filter_compile_attr_net6s_commit(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_net6s_attr *net6s_attr =
		container_of(attr, struct filter_compile_net6s_attr, attr);

	/*
	 * The compile stages assign the final class values to the combined
	 * regions; the cells and the whole row values still hold the
	 * region identifiers. A row without any low half distinction
	 * resolves into its final class carried by the high half trie
	 * value itself, so the lookup finishes after the high half walk;
	 * the rows of a low half distinction pack densely into a rebuilt
	 * join table referenced from the marked trie values, and the cells
	 * of the uniform rows are never read again.
	 */
	struct filter_query_attr_net6 *query_attr = net6s_attr->query_attr;
	struct value_table *comb = &query_attr->comb;

	// The trie walk below delivers region ordinals, so the resolved
	// values are indexed by ordinal and reach the row of every ordinal
	// through the region values of the partition.
	const uint32_t *hi_values = ADDR_OF(&net6s_attr->ri_hi.values);
	uint32_t hi_count = net6s_attr->ri_hi.count;

	uint32_t *dense_of_row = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * net6s_attr->row_count
	);
	uint32_t *lpm_values = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * (hi_count ? hi_count : 1)
	);
	if (dense_of_row == NULL || lpm_values == NULL) {
		if (dense_of_row != NULL) {
			memory_bfree(
				memory_context,
				dense_of_row,
				sizeof(uint32_t) * net6s_attr->row_count
			);
		}
		return NULL;
	}

	// A region value repeats across the ordinals of the deduplicated
	// partition, so a dense row is assigned per row and every ordinal
	// of the row shares the assigned one.
	memset(dense_of_row, 0xff, sizeof(uint32_t) * net6s_attr->row_count);

	uint32_t dense_count = 0;
	for (uint32_t idx = 0; idx < hi_count; ++idx) {
		uint32_t row = hi_values[idx];
		if (net6s_attr->row_split[row]) {
			if (dense_of_row[row] == 0xffffffffu) {
				dense_of_row[row] = dense_count;
				++dense_count;
			}
			lpm_values[idx] =
				FILTER_NET6_ROW_MARK | dense_of_row[row];
			continue;
		}

		lpm_values[idx] = net6s_attr->region_values[vline_get(
			&net6s_attr->uniform, row
		)];
	}

	if (dense_count != 0) {
		struct value_table dense;
		if (value_table_init(
			    &dense,
			    memory_context,
			    "filter:net6",
			    dense_count,
			    comb->h_dim
		    )) {
			memory_bfree(
				memory_context,
				dense_of_row,
				sizeof(uint32_t) * net6s_attr->row_count
			);
			memory_bfree(
				memory_context,
				lpm_values,
				sizeof(uint32_t) * (hi_count ? hi_count : 1)
			);
			return NULL;
		}

		for (uint32_t row = 0; row < net6s_attr->row_count; ++row) {
			if (!net6s_attr->row_split[row]) {
				continue;
			}

			uint32_t dense_row = dense_of_row[row];
			for (uint32_t h_idx = 0; h_idx < comb->h_dim; ++h_idx) {
				*value_table_get_ptr(&dense, dense_row, h_idx) =
					net6s_attr->region_values
						[*value_table_get_ptr(
							comb, row, h_idx
						)];
			}
		}

		value_table_free(comb);
		// Field by field: the table carries relative pointers that a
		// struct copy would strand.
		comb->v_dim = dense.v_dim;
		comb->h_dim = dense.h_dim;
		SET_OFFSET_OF(&comb->values, ADDR_OF(&dense.values));
		SET_OFFSET_OF(
			&comb->memory_context, ADDR_OF(&dense.memory_context)
		);
	} else {
		value_table_free(comb);
	}

	int revalue_rc = net6_lpm_revalue(
		&query_attr->hi, &net6s_attr->ri_hi, lpm_values
	);

	memory_bfree(
		memory_context,
		dense_of_row,
		sizeof(uint32_t) * net6s_attr->row_count
	);
	memory_bfree(
		memory_context,
		lpm_values,
		sizeof(uint32_t) * (hi_count ? hi_count : 1)
	);

	if (revalue_rc) {
		return NULL;
	}

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

	const uint8_t *data;
	uint32_t len;
	net6s_get_set(net6s_handlers->get_net6s, rule, &data, &len);

	return filter_net6_set_hash(data, len);
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

	const uint8_t *first_data;
	uint32_t first_len;
	const uint8_t *second_data;
	uint32_t second_len;
	net6s_get_set(
		net6s_handlers->get_net6s, first, &first_data, &first_len
	);
	net6s_get_set(
		net6s_handlers->get_net6s, second, &second_data, &second_len
	);

	return first_len != second_len ||
	       memcmp(first_data, second_data, first_len) != 0;
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
