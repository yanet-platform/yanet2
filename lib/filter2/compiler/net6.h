#pragma once

#include "../rule.h"

#include "common/lpm.h"
#include "common/range_collector.h"
#include "common/registry.h"

#include "lib/filter2/classifiers/net6.h"

#include "declare.h"

typedef void (*filter_rule_get_net6s_func)(
	const struct filter_rule *filter_rule, struct filter_net6s *net6s
);

struct filter_compile_net6s_attr {
	struct filter_compile_attr attr;
	struct filter_query_attr_net6 *query_attr;
	// Count of non uniform comb rows; the dense rebuild before commit
	// packs exactly these into the final comb.
	uint32_t dense_pending;

	struct range_index ri_hi;
	struct range_index ri_lo;
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
	memcpy(dst->addr, src->addr, 16);
	memcpy(dst->mask, src->mask, 16);
	for (uint8_t idx = 0; idx < 16; ++idx) {
		dst->addr[idx] &= src->mask[idx];
	}
}

static inline void
filter_compile_attr_net6s_free(
	struct memory_context *memory_context, struct filter_compile_attr *attr
);

static inline void
filter_compile_attr_net6s_free(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_net6s_attr *net6s_attr =
		container_of(attr, struct filter_compile_net6s_attr, attr);

	range_index_free(&net6s_attr->ri_hi);
	range_index_free(&net6s_attr->ri_lo);

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

	struct filter_query_attr_net6 *query_attr = net6s_attr->query_attr;
	net6s_attr->query_attr = NULL;

	filter_compile_attr_net6s_free(memory_context, attr);

	return &query_attr->attr;
}

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

// Half space region bounds of a normalized network, with the wrap case
// of an upper bound past the last collected boundary mapped to the end.
//
// A mask with holes makes the upper bound escape the collected regions;
// that is reported as a failure instead of being walked.
static inline int
net6_half_bounds(
	const struct range_index *ri,
	const uint8_t *addr,
	const uint8_t *mask,
	uint32_t *start,
	uint32_t *stop
) {
	uint8_t from[8];
	uint8_t to[8];
	for (uint32_t idx = 0; idx < 8; ++idx) {
		from[idx] = addr[idx];
		to[idx] = addr[idx] | ~mask[idx];
	}
	filter_key_inc(8, to);
	*start = radix_lookup(&ri->radix, 8, from);
	*stop = radix_lookup(&ri->radix, 8, to);
	if (*stop == RADIX_VALUE_INVALID) {
		return -1;
	}
	if (*stop == 0) {
		// The only chance to read zero is the key past the last
		// collected boundary.
		*stop = ri->count;
	}
	return 0;
}

// Host side open addressing map from a 32 byte network to an id.
struct net6_dedup {
	// The hash table stores keys by slot; the id keyed view below is
	// the one every consumer indexes, since returned ids are dense
	// while slots are not.
	uint8_t *keys;
	uint8_t *by_id;
	uint32_t *ids;
	uint32_t cap;
	uint32_t count;
};

static inline uint32_t
net6_dedup_hash(const uint8_t *key) {
	uint32_t h = 2166136261u;
	for (uint32_t idx = 0; idx < 32; ++idx) {
		h = (h ^ key[idx]) * 16777619u;
	}
	return h ? h : 1;
}

static inline uint32_t
net6_dedup_get(struct net6_dedup *d, const uint8_t *key) {
	uint32_t slot = net6_dedup_hash(key) & (d->cap - 1);
	while (d->ids[slot] != 0) {
		if (memcmp(d->keys + slot * 32, key, 32) == 0) {
			return d->ids[slot] - 1;
		}
		slot = (slot + 1) & (d->cap - 1);
	}
	memcpy(d->keys + slot * 32, key, 32);
	d->ids[slot] = ++d->count;
	memcpy(d->by_id + (d->count - 1) * 32, key, 32);
	return d->count - 1;
}

static inline uint32_t
net6_span_hash(const uint32_t *span, uint32_t len) {
	uint32_t h = 2166136261u;
	for (uint32_t idx = 0; idx < len; ++idx) {
		h = (h ^ span[idx]) * 16777619u;
	}
	return h ? h : 1;
}

