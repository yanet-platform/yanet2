#pragma once

#include "../rule.h"
#include "common/lpm.h"
#include "common/range_collector.h"
#include "common/registry.h"
#include "common/value.h"
#include "lib/filter2/classifiers/net4.h"

#include "declare.h"
#include "helper.h"

typedef void (*filter_rule_get_net4s_func)(
	const struct filter_rule *filter_rule, struct filter_net4s *net
);

struct filter_compile_net_attr {
	struct filter_compile_attr attr;
	struct filter_query_attr_net4 *query_attr;

	struct range_index range_index;
	struct vline line;
};

struct filter_compile_attr_net4_handlers {
	struct filter_compile_attr_handlers attr_handlers;
	filter_rule_get_net4s_func get_net4s;
};

static inline void
filter_compile_attr_net_free(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_net_attr *net_attr =
		container_of(attr, struct filter_compile_net_attr, attr);

	if (net_attr->query_attr != NULL) {
		lpm_free(&net_attr->query_attr->lpm);
		memory_bfree(
			memory_context,
			net_attr->query_attr,
			sizeof(struct filter_query_attr_net4)
		);
	}

	range_index_free(&net_attr->range_index);
	vline_free(&net_attr->line);

	memory_bfree(
		memory_context, attr, sizeof(struct filter_compile_net_attr)
	);
}

static inline struct filter_query_attr *
filter_compile_attr_net_commit(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_net_attr *net_attr =
		container_of(attr, struct filter_compile_net_attr, attr);

	struct filter_query_attr_net4 *query_attr = net_attr->query_attr;

	/*
	 * The region table moves into the classifier unchanged; the trie
	 * keeps raw region ids and every lookup goes through the table. The
	 * fields are moved one by one because the table carries relative
	 * pointers that a struct copy would strand.
	 */
	query_attr->line.size = net_attr->line.size;
	SET_OFFSET_OF(
		&query_attr->line.values, ADDR_OF(&net_attr->line.values)
	);
	SET_OFFSET_OF(
		&query_attr->line.memory_context,
		ADDR_OF(&net_attr->line.memory_context)
	);
	memset(&net_attr->line, 0, sizeof(net_attr->line));

	net_attr->query_attr = NULL;

	filter_compile_attr_net_free(memory_context, attr);

	return &query_attr->attr;
}

static inline void
get_net_src(const struct filter_rule *rule, struct filter_net4s *net) {
	net->count = rule->net4.src_count;
	net->items = rule->net4.srcs;
}

static inline struct filter_query_attr *
filter_compile_attr_net4_build_dedup(
	filter_rule_get_net4s_func get_net4s,
	struct value_registry *registry,
	const struct filter_rule **rules,
	uint32_t rule_count,
	struct memory_context *memory_context
);

static inline void
get_net_dst(const struct filter_rule *rule, struct filter_net4s *net) {
	net->count = rule->net4.dst_count;
	net->items = rule->net4.dsts;
}

static inline struct filter_query_attr *
filter_compile_attr_net4_src_build(
	struct value_registry *registry,
	const struct filter_rule **rules,
	uint32_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_net4_build_dedup(
		get_net_src, registry, rules, rule_count, memory_context
	);
}

static inline struct filter_query_attr *
filter_compile_attr_net4_dst_build(
	struct value_registry *registry,
	const struct filter_rule **rules,
	uint32_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_net4_build_dedup(
		get_net_dst, registry, rules, rule_count, memory_context
	);
}

static const struct filter_compile_attr_handlers
	filter_compile_net4_src_handlers = {
		.build = filter_compile_attr_net4_src_build,
		.free_query = filter_query_attr_net4_free,
};

static const struct filter_compile_attr_handlers
	filter_compile_net4_dst_handlers = {
		.build = filter_compile_attr_net4_dst_build,
		.free_query = filter_query_attr_net4_free,
};

static const struct filter_compile_attr_net4_handlers
	filter_compile_attr_net4_src = {
		.attr_handlers = filter_compile_net4_src_handlers,
		.get_net4s = get_net_src,
};

static const struct filter_compile_attr_net4_handlers
	filter_compile_attr_net4_dst = {
		.attr_handlers = filter_compile_net4_dst_handlers,
		.get_net4s = get_net_dst,
};

static inline struct filter_query_attr *
filter_compile_attr_net_commit(
	struct memory_context *memory_context, struct filter_compile_attr *attr
);

