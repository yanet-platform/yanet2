#pragma once

#include "common/exp_array.h"
#include "common/hash_index.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "common/network.h"
#include "common/registry.h"
#include "common/remap.h"
#include "common/value.h"

#include "declare.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/rule.h"
#include "partition.h"

#include <stdint.h>
#include <string.h>

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
 * The builder runs in two layers. The networks repeat across the
 * rules, so the first layer groups the unique normalized networks with
 * a hash index and runs the region assignment once per network: the
 * network touches the combined cells of its cross, the touch enumerates
 * the combined regions, and a registry with one range per network holds
 * the region values assigned to the network. A network without a mask
 * covers the whole domain and contributes no distinction on its own, so
 * it skips the touch and its range holds every region.
 *
 * The second layer is the shared attribute driver itself: it groups the
 * rules by their network list - hashed and compared as stored, with no
 * sorting or normalization, canonicalizing the lists is a preparation
 * stage outside the library - and the rule iteration resolves the
 * networks of the list through the net index and replays the values
 * assigned to them instead of walking the crosses again.
 */

/*
 * Getter of the IPv6 networks of a module rule - the source or the
 * destination side - filled by the module and adapted to the rule
 * agnostic core through the compile macro below.
 */
typedef void (*classify_net6_get_net6s_func)(
	const struct classifier_rule *rule, struct filter_net6s *net6s
);

/*
 * Compile scratch of the IPv6 network attribute.
 *
 * The rule field selector of the compiled side - source or destination
 * networks - is stored in the scratch, so one body serves both sides;
 * the grouping callbacks bake their side in and need no scratch.
 */
struct classify_compile_net6 {
	classify_net6_get_net6s_func get_net6s;
	const char *name;

	struct classify_partition part_hi;
	struct classify_partition part_lo;

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

	// The dense high by low join table of the combined regions; it
	// moves into the embedded attribute at commit.
	struct value_table comb;
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
	// The halves read through the byte aligned view: the mask array
	// carries no integer alignment of its own.
	const net6_half *mask = (const net6_half *)net->mask;
	return mask[0] == 0 && mask[1] == 0;
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
	struct classify_compile_net6 *net6_compile;
	const struct net6 *net;
};

static inline int
filter_net6_group_eq(uint32_t value, const void *data) {
	const struct filter_net6_group_ctx *group_ctx = data;
	const struct net6 *group_net = group_ctx->net6_compile->nets + value;

	return memcmp(group_net->addr, group_ctx->net->addr, NET6_LEN) != 0 ||
	       memcmp(group_net->mask, group_ctx->net->mask, NET6_LEN) != 0;
}

static inline int
filter_net6_nets_append(
	struct memory_context *memory_context,
	struct classify_compile_net6 *net6_compile,
	const struct net6 *net
) {
	uint64_t count = net6_compile->net_count;
	if (mem_array_expand_exp(
		    memory_context,
		    (void **)&net6_compile->nets,
		    sizeof(*net),
		    &count
	    )) {
		return -1;
	}

	net6_compile->nets[count - 1] = *net;
	net6_compile->net_count = count;

	return 0;
}

static inline uint64_t
filter_net6_part_len(const uint8_t *part_mask) {
	return __builtin_popcountll(*(net6_half *)part_mask);
}

static inline int
filter_net6_build_part(
	struct memory_context *memory_context,
	struct net6 *nets,
	uint32_t net_count,
	net6_get_part_func get_part,
	struct classify_partition *partition
) {
	struct classify_prefixes prefixes = {0};

	for (uint32_t net_idx = 0; net_idx < net_count; ++net_idx) {
		uint8_t *addr;
		uint8_t *mask;
		get_part(nets + net_idx, &addr, &mask);

		if (classify_prefixes_add(
			    memory_context,
			    &prefixes,
			    classify_key_load(addr, 8),
			    filter_net6_part_len(mask)
		    )) {
			classify_prefixes_free(memory_context, &prefixes);
			return -1;
		}
	}

	int rc = classify_partition_build(
		memory_context, 64, &prefixes, partition
	);
	classify_prefixes_free(memory_context, &prefixes);

	return rc;
}

/*
 * Resolves the half region index range of a network half: the indexes of
 * every region of the half the network interval covers.
 */
static inline void
filter_net6_part_bounds(
	const struct classify_partition *partition,
	const uint8_t *part_addr,
	const uint8_t *part_mask,
	uint32_t *start,
	uint32_t *stop
) {
	uint64_t from = classify_key_load(part_addr, 8);
	classify_partition_bounds(
		partition,
		from,
		classify_prefix_to(64, from, filter_net6_part_len(part_mask)),
		start,
		stop
	);
}