// Builds one half trie from the distinct networks of the id keyed view.
static inline int
net6_build_range_from_distinct(
	struct memory_context *memory_context,
	const struct net6_dedup *dedup,
	uint32_t net_total,
	net6_get_part_func get_part,
	struct lpm *lpm,
	struct range_index *ri
) {
	struct range_collector collector;
	if (range_collector_init(&collector, memory_context)) {
		return -1;
	}
	for (uint32_t net_id = 0; net_id < net_total; ++net_id) {
		struct net6 *net6 = (struct net6 *)(dedup->by_id + net_id * 32);
		uint8_t *addr;
		uint8_t *mask;
		get_part(net6, &addr, &mask);
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
		goto error;
	}
	if (range_collector_collect(&collector, 8, ri)) {
		goto error;
	}
	if (range_index_build_lpm(ri, 8, lpm)) {
		goto error;
	}
	range_collector_free(&collector, 8);
	return 0;
error:
	range_collector_free(&collector, 8);
	return -1;
}

// Walks the comb cells covered by a network through its cached half
// bounds; the callback receives every covered coordinate pair.
static inline void
net6_walk_bounds(
	const struct range_index *ri_hi,
	const struct range_index *ri_lo,
	const uint32_t *net_bounds,
	void (*cb)(uint32_t v, uint32_t h, void *data),
	void *data
) {
	uint32_t *values_hi = ADDR_OF(&ri_hi->values);
	uint32_t *values_lo = ADDR_OF(&ri_lo->values);
	for (uint32_t idx_hi = net_bounds[0]; idx_hi < net_bounds[1];
	     ++idx_hi) {
		for (uint32_t idx_lo = net_bounds[2]; idx_lo < net_bounds[3];
		     ++idx_lo) {
			cb(values_hi[idx_hi], values_lo[idx_lo], data);
		}
	}
}

struct net6_touch_ctx {
	struct remap_table *remap;
	struct value_table *comb;
};

static inline void
net6_touch_cb(uint32_t v, uint32_t h, void *data) {
	struct net6_touch_ctx *ctx = data;
	uint32_t *cell = value_table_get_ptr(ctx->comb, v, h);
	remap_table_touch(ctx->remap, *cell, cell);
}

// Bitmap over the compacted class space; a set bit is a class present in
// the walked coverage.
struct net6_bitmap {
	uint64_t *words;
	uint32_t bound;
};

static inline void
net6_bitmap_set(struct net6_bitmap *b, uint32_t value) {
	if (value < b->bound) {
		b->words[value >> 6] |= 1ull << (value & 63);
	}
}

struct net6_bitmap_ctx {
	struct value_table *comb;
	struct net6_bitmap *bitmap;
};

static inline void
net6_bitmap_cb(uint32_t v, uint32_t h, void *data) {
	struct net6_bitmap_ctx *ctx = data;
	net6_bitmap_set(ctx->bitmap, value_table_get(ctx->comb, v, h));
}

/*
 * Deduplicated net6 builder: the whole pipeline in one hand written
 * routine. Networks repeated across rules collapse by rule set, the comb
 * is refined with one remap generation per group walking every distinct
 * network exactly once, and each rule registry range is replayed from
 * per group class snapshots instead of re-walking the rule networks.
 */
static inline struct filter_query_attr *
filter_compile_attr_net6s_commit(
	struct memory_context *memory_context, struct filter_compile_attr *attr
);

// Copies the non uniform comb rows into the dense table. Returns -1 on
// allocation failure with the dense table already released.
static inline int
net6_copy_dense_rows(
	struct value_table *dense,
	const struct value_table *comb,
	const uint32_t *row_scalar,
	const uint32_t *row_index,
	uint32_t hi_count
) {
	for (uint32_t hi = 0; hi < hi_count; ++hi) {
		if (row_scalar[hi] != FILTER_NET6_ROW_2D) {
			continue;
		}
		uint32_t dense_row = row_index[hi];
		for (uint32_t lo = 0; lo < comb->h_dim; ++lo) {
			*value_table_get_ptr(dense, dense_row, lo) =
				value_table_get(comb, hi, lo);
		}
	}
	return 0;
}