// Host side open addressing map from an 8 byte network to a dense id;
// by_id is the id keyed view every consumer indexes, since hash slots
// are not dense.
struct net4_dedup {
	uint8_t *keys;
	uint8_t *by_id;
	uint32_t *ids;
	uint32_t cap;
	uint32_t count;
};

static inline uint32_t
net4_dedup_hash(const uint8_t *key) {
	uint32_t h = 2166136261u;
	for (uint32_t idx = 0; idx < 8; ++idx) {
		h = (h ^ key[idx]) * 16777619u;
	}
	return h ? h : 1;
}

static inline uint32_t
net4_dedup_get(struct net4_dedup *d, const uint8_t *key) {
	uint32_t slot = net4_dedup_hash(key) & (d->cap - 1);
	while (d->ids[slot] != 0) {
		if (memcmp(d->keys + slot * 8, key, 8) == 0) {
			return d->ids[slot] - 1;
		}
		slot = (slot + 1) & (d->cap - 1);
	}
	memcpy(d->keys + slot * 8, key, 8);
	d->ids[slot] = ++d->count;
	memcpy(d->by_id + (d->count - 1) * 8, key, 8);
	return d->count - 1;
}

static inline uint32_t
net4_span_hash(const uint32_t *span, uint32_t len) {
	uint32_t h = 2166136261u;
	for (uint32_t idx = 0; idx < len; ++idx) {
		h = (h ^ span[idx]) * 16777619u;
	}
	return h ? h : 1;
}

/*
 * Deduplicated net4 builder: one pass over the rule instances feeds a
 * dense network index, the trie is built from the distinct networks
 * only, the region table is refined with one remap generation per
 * rule-set group walking every distinct network exactly once through
 * cached bounds, and each rule registry range is replayed from per group
 * class lists.
 */