// Resolves both half region index ranges of a normalized network at
// once.
static inline void
filter_net6_net_bounds(
	struct classify_compile_net6 *net6_compile,
	const struct net6 *net,
	uint32_t bounds[4]
) {
	filter_net6_part_bounds(
		&net6_compile->part_hi,
		net->addr,
		net->mask,
		&bounds[0],
		&bounds[1]
	);
	filter_net6_part_bounds(
		&net6_compile->part_lo,
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
	struct classify_compile_net6 *net6_compile,
	const struct net6 *net,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	uint32_t bounds[4];
	filter_net6_net_bounds(net6_compile, net, bounds);

	const uint32_t *values_hi = net6_compile->part_hi.values;
	const uint32_t *values_lo = net6_compile->part_lo.values;
	struct value_table *comb = &net6_compile->comb;
	const uint8_t *row_split = net6_compile->row_split;
	struct vline *uniform = &net6_compile->uniform;

	const int full_lo =
		bounds[2] == 0 && bounds[3] == net6_compile->part_lo.count;

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
	struct classify_compile_net6 *net6_compile,
	const struct net6 *net,
	const struct value_range **range
) {
	struct filter_net6_group_ctx group_ctx = {
		net6_compile,
		net,
	};
	uint32_t group = hash_index_lookup(
		&net6_compile->net_index,
		net6_hash(net),
		filter_net6_group_eq,
		&group_ctx
	);
	if (group == HASH_INDEX_INVALID) {
		return -1;
	}

	const struct value_range *ranges =
		ADDR_OF(&net6_compile->net_registry.ranges);
	*range = ranges + group;
	return 0;
}

static inline struct classify_compile_net6 *
classify_compile_net6_create(
	struct memory_context *memory_context,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_net6_get_net6s_func get_net6s,
	const char *name
) {
	struct classify_compile_net6 *compile =
		(struct classify_compile_net6 *)memory_balloc(
			memory_context, sizeof(struct classify_compile_net6)
		);
	if (compile == NULL) {
		return NULL;
	}
	memset(compile, 0, sizeof(struct classify_compile_net6));

	compile->get_net6s = get_net6s;
	compile->name = name;

	/*
	 * Collect the unique normalized networks of the ruleset: the first
	 * occurrence extends the network array, the hash index holds the
	 * probe table over the network indexes.
	 */
	uint32_t net_total = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct classifier_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		struct filter_net6s net6s;
		compile->get_net6s(rule, &net6s);
		net_total += net6s.count;
	}

	if (hash_index_init(&compile->net_index, memory_context, net_total)) {
		goto error_free;
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct classifier_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		struct filter_net6s net6s;
		compile->get_net6s(rule, &net6s);

		for (uint32_t net_idx = 0; net_idx < net6s.count; ++net_idx) {
			struct net6 net6;
			net6_normalize(net6s.items + net_idx, &net6);

			struct filter_net6_group_ctx group_ctx = {
				compile,
				&net6,
			};
			uint32_t hash = net6_hash(&net6);
			uint32_t group = hash_index_lookup(
				&compile->net_index,
				hash,
				filter_net6_group_eq,
				&group_ctx
			);
			if (group == HASH_INDEX_INVALID) {
				if (hash_index_insert(
					    &compile->net_index,
					    hash,
					    compile->net_count
				    )) {
					goto error_free_nets;
				}
				if (filter_net6_nets_append(
					    memory_context, compile, &net6
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
		    compile->nets,
		    compile->net_count,
		    net6_get_hi_part,
		    &compile->part_hi
	    )) {
		goto error_free_nets;
	}

	if (filter_net6_build_part(
		    memory_context,
		    compile->nets,
		    compile->net_count,
		    net6_get_lo_part,
		    &compile->part_lo
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
	char comb_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(comb_name, sizeof(comb_name), name, "comb");
	if (value_table_init(
		    &compile->comb,
		    memory_context,
		    comb_name,
		    compile->part_hi.region_count,
		    compile->part_lo.region_count
	    )) {
		goto error_free_parts;
	}

	compile->row_count = compile->comb.v_dim;
	char rows_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(rows_name, sizeof(rows_name), name, "rows");
	if (vline_init(
		    &compile->uniform,
		    memory_context,
		    rows_name,
		    compile->row_count
	    )) {
		goto error_free_comb;
	}

	compile->row_split = memory_balloc(memory_context, compile->row_count);
	if (compile->row_split == NULL) {
		goto error_free_uniform;
	}
	memset(compile->row_split, 0, compile->row_count);

	/*
	 * A row keeps the whole row value only while no network ever
	 * covers a strict sub range of the low half within the row; the
	 * marking pass visits the high range of every such network once,
	 * before any region enumeration.
	 */
	{
		const uint32_t *values_hi = compile->part_hi.values;

		for (uint32_t net_idx = 0; net_idx < compile->net_count;
		     ++net_idx) {
			const struct net6 *net = compile->nets + net_idx;
			if (net6_is_any(net)) {
				continue;
			}

			uint32_t bounds[4];
			filter_net6_net_bounds(compile, net, bounds);
			if (bounds[2] == 0 &&
			    bounds[3] == compile->part_lo.count) {
				continue;
			}

			for (uint32_t idx_hi = bounds[0]; idx_hi < bounds[1];
			     ++idx_hi) {
				compile->row_split[values_hi[idx_hi]] = 1;
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
			    compile->comb.v_dim * compile->comb.h_dim +
				    compile->row_count
		    )) {
			goto error_free_row_split;
		}

		for (uint32_t net_idx = 0; net_idx < compile->net_count;
		     ++net_idx) {
			const struct net6 *net = compile->nets + net_idx;
			if (net6_is_any(net)) {
				continue;
			}

			remap_table_new_gen(&remap_table);

			if (filter_net6_net_regions_iter(
				    compile,
				    net,
				    classify_attr_touch,
				    &remap_table
			    )) {
				remap_table_free(&remap_table);
				goto error_free_row_split;
			}
		}

		remap_table_compact(&remap_table);
		value_table_compact(&compile->comb, &remap_table);
		// The line values join the cells in one region space, so the
		// compaction must renumber them through the same table: a raw
		// line key left beside the compacted cell ids would collide
		// with an unrelated cell region.
		for (uint32_t row = 0; row < compile->row_count; ++row) {
			uint32_t *value = vline_get_ptr(&compile->uniform, row);
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
	char nets_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(nets_name, sizeof(nets_name), name, "nets");
	if (value_registry_init(
		    &compile->net_registry, memory_context, nets_name
	    )) {
		goto error_free_row_split;
	}

	{
		struct filter_net6_collect_ctx collect_ctx = {
			&compile->net_registry,
			0,
		};

		for (uint32_t net_idx = 0; net_idx < compile->net_count;
		     ++net_idx) {
			if (value_registry_start(&compile->net_registry)) {
				goto error_free_net_registry;
			}

			const struct net6 *net = compile->nets + net_idx;
			if (net6_is_any(net)) {
				continue;
			}

			if (filter_net6_net_regions_iter(
				    compile,
				    net,
				    filter_net6_collect,
				    &collect_ctx
			    )) {
				goto error_free_net_registry;
			}
		}

		compile->region_count = collect_ctx.max_value + 1;
	}

	{
		struct value_range *ranges =
			ADDR_OF(&compile->net_registry.ranges);
		struct memory_context *registry_context =
			ADDR_OF(&compile->net_registry.memory_context);

		for (uint32_t net_idx = 0; net_idx < compile->net_count;
		     ++net_idx) {
			if (!net6_is_any(compile->nets + net_idx)) {
				continue;
			}

			for (uint32_t region_idx = 0;
			     region_idx < compile->region_count;
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

	compile->region_values = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * compile->region_count
	);
	if (compile->region_values == NULL) {
		goto error_free_net_registry;
	}
	memset(compile->region_values,
	       0,
	       sizeof(uint32_t) * compile->region_count);

	return compile;

error_free_net_registry:
	if (compile->region_values != NULL) {
		memory_bfree(
			memory_context,
			compile->region_values,
			sizeof(uint32_t) * compile->region_count
		);
	}
	value_registry_fini(&compile->net_registry);

error_free_row_split:
	if (compile->row_split != NULL) {
		memory_bfree(
			memory_context, compile->row_split, compile->row_count
		);
	}

error_free_uniform:
	vline_free(&compile->uniform);

error_free_comb:
	value_table_free(&compile->comb);

error_free_parts:
	classify_partition_free(memory_context, &compile->part_lo);

error_free_part_hi:
	classify_partition_free(memory_context, &compile->part_hi);

error_free_nets:
	mem_array_free_exp(
		memory_context,
		compile->nets,
		sizeof(struct net6),
		compile->net_count
	);
	hash_index_fini(&compile->net_index);

error_free:
	memory_bfree(
		memory_context, compile, sizeof(struct classify_compile_net6)
	);

	return NULL;
}

static inline uint32_t
classify_compile_net6_size(const void *compile) {
	const struct classify_compile_net6 *net6_compile = compile;

	return net6_compile->region_count;
}

static inline int
classify_compile_net6_iter(
	void *compile,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_net6 *net6_compile = compile;

	for (uint32_t idx = 0; idx < net6_compile->region_count; ++idx) {
		if (iter_cb_func(
			    net6_compile->region_values + idx, cb_func_data
		    ) < 0) {
			return -1;
		}
	}

	return 0;
}

static inline int
classify_compile_net6_rule_is_any(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_net6 *net6_compile = compile;

	struct filter_net6s nets;
	net6_compile->get_net6s(rule, &nets);

	return net6s_is_any(&nets);
}

static inline int
classify_compile_net6_rule_iter(
	void *compile,
	const struct classifier_rule *rule,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_net6 *net6_compile = compile;

	/*
	 * Every network of the rule resolves through the net index into
	 * its range of combined regions, so the iteration replays the
	 * values assigned to the networks of the list instead of walking
	 * the crosses again. A list without networks covers the whole
	 * domain and takes every region directly.
	 */
	struct filter_net6s nets;
	net6_compile->get_net6s(rule, &nets);

	if (nets.count == 0) {
		for (uint32_t idx = 0; idx < net6_compile->region_count;
		     ++idx) {
			if (iter_cb_func(
				    net6_compile->region_values + idx,
				    cb_func_data
			    ) < 0) {
				return -1;
			}
		}
		return 0;
	}

	for (uint32_t net_idx = 0; net_idx < nets.count; ++net_idx) {
		struct net6 net6;
		net6_normalize(nets.items + net_idx, &net6);

		const struct value_range *range;
		if (filter_net6_net_range(net6_compile, &net6, &range)) {
			return -1;
		}

		const uint32_t *values = ADDR_OF(&range->values);
		for (uint32_t idx = 0; idx < range->count; ++idx) {
			if (iter_cb_func(
				    net6_compile->region_values + values[idx],
				    cb_func_data
			    ) < 0) {
				return -1;
			}
		}
	}

	return 0;
}

static inline void
classify_compile_net6_free(
	struct memory_context *memory_context, void *compile
) {
	struct classify_compile_net6 *net6_compile = compile;

	classify_partition_free(memory_context, &net6_compile->part_hi);
	classify_partition_free(memory_context, &net6_compile->part_lo);

	if (net6_compile->region_values != NULL) {
		memory_bfree(
			memory_context,
			net6_compile->region_values,
			sizeof(uint32_t) * net6_compile->region_count
		);
	}

	value_registry_fini(&net6_compile->net_registry);

	mem_array_free_exp(
		memory_context,
		net6_compile->nets,
		sizeof(struct net6),
		net6_compile->net_count
	);
	hash_index_fini(&net6_compile->net_index);

	if (net6_compile->row_split != NULL) {
		memory_bfree(
			memory_context,
			net6_compile->row_split,
			net6_compile->row_count
		);
	}

	// The uniform line is compile side scratch: it is released here in
	// every path, including the commit one.
	vline_free(&net6_compile->uniform);

	// The join table is detached by a successful commit, so this
	// releases it only while it still belongs to the scratch.
	value_table_free(&net6_compile->comb);

	memory_bfree(
		memory_context,
		net6_compile,
		sizeof(struct classify_compile_net6)
	);
}

static inline int
classify_compile_net6_commit(
	struct memory_context *memory_context, void *compile, void *attr
) {
	struct classify_compile_net6 *net6_compile = compile;
	struct classify_attr_net6 *net6_attr = attr;

	// The attribute starts empty, so the common unwind frees only the
	// members a failed step already initialised.
	memset(net6_attr, 0, sizeof(*net6_attr));

	/*
	 * The half matches are built directly in the embedded attribute:
	 * their pages are addressed through relative pointers, so trees
	 * built elsewhere cannot move in.
	 *
	 * The high half trie stays empty until its values are resolved
	 * below.
	 */
	char hi_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(hi_name, sizeof(hi_name), net6_compile->name, "hi");
	if (lpm_init(&net6_attr->hi, memory_context, hi_name)) {
		goto error;
	}
	char lo_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(lo_name, sizeof(lo_name), net6_compile->name, "lo");
	if (lpm_init(&net6_attr->lo, memory_context, lo_name)) {
		goto error;
	}
	if (classify_partition_build_lpm(
		    &net6_compile->part_lo,
		    net6_compile->part_lo.values,
		    &net6_attr->lo
	    )) {
		goto error;
	}

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
	struct value_table *comb = &net6_compile->comb;

	// The high half trie build walks the partition by region ordinal,
	// so the resolved values are indexed by ordinal and reach the row
	// of every ordinal through the region values of the partition.
	const uint32_t *hi_values = net6_compile->part_hi.values;
	uint32_t hi_count = net6_compile->part_hi.count;

	uint32_t *dense_of_row = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * net6_compile->row_count
	);
	uint32_t *lpm_values = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * (hi_count ? hi_count : 1)
	);
	if (dense_of_row == NULL || lpm_values == NULL) {
		if (dense_of_row != NULL) {
			memory_bfree(
				memory_context,
				dense_of_row,
				sizeof(uint32_t) * net6_compile->row_count
			);
		}
		if (lpm_values != NULL) {
			memory_bfree(
				memory_context,
				lpm_values,
				sizeof(uint32_t) * (hi_count ? hi_count : 1)
			);
		}
		goto error;
	}

	// A region value repeats across the ordinals of the deduplicated
	// partition, so a dense row is assigned per row and every ordinal
	// of the row shares the assigned one.
	memset(dense_of_row, 0xff, sizeof(uint32_t) * net6_compile->row_count);

	uint32_t dense_count = 0;
	for (uint32_t idx = 0; idx < hi_count; ++idx) {
		uint32_t row = hi_values[idx];
		if (net6_compile->row_split[row]) {
			if (dense_of_row[row] == 0xffffffffu) {
				dense_of_row[row] = dense_count;
				++dense_count;
			}
			lpm_values[idx] =
				FILTER_NET6_ROW_MARK | dense_of_row[row];
			continue;
		}

		lpm_values[idx] = net6_compile->region_values[vline_get(
			&net6_compile->uniform, row
		)];
	}

	if (dense_count != 0) {
		struct value_table dense;
		char dense_name[MEMORY_CONTEXT_NAME_SIZE];
		classify_leaf_name(
			dense_name,
			sizeof(dense_name),
			net6_compile->name,
			"comb"
		);
		if (value_table_init(
			    &dense,
			    memory_context,
			    dense_name,
			    dense_count,
			    comb->h_dim
		    )) {
			memory_bfree(
				memory_context,
				dense_of_row,
				sizeof(uint32_t) * net6_compile->row_count
			);
			memory_bfree(
				memory_context,
				lpm_values,
				sizeof(uint32_t) * (hi_count ? hi_count : 1)
			);
			goto error;
		}

		for (uint32_t row = 0; row < net6_compile->row_count; ++row) {
			if (!net6_compile->row_split[row]) {
				continue;
			}

			uint32_t dense_row = dense_of_row[row];
			for (uint32_t h_idx = 0; h_idx < comb->h_dim; ++h_idx) {
				*value_table_get_ptr(&dense, dense_row, h_idx) =
					net6_compile->region_values
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

	int hi_rc = classify_partition_build_lpm(
		&net6_compile->part_hi, lpm_values, &net6_attr->hi
	);

	memory_bfree(
		memory_context,
		dense_of_row,
		sizeof(uint32_t) * net6_compile->row_count
	);
	memory_bfree(
		memory_context,
		lpm_values,
		sizeof(uint32_t) * (hi_count ? hi_count : 1)
	);

	if (hi_rc) {
		goto error;
	}

	/*
	 * The join table moves into the embedded attribute field by field:
	 * it carries relative pointers that a struct copy would strand.
	 */
	net6_attr->comb.v_dim = comb->v_dim;
	net6_attr->comb.h_dim = comb->h_dim;
	SET_OFFSET_OF(&net6_attr->comb.values, ADDR_OF(&comb->values));
	SET_OFFSET_OF(
		&net6_attr->comb.memory_context, ADDR_OF(&comb->memory_context)
	);

	// The table internals now belong to the attribute: the scratch
	// copy is detached, so its release below leaves them alive.
	memset(comb, 0, sizeof(*comb));

	classify_compile_net6_free(memory_context, compile);

	return 0;

error:
	lpm_free(&net6_attr->hi);
	lpm_free(&net6_attr->lo);
	value_table_free(&net6_attr->comb);
	memset(net6_attr, 0, sizeof(*net6_attr));
	return -1;
}

static inline uint32_t
classify_compile_net6_hash(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_net6 *net6_compile = compile;

	struct filter_net6s nets;
	net6_compile->get_net6s(rule, &nets);

	uint32_t hash = nets.count;
	for (uint32_t idx = 0; idx < nets.count; ++idx) {
		const struct net6 *net = nets.items + idx;
		for (uint32_t byte_idx = 0; byte_idx < NET6_LEN; ++byte_idx) {
			hash = hash * 31 +
			       (net->addr[byte_idx] & net->mask[byte_idx]);
		}
		for (uint32_t byte_idx = 0; byte_idx < NET6_LEN; ++byte_idx) {
			hash = hash * 31 + net->mask[byte_idx];
		}
	}
	return hash;
}

static inline int
classify_compile_net6_compare(
	const void *compile,
	const struct classifier_rule *first,
	const struct classifier_rule *second
) {
	const struct classify_compile_net6 *net6_compile = compile;

	struct filter_net6s first_nets;
	struct filter_net6s second_nets;
	net6_compile->get_net6s(first, &first_nets);
	net6_compile->get_net6s(second, &second_nets);

	if (first_nets.count != second_nets.count) {
		return 1;
	}

	for (uint32_t idx = 0; idx < first_nets.count; ++idx) {
		const struct net6 *first_net = first_nets.items + idx;
		const struct net6 *second_net = second_nets.items + idx;

		for (uint32_t byte_idx = 0; byte_idx < NET6_LEN; ++byte_idx) {
			if ((first_net->addr[byte_idx] &
			     first_net->mask[byte_idx]) !=
			    (second_net->addr[byte_idx] &
			     second_net->mask[byte_idx])) {
				return 1;
			}
			if (first_net->mask[byte_idx] !=
			    second_net->mask[byte_idx]) {
				return 1;
			}
		}
	}

	return 0;
}

/*
 * Compiles the IPv6 network classifier of a ruleset into an embedded
 * attribute.
 *
 * The name scopes the memory contexts of the compile - the attribute
 * half matches, the join table and the stage registry - each titled
 * with the name and its own artifact leaf, in the memory tree.
 *
 * On success the attribute and the stage - the registry with the rule
 * group row - are owned by the caller, released through
 * classifier_fini; on failure every partial state is freed, the
 * attribute and the stage are left zeroed.
 */
static inline int
classify_net6_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_net6_get_net6s_func get_net6s,
	struct classify_attr_net6 *attr,
	struct classifier *cls
) {
	struct classify_compile_net6 *compile = classify_compile_net6_create(
		memory_context, rules, rule_count, get_net6s, name
	);
	if (compile == NULL) {
		memset(attr, 0, sizeof(*attr));
		memset(cls, 0, sizeof(*cls));
		return -1;
	}

	const struct classify_attr_ops ops = {
		.size = classify_compile_net6_size,
		.rule_is_any = classify_compile_net6_rule_is_any,
		.hash = classify_compile_net6_hash,
		.compare = classify_compile_net6_compare,
		.iter = classify_compile_net6_iter,
		.rule_iter = classify_compile_net6_rule_iter,
		.commit = classify_compile_net6_commit,
		.free_compile = classify_compile_net6_free,
	};

	return classify_attr_compile(
		memory_context,
		name,
		&ops,
		compile,
		rules,
		rule_count,
		attr,
		cls
	);
}

/*
 * Declares the IPv6 network compile entry of one consumer side: the
 * getter selects the source or the destination networks of the module
 * rule and adapts them to the rule agnostic core, and the entry takes
 * the module rule array the consumer owns.
 *
 * The tag must carry the consumer prefix, so the generated symbols
 * never collide with the library ones.
 */
#define CLASSIFY_NET6_COMPILE(tag, get_net6s)                                  \
	static inline int classify_##tag##_compile(                            \
		struct memory_context *memory_context,                         \
		const char *name,                                              \
		const struct classifier_rule **rules,                          \
		uint32_t rule_count,                                           \
		struct classify_attr_net6 *attr,                               \
		struct classifier *cls                                         \
	) {                                                                    \
		return classify_net6_compile(                                  \
			memory_context,                                        \
			name,                                                  \
			rules,                                                 \
			rule_count,                                            \
			get_net6s,                                             \
			attr,                                                  \
			cls                                                    \
		);                                                             \
	}