static inline struct filter_query_attr *
filter_compile_attr_net6s_build_dedup(
	filter_rule_get_net6s_func get_net6s,
	struct value_registry *registry,
	const struct filter_rule **rules,
	uint32_t rule_count,
	struct memory_context *memory_context
) {
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
		goto error;
	}
	memset(attr->query_attr, 0, sizeof(struct filter_query_attr_net6));

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
	struct filter_net6s nets;
	uint32_t instance_total = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}
		get_net6s(rule, &nets);
		instance_total += nets.count;
	}

	/*
	 * One pass over the rule instances: normalize, index each distinct
	 * network, and record (network, rule) pairs. Everything downstream
	 * works from the pairs and the distinct network list.
	 */
	uint32_t dedup_cap = 16;
	while (dedup_cap < instance_total * 2) {
		dedup_cap <<= 1;
	}
	struct net6_dedup dedup = {
		.keys = calloc(dedup_cap, 32),
		.by_id = calloc(instance_total + 1, 32),
		.ids = calloc(dedup_cap, 4),
		.cap = dedup_cap,
		.count = 0,
	};
	uint32_t *occ = NULL;
	uint32_t *span_off = NULL;
	uint32_t *net_rules = NULL;
	uint32_t *group_slots = NULL;
	uint32_t *slot_group = NULL;
	uint32_t *net_group = NULL;
	uint32_t *group_rep = NULL;
	uint32_t *group_net_off = NULL;
	uint32_t *group_nets = NULL;
	uint32_t *pair_net = NULL;
	uint32_t *pair_rule = NULL;
	uint32_t *bounds = NULL;
	if (dedup.keys == NULL || dedup.ids == NULL || dedup.by_id == NULL) {
		goto error_host;
	}

	uint32_t pair_cap = 64;
	uint32_t pair_len = 0;
	pair_net = malloc(pair_cap * 4);
	pair_rule = malloc(pair_cap * 4);
	if (pair_net == NULL || pair_rule == NULL) {
		goto error_host;
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}
		get_net6s(rule, &nets);
		for (uint32_t idx = 0; idx < nets.count; ++idx) {
			struct net6 net6;
			net6_normalize(nets.items + idx, &net6);
			if (pair_len == pair_cap) {
				pair_cap *= 2;
				pair_net = realloc(pair_net, pair_cap * 4);
				pair_rule = realloc(pair_rule, pair_cap * 4);
				if (pair_net == NULL || pair_rule == NULL) {
					goto error_host;
				}
			}
			pair_net[pair_len] =
				net6_dedup_get(&dedup, (const uint8_t *)&net6);
			pair_rule[pair_len] = rule_idx;
			++pair_len;
		}
	}
	uint32_t net_total = dedup.count;

	// Half tries from the distinct networks only.
	if (net6_build_range_from_distinct(
		    memory_context,
		    &dedup,
		    net_total,
		    net6_get_hi_part,
		    &attr->query_attr->hi,
		    &attr->ri_hi
	    )) {
		goto error;
	}

	if (net6_build_range_from_distinct(
		    memory_context,
		    &dedup,
		    net_total,
		    net6_get_lo_part,
		    &attr->query_attr->lo,
		    &attr->ri_lo
	    )) {
		goto error;
	}

	if (value_table_init(
		    &attr->query_attr->comb,
		    memory_context,
		    "filter:net6",
		    attr->ri_hi.max_value + 1,
		    attr->ri_lo.max_value + 1
	    )) {
		goto error;
	}

	// Bounds per distinct network, computed once: the two half ranges
	// of every network in region indexes.
	bounds = malloc((net_total + 1) * 16);
	if (bounds == NULL) {
		goto error_host;
	}
	for (uint32_t net_id = 0; net_id < net_total; ++net_id) {
		const uint8_t *key = dedup.by_id + net_id * 32;
		if (net6_half_bounds(
			    &attr->ri_hi,
			    key,
			    key + 16,
			    &bounds[net_id * 4],
			    &bounds[net_id * 4 + 1]
		    ) ||
		    net6_half_bounds(
			    &attr->ri_lo,
			    key + 8,
			    key + 24,
			    &bounds[net_id * 4 + 2],
			    &bounds[net_id * 4 + 3]
		    )) {
			goto error_host;
		}
	}

	occ = calloc(net_total + 1, 4);
	span_off = calloc(net_total + 1, 4);
	net_rules = calloc(pair_len + 1, 4);
	if (occ == NULL || span_off == NULL || net_rules == NULL) {
		goto error_host;
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
	group_slots = calloc(group_cap, 4);
	slot_group = calloc(group_cap, 4);
	net_group = calloc(net_total + 1, 4);
	if (group_slots == NULL || slot_group == NULL || net_group == NULL) {
		goto error_host;
	}
	uint32_t group_count = 0;
	for (uint32_t net_id = 0; net_id < net_total; ++net_id) {
		const uint32_t *span = net_rules + span_off[net_id];
		uint32_t len = span_off[net_id + 1] - span_off[net_id];
		uint32_t slot = net6_span_hash(span, len) & (group_cap - 1);
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
				goto error_host;
			}
			group_rep = grown;
		}
		group_rep[group_count - 1] = net_id;
		net_group[net_id] = group_count - 1;
	next_net:;
	}

	// Flat per group network lists.
	group_net_off = calloc(group_count + 1, 4);
	group_nets = calloc(net_total + 1, 4);
	if (group_net_off == NULL || group_nets == NULL) {
		goto error_host;
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
			goto error_host;
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
	 * Refine the comb: one remap generation per group, each distinct
	 * network walked exactly once.
	 */
	struct value_table *comb = &attr->query_attr->comb;
	struct remap_table remap;
	if (remap_table_init(
		    &remap, memory_context, comb->v_dim * comb->h_dim
	    )) {
		goto error_host;
	}
	struct net6_touch_ctx touch_ctx = {.remap = &remap, .comb = comb};
	for (uint32_t g = 0; g < group_count; ++g) {
		remap_table_new_gen(&remap);
		for (uint32_t idx = group_net_off[g];
		     idx < group_net_off[g + 1];
		     ++idx) {
			net6_walk_bounds(
				&attr->ri_hi,
				&attr->ri_lo,
				bounds + group_nets[idx] * 4,
				net6_touch_cb,
				&touch_ctx
			);
		}
	}
	remap_table_compact(&remap);
	value_table_compact(comb, &remap);
	uint32_t class_bound = remap.count + 1;
	remap_table_free(&remap);

	/*
	 * Rows uniform across all lo values carry their result directly:
	 * the lo trie walk and the comb load are skipped for them at query
	 * time.
	 */
	{
		uint32_t hi_count = comb->v_dim;
		uint32_t *row_scalar = memory_balloc(
			memory_context, sizeof(uint32_t) * hi_count
		);
		uint32_t *row_index = memory_balloc(
			memory_context, sizeof(uint32_t) * hi_count
		);
		if (row_scalar == NULL || row_index == NULL) {
			memory_bfree(
				memory_context,
				row_scalar,
				sizeof(uint32_t) * hi_count
			);
			memory_bfree(
				memory_context,
				row_index,
				sizeof(uint32_t) * hi_count
			);
			goto error_host;
		}

		/*
		 * Classify rows: a row whose every cell reads the same
		 * class carries it directly (the lo lookup is skipped at
		 * query time); the remaining rows pack densely into a
		 * rebuilt comb.
		 */
		uint32_t dense_count = 0;
		for (uint32_t hi = 0; hi < hi_count; ++hi) {
			uint32_t first = value_table_get(comb, hi, 0);
			uint32_t lo = 1;
			for (; lo < comb->h_dim; ++lo) {
				if (value_table_get(comb, hi, lo) != first) {
					break;
				}
			}
			if (lo == comb->h_dim) {
				row_scalar[hi] = first;
				row_index[hi] = 0;
			} else {
				row_scalar[hi] = FILTER_NET6_ROW_2D;
				row_index[hi] = dense_count++;
			}
		}

		// The dense rebuild happens after the registry ranges are
		// replayed: those still index the comb by original row.
		attr->dense_pending = dense_count;
		SET_OFFSET_OF(&attr->query_attr->row_scalar, row_scalar);
		SET_OFFSET_OF(&attr->query_attr->row_index, row_index);
		attr->query_attr->row_count = hi_count;
	}

	/*
	 * Registry ranges: one per rule in order, replayed from per group
	 * class lists. Each group is walked once and its distinct classes
	 * recorded; a rule replays the lists of its groups without walking
	 * again. Rules with no networks of this attribute take the whole
	 * area class list.
	 */
	uint32_t bitmap_words = (class_bound + 63) / 64;
	uint64_t *bitmap = calloc(bitmap_words + 1, 8);
	uint32_t **group_vals = calloc(group_count + 1, sizeof(uint32_t *));
	uint32_t *group_val_len = calloc(group_count + 1, 4);
	uint32_t *whole_vals = NULL;
	uint32_t whole_len = 0;
	uint32_t *seen = calloc(group_count + 1, 4);
	if (bitmap == NULL || group_vals == NULL || group_val_len == NULL ||
	    seen == NULL) {
		free(bitmap);
		free(group_vals);
		free(group_val_len);
		free(seen);
		goto error_host;
	}
	struct net6_bitmap_ctx bitmap_ctx = {
		.comb = comb,
		.bitmap = &(struct net6_bitmap){
			.words = bitmap, .bound = class_bound
		},
	};
	for (uint32_t g = 0; g < group_count; ++g) {
		memset(bitmap, 0, bitmap_words * 8);
		for (uint32_t nidx = group_net_off[g];
		     nidx < group_net_off[g + 1];
		     ++nidx) {
			net6_walk_bounds(
				&attr->ri_hi,
				&attr->ri_lo,
				bounds + group_nets[nidx] * 4,
				net6_bitmap_cb,
				&bitmap_ctx
			);
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
				uint32_t bit = w * 64 + __builtin_ctzll(word);
				group_vals[g][pos++] = bit;
				word &= word - 1;
			}
		}
		group_val_len[g] = len;
	}
	free(bitmap);
	bitmap = NULL;

	// Whole area class list, only when some rule has no networks of
	// this attribute.
	int need_whole = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count && !need_whole;
	     ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}
		get_net6s(rule, &nets);
		need_whole = nets.count == 0;
	}
	if (need_whole) {
		uint64_t *whole = calloc(bitmap_words + 1, 8);
		if (whole == NULL) {
			goto error_lists_build;
		}
		struct net6_bitmap whole_map = {
			.words = whole, .bound = class_bound
		};
		for (uint32_t v = 0; v < comb->v_dim; ++v) {
			for (uint32_t h = 0; h < comb->h_dim; ++h) {
				net6_bitmap_set(
					&whole_map, value_table_get(comb, v, h)
				);
			}
		}
		uint32_t len = 0;
		for (uint32_t w = 0; w < bitmap_words; ++w) {
			len += __builtin_popcountll(whole[w]);
		}
		whole_vals = malloc((len + 1) * 4);
		if (whole_vals == NULL) {
			free(whole);
			goto error_lists_build;
		}
		uint32_t pos = 0;
		for (uint32_t w = 0; w < bitmap_words; ++w) {
			uint64_t word = whole[w];
			while (word != 0) {
				whole_vals[pos++] =
					w * 64 + __builtin_ctzll(word);
				word &= word - 1;
			}
		}
		whole_len = len;
		free(whole);
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		if (value_registry_start(registry)) {
			goto error_lists_build;
		}
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}
		get_net6s(rule, &nets);
		if (nets.count == 0) {
			for (uint32_t idx = 0; idx < whole_len; ++idx) {
				value_registry_collect(
					registry, whole_vals[idx]
				);
			}
			continue;
		}
		for (uint32_t idx = 0; idx < nets.count; ++idx) {
			struct net6 net6;
			net6_normalize(nets.items + idx, &net6);
			uint32_t g = net_group[net6_dedup_get(
				&dedup, (const uint8_t *)&net6
			)];
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
	free(whole_vals);
	free(seen);
	free(bounds);
	free(pair_net);
	free(pair_rule);
	free(group_net_off);
	free(group_nets);
	free(group_rep);
	free(net_group);
	free(slot_group);
	free(group_slots);
	free(net_rules);
	free(span_off);
	free(occ);
	free(dedup.keys);
	free(dedup.by_id);
	free(dedup.ids);

	/*
	 * Rebuild the comb over the non uniform rows only: those rows were
	 * fully walked for the uniformity verdict, so dropping the uniform
	 * ones frees the bulk of the materialized chunks.
	 */
	{
		struct value_table *comb = &attr->query_attr->comb;
		uint32_t hi_count = attr->query_attr->row_count;
		uint32_t *row_scalar = ADDR_OF(&attr->query_attr->row_scalar);
		uint32_t *row_index = ADDR_OF(&attr->query_attr->row_index);
		if (attr->dense_pending != 0) {
			struct value_table dense;
			if (value_table_init(
				    &dense,
				    memory_context,
				    "filter:net6",
				    attr->dense_pending,
				    comb->h_dim
			    ) ||
			    net6_copy_dense_rows(
				    &dense,
				    comb,
				    row_scalar,
				    row_index,
				    hi_count
			    )) {
				// The host arrays and registries are already
				// released on this tail; unwind locally and
				// hand the failure to the caller as a plain
				// attribute error.
				filter_compile_attr_net6s_free(
					memory_context, &attr->attr
				);
				return NULL;
			}
			value_table_free(comb);
			// Field by field: the table carries relative pointers
			// that a struct copy would strand.
			comb->v_dim = dense.v_dim;
			comb->h_dim = dense.h_dim;
			SET_OFFSET_OF(&comb->values, ADDR_OF(&dense.values));
			SET_OFFSET_OF(
				&comb->memory_context,
				ADDR_OF(&dense.memory_context)
			);
		}
	}

	return filter_compile_attr_net6s_commit(memory_context, &attr->attr);

error_lists_build:
	for (uint32_t g = 0; g < group_count; ++g) {
		free(group_vals[g]);
	}
	free(group_vals);
	free(group_val_len);
	free(whole_vals);
	free(seen);
	free(bounds);
	free(pair_net);
	free(pair_rule);
	free(bitmap);
error_host:
	free(bounds);
	free(pair_net);
	free(pair_rule);
	free(group_net_off);
	free(group_nets);
	free(group_rep);
	free(net_group);
	free(slot_group);
	free(group_slots);
	free(net_rules);
	free(span_off);
	free(occ);
	free(dedup.keys);
	free(dedup.by_id);
	free(dedup.ids);
error:
	filter_compile_attr_net6s_free(memory_context, &attr->attr);
	return NULL;
}