static inline struct filter_query_attr *
filter_compile_attr_net4_build_dedup(
	filter_rule_get_net4s_func get_net4s,
	struct value_registry *registry,
	const struct filter_rule **rules,
	uint32_t rule_count,
	struct memory_context *memory_context
) {
	struct filter_compile_net_attr *attr = memory_balloc(
		memory_context, sizeof(struct filter_compile_net_attr)
	);
	if (attr == NULL) {
		return NULL;
	}
	memset(attr, 0, sizeof(struct filter_compile_net_attr));

	/*
	 * The dedup arrays are sized from the instance count — one
	 * instance per rule and network pair — not from the rule count.
	 *
	 * A single rule may carry an unbounded network list, so a per rule
	 * constant overflows the dense view on the first wide rule. The
	 * distinct network count never exceeds the instance count, and the
	 * doubled open addressing capacity keeps a free slot to terminate
	 * the probe.
	 */
	struct filter_net4s nets;
	uint32_t instance_total = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}
		get_net4s(rule, &nets);
		instance_total += nets.count;
	}

	// One pass over the instances: normalize, index, record pairs.
	uint32_t pair_cap = 64;
	uint32_t pair_len = 0;
	uint32_t *pair_net = malloc(pair_cap * 4);
	uint32_t *pair_rule = malloc(pair_cap * 4);
	uint32_t dedup_cap = 16;
	while (dedup_cap < instance_total * 2) {
		dedup_cap <<= 1;
	}
	struct net4_dedup dedup = {
		.keys = calloc(dedup_cap, 8),
		.by_id = calloc(instance_total + 1, 8),
		.ids = calloc(dedup_cap, 4),
		.cap = dedup_cap,
		.count = 0,
	};
	if (pair_net == NULL || pair_rule == NULL || dedup.keys == NULL ||
	    dedup.by_id == NULL || dedup.ids == NULL) {
		goto error_host;
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}
		get_net4s(rule, &nets);
		for (uint32_t idx = 0; idx < nets.count; ++idx) {
			uint8_t key[8];
			for (uint32_t b = 0; b < 4; ++b) {
				key[b] = nets.items[idx].addr[b] &
					 nets.items[idx].mask[b];
				key[4 + b] = nets.items[idx].mask[b];
			}
			if (pair_len == pair_cap) {
				pair_cap *= 2;
				pair_net = realloc(pair_net, pair_cap * 4);
				pair_rule = realloc(pair_rule, pair_cap * 4);
				if (pair_net == NULL || pair_rule == NULL) {
					goto error_host;
				}
			}
			pair_net[pair_len] = net4_dedup_get(&dedup, key);
			pair_rule[pair_len] = rule_idx;
			++pair_len;
		}
	}
	uint32_t net_total = dedup.count;

	// Trie from the distinct networks only.
	struct range_collector collector;
	if (range_collector_init(&collector, memory_context)) {
		goto error_host;
	}
	for (uint32_t net_id = 0; net_id < net_total; ++net_id) {
		const uint8_t *key = dedup.by_id + net_id * 8;
		if (range4_collector_add(
			    &collector,
			    key,
			    __builtin_popcountll(*(const uint32_t *)(key + 4))
		    )) {
			goto error_collector;
		}
	}

	if (range_index_init(&attr->range_index, memory_context)) {
		goto error_collector;
	}

	attr->query_attr = (struct filter_query_attr_net4 *)memory_balloc(
		memory_context, sizeof(struct filter_query_attr_net4)
	);
	if (attr->query_attr == NULL) {
		goto error_range_index;
	}
	// FIXME lpm should be built while commit
	if (lpm_init(&attr->query_attr->lpm, memory_context, "filter:net4")) {
		goto error_query;
	}
	if (range_collector_collect(&collector, 4, &attr->range_index)) {
		goto error_collect;
	}
	if (range_index_build_lpm(
		    &attr->range_index, 4, &attr->query_attr->lpm
	    )) {
		goto error_collect;
	}
	if (vline_init(
		    &attr->line, memory_context, "filter:net4", collector.count
	    )) {
		goto error_collect;
	}
	range_collector_free(&collector, 4);

	// Bounds per distinct network, computed once.
	uint32_t *bounds = malloc(net_total * 8);
	if (bounds == NULL) {
		goto error_query;
	}
	uint32_t *range_index_values = ADDR_OF(&attr->range_index.values);
	for (uint32_t net_id = 0; net_id < net_total; ++net_id) {
		const uint8_t *key = dedup.by_id + net_id * 8;
		uint8_t from[4];
		uint8_t to[4];
		for (uint32_t idx = 0; idx < 4; ++idx) {
			from[idx] = key[idx];
			to[idx] = key[idx] | ~key[4 + idx];
		}
		filter_key_inc(4, to);
		uint32_t start =
			radix_lookup(&attr->range_index.radix, 4, from);
		uint32_t stop = radix_lookup(&attr->range_index.radix, 4, to);
		if (stop == RADIX_VALUE_INVALID) {
			// Only a mask with holes escapes the collected
			// regions; fail the compile instead of walking out
			// of bounds.
			free(bounds);
			goto error_query;
		}
		if (stop == 0) {
			// The only chance to read zero is the key past the
			// last collected boundary.
			stop = attr->range_index.count;
		}
		bounds[net_id * 2] = start;
		bounds[net_id * 2 + 1] = stop;
	}

	// Per network rule spans from the pair list, grouped by span.
	uint32_t *occ = calloc(net_total + 1, 4);
	uint32_t *span_off = calloc(net_total + 1, 4);
	uint32_t *net_rules = calloc(pair_len + 1, 4);
	if (occ == NULL || span_off == NULL || net_rules == NULL) {
		free(bounds);
		goto error_query;
	}
	for (uint32_t idx = 0; idx < pair_len; ++idx) {
		occ[pair_net[idx]]++;
	}
	for (uint32_t idx = 0; idx < net_total; ++idx) {
		span_off[idx + 1] = span_off[idx] + occ[idx];
		occ[idx] = span_off[idx];
	}
	for (uint32_t idx = 0; idx < pair_len; ++idx) {
		net_rules[occ[pair_net[idx]]++] = pair_rule[idx];
	}

	uint32_t group_cap = 16;
	while (group_cap < net_total * 2 + 16) {
		group_cap <<= 1;
	}
	uint32_t *group_slots = calloc(group_cap, 4);
	uint32_t *slot_group = calloc(group_cap, 4);
	uint32_t *net_group = calloc(net_total + 1, 4);
	uint32_t *group_rep = NULL;
	uint32_t *group_net_off = NULL;
	uint32_t *group_nets = NULL;
	if (group_slots == NULL || slot_group == NULL || net_group == NULL) {
		free(bounds);
		goto error_spans;
	}
	uint32_t group_count = 0;
	for (uint32_t net_id = 0; net_id < net_total; ++net_id) {
		const uint32_t *span = net_rules + span_off[net_id];
		uint32_t len = span_off[net_id + 1] - span_off[net_id];
		uint32_t slot = net4_span_hash(span, len) & (group_cap - 1);
		while (group_slots[slot] != 0) {
			uint32_t g = slot_group[slot] - 1;
			uint32_t rep = group_rep[g];
			if (span_off[rep + 1] - span_off[rep] == len &&
			    memcmp(net_rules + span_off[rep], span, len * 4) ==
				    0) {
				net_group[net_id] = g;
				goto next_net;
			}
			slot = (slot + 1) & (group_cap - 1);
		}
		group_slots[slot] = 1;
		slot_group[slot] = ++group_count;
		{
			uint32_t *grown =
				realloc(group_rep, (group_count + 1) * 4);
			if (grown == NULL) {
				free(bounds);
				goto error_spans;
			}
			group_rep = grown;
		}
		group_rep[group_count - 1] = net_id;
		net_group[net_id] = group_count - 1;
	next_net:;
	}

	group_net_off = calloc(group_count + 1, 4);
	group_nets = calloc(net_total + 1, 4);
	if (group_net_off == NULL || group_nets == NULL) {
		free(bounds);
		goto error_spans;
	}
	for (uint32_t net_id = 0; net_id < net_total; ++net_id) {
		group_net_off[net_group[net_id] + 1]++;
	}
	for (uint32_t g = 0; g < group_count; ++g) {
		group_net_off[g + 1] += group_net_off[g];
	}
	{
		uint32_t *cursor = calloc(group_count + 1, 4);
		if (cursor == NULL) {
			free(bounds);
			goto error_spans;
		}
		for (uint32_t g = 0; g < group_count; ++g) {
			cursor[g] = group_net_off[g];
		}
		for (uint32_t net_id = 0; net_id < net_total; ++net_id) {
			group_nets[cursor[net_group[net_id]]++] = net_id;
		}
		free(cursor);
	}

	/*
	 * Refine the region table: one generation per group, every distinct
	 * network walked once through its cached bounds.
	 */
	struct vline *table = &attr->line;
	struct remap_table remap;
	if (remap_table_init(&remap, memory_context, table->size)) {
		free(bounds);
		goto error_spans;
	}
	for (uint32_t g = 0; g < group_count; ++g) {
		remap_table_new_gen(&remap);
		for (uint32_t idx = group_net_off[g];
		     idx < group_net_off[g + 1];
		     ++idx) {
			uint32_t net_id = group_nets[idx];
			for (uint32_t ridx = bounds[net_id * 2];
			     ridx < bounds[net_id * 2 + 1];
			     ++ridx) {
				uint32_t *cell = vline_get_ptr(
					table, range_index_values[ridx]
				);
				remap_table_touch(&remap, *cell, cell);
			}
		}
	}
	remap_table_compact(&remap);
	vline_compact(table, &remap);
	uint32_t class_bound = remap.count + 1;
	remap_table_free(&remap);

	// Per group class lists, then per rule replay.
	uint32_t bitmap_words = (class_bound + 63) / 64;
	uint64_t *bitmap = calloc(bitmap_words + 1, 8);
	uint32_t **group_vals = calloc(group_count + 1, sizeof(uint32_t *));
	uint32_t *group_val_len = calloc(group_count + 1, 4);
	uint32_t *whole_vals = NULL;
	uint32_t whole_len = 0;
	uint32_t *seen = calloc(group_count + 1, 4);
	if (bitmap == NULL || group_vals == NULL || group_val_len == NULL ||
	    seen == NULL) {
		goto error_lists_build;
	}
	for (uint32_t g = 0; g < group_count; ++g) {
		memset(bitmap, 0, bitmap_words * 8);
		for (uint32_t idx = group_net_off[g];
		     idx < group_net_off[g + 1];
		     ++idx) {
			uint32_t net_id = group_nets[idx];
			for (uint32_t ridx = bounds[net_id * 2];
			     ridx < bounds[net_id * 2 + 1];
			     ++ridx) {
				uint32_t value = vline_get(
					table, range_index_values[ridx]
				);
				if (value < class_bound) {
					bitmap[value >> 6] |= 1ull
							      << (value & 63);
				}
			}
		}
		uint32_t len = 0;
		for (uint32_t w = 0; w < bitmap_words; ++w) {
			len += __builtin_popcountll(bitmap[w]);
		}
		group_vals[g] = malloc((len + 1) * 4);
		if (group_vals[g] == NULL) {
			goto error_lists_build;
		}
		uint32_t pos = 0;
		for (uint32_t w = 0; w < bitmap_words; ++w) {
			uint64_t word = bitmap[w];
			while (word != 0) {
				group_vals[g][pos++] =
					w * 64 + __builtin_ctzll(word);
				word &= word - 1;
			}
		}
		group_val_len[g] = len;
	}

	int need_whole = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count && !need_whole;
	     ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}
		get_net4s(rule, &nets);
		need_whole = nets.count == 0;
	}
	if (need_whole) {
		memset(bitmap, 0, bitmap_words * 8);
		for (uint32_t idx = 0; idx < table->size; ++idx) {
			uint32_t value = vline_get(table, idx);
			if (value < class_bound) {
				bitmap[value >> 6] |= 1ull << (value & 63);
			}
		}
		uint32_t len = 0;
		for (uint32_t w = 0; w < bitmap_words; ++w) {
			len += __builtin_popcountll(bitmap[w]);
		}
		whole_vals = malloc((len + 1) * 4);
		if (whole_vals == NULL) {
			goto error_lists_build;
		}
		uint32_t pos = 0;
		for (uint32_t w = 0; w < bitmap_words; ++w) {
			uint64_t word = bitmap[w];
			while (word != 0) {
				whole_vals[pos++] =
					w * 64 + __builtin_ctzll(word);
				word &= word - 1;
			}
		}
		whole_len = len;
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		if (value_registry_start(registry)) {
			goto error_lists_build;
		}
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}
		get_net4s(rule, &nets);
		if (nets.count == 0) {
			for (uint32_t idx = 0; idx < whole_len; ++idx) {
				value_registry_collect(
					registry, whole_vals[idx]
				);
			}
			continue;
		}
		for (uint32_t idx = 0; idx < nets.count; ++idx) {
			uint8_t key[8];
			for (uint32_t b = 0; b < 4; ++b) {
				key[b] = nets.items[idx].addr[b] &
					 nets.items[idx].mask[b];
				key[4 + b] = nets.items[idx].mask[b];
			}
			uint32_t g = net_group[net4_dedup_get(&dedup, key)];
			if (seen[g] == rule_idx + 1) {
				continue;
			}
			seen[g] = rule_idx + 1;
			for (uint32_t vi = 0; vi < group_val_len[g]; ++vi) {
				value_registry_collect(
					registry, group_vals[g][vi]
				);
			}
		}
	}

	for (uint32_t g = 0; g < group_count; ++g) {
		free(group_vals[g]);
	}
	free(group_vals);
	free(group_val_len);
	free(seen);
	free(bitmap);
	free(whole_vals);
	free(bounds);
	free(group_net_off);
	free(group_nets);
	free(group_rep);
	free(net_group);
	free(slot_group);
	free(group_slots);
	free(net_rules);
	free(span_off);
	free(occ);
	free(pair_net);
	free(pair_rule);
	free(dedup.keys);
	free(dedup.by_id);
	free(dedup.ids);

	return filter_compile_attr_net_commit(memory_context, &attr->attr);

error_lists_build:
	for (uint32_t g = 0; g < group_count; ++g) {
		free(group_vals[g]);
	}
	free(group_vals);
	free(group_val_len);
	free(seen);
	free(bitmap);
	free(whole_vals);
	free(bounds);
error_spans:
	free(group_net_off);
	free(group_nets);
	free(group_rep);
	free(net_group);
	free(slot_group);
	free(group_slots);
	free(net_rules);
	free(span_off);
	free(occ);
error_query:
	free(pair_net);
	free(pair_rule);
	free(dedup.keys);
	free(dedup.by_id);
	free(dedup.ids);
	filter_compile_attr_net_free(memory_context, &attr->attr);
	return NULL;
error_collect:
	lpm_free(&attr->query_attr->lpm);
	memory_bfree(
		memory_context,
		attr->query_attr,
		sizeof(struct filter_query_attr_net4)
	);
	attr->query_attr = NULL;
error_range_index:
	range_index_free(&attr->range_index);
error_collector:
	range_collector_free(&collector, 4);
error_host:
	free(pair_net);
	free(pair_rule);
	free(dedup.keys);
	free(dedup.by_id);
	free(dedup.ids);
	filter_compile_attr_net_free(memory_context, &attr->attr);
	return NULL;
}