static inline struct filter_query_attr *
filter_compile_attr_net6_src_build(
	struct value_registry *registry,
	const struct filter_rule **rules,
	uint32_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_net6s_build_dedup(
		get_net6s_src, registry, rules, rule_count, memory_context
	);
}

static inline struct filter_query_attr *
filter_compile_attr_net6_dst_build(
	struct value_registry *registry,
	const struct filter_rule **rules,
	uint32_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_net6s_build_dedup(
		get_net6s_dst, registry, rules, rule_count, memory_context
	);
}

static const struct filter_compile_attr_handlers
	filter_compile_net6_src_handlers = {
		.build = filter_compile_attr_net6_src_build,
		.free_query = filter_query_attr_net6_free,
};

static const struct filter_compile_attr_handlers
	filter_compile_net6_dst_handlers = {
		.build = filter_compile_attr_net6_dst_build,
		.free_query = filter_query_attr_net6_free,
};

static const struct filter_compile_attr_net6s_handlers
	filter_compile_attr_net6_src = {
		.attr_handlers = filter_compile_net6_src_handlers,
		.get_net6s = get_net6s_src,
};

static const struct filter_compile_attr_net6s_handlers
	filter_compile_attr_net6_dst = {
		.attr_handlers = filter_compile_net6_dst_handlers,
		.get_net6s = get_net6s_dst,
};
