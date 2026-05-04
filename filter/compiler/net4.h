#pragma once

#include "../rule.h"
#include "common/lpm.h"
#include "common/range_collector.h"
#include "common/registry.h"
#include "common/value.h"
#include "filter/classifiers/net4.h"

#include "declare.h"
#include "helper.h"

typedef void (*filter_rule_get_net4s_func)(
	const struct filter_rule *filter_rule, struct filter_net4s *net
);

struct filter_compile_net_attr {
	struct filter_compile_attr attr;
	struct filter_query_attr_net4 *query_attr;

	struct range_index range_index;
	struct value_table value_table;
};

struct filter_compile_attr_net4_handlers {
	struct filter_compile_attr_handlers attr_handlers;
	filter_rule_get_net4s_func get_net4s;
};

static inline struct filter_compile_attr *
filter_compile_attr_net_create(
	struct memory_context *memory_context,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
) {
	struct filter_compile_attr_net4_handlers *net_handlers = container_of(
		attr_handlers,
		struct filter_compile_attr_net4_handlers,
		attr_handlers
	);

	struct filter_compile_net_attr *attr = memory_balloc(
		memory_context, sizeof(struct filter_compile_net_attr)
	);
	if (attr == NULL) {
		return NULL;
	}

	struct range_collector collector;
	if (range_collector_init(&collector, memory_context))
		goto error_free;

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL)
			continue;

		struct filter_net4s nets;
		net_handlers->get_net4s(rule, &nets);
		const struct filter_net4s *net = &nets;

		for (struct net4 *net4 = net->items;
		     net4 < net->items + net->count;
		     ++net4) {
			uint8_t from[4];
			for (uint32_t idx = 0; idx < 4; ++idx) {
				from[idx] = net4->addr[idx] & net4->mask[idx];
			}

			if (range4_collector_add(
				    &collector,
				    from,
				    __builtin_popcountll(*(uint32_t *)net4->mask
				    )
			    )) {
				goto error_collector;
			}
		}
	}

	if (range_index_init(&attr->range_index, memory_context)) {
		goto error_collector;
	}

	attr->query_attr = (struct filter_query_attr_net4 *)memory_balloc(
		memory_context, sizeof(struct filter_query_attr_net4)
	);
	if (attr->query_attr == NULL)
		goto error_range_index;

	// FIXME lpm should be built while commit
	if (lpm_init(&attr->query_attr->lpm, memory_context)) {
		goto error_query;
	}

	if (range_collector_collect(
		    &collector, 4, &attr->query_attr->lpm, &attr->range_index
	    )) {
		goto error_collect;
	}

	if (value_table_init(
		    &attr->value_table, memory_context, 1, collector.count
	    )) {
		goto error_collect;
	}

	range_collector_free(&collector, 4);

	return &attr->attr;

error_collect:
	lpm_free(&attr->query_attr->lpm);

error_query:
	memory_bfree(
		memory_context,
		attr->query_attr,
		sizeof(struct filter_query_attr_net4)
	);

error_range_index:
	range_index_free(&attr->range_index);

error_collector:
	range_collector_free(&collector, 4);

error_free:
	memory_bfree(
		memory_context, attr, sizeof(struct filter_compile_net_attr)
	);

	return NULL;
}

static inline uint32_t
filter_compile_attr_net_size(const struct filter_compile_attr *attr) {
	struct filter_compile_net_attr *net_attr =
		container_of(attr, struct filter_compile_net_attr, attr);

	return net_attr->value_table.v_dim * net_attr->value_table.h_dim;
}

static inline int
filter_compile_attr_net_rule_is_any(
	const struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {

	struct filter_compile_attr_net4_handlers *net_handlers = container_of(
		attr_handlers,
		struct filter_compile_attr_net4_handlers,
		attr_handlers
	);

	(void)attr;

	struct filter_net4s nets;
	net_handlers->get_net4s(rule, &nets);

	return nets.count == 0 ||
	       (nets.items[0].mask[0] == 0 && nets.items[0].mask[1] == 0 &&
		nets.items[0].mask[2] == 0 && nets.items[0].mask[3] == 0);
}

static inline int
filter_compile_attr_net_iterate(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	struct filter_compile_attr_net4_handlers *net_handlers = container_of(
		attr_handlers,
		struct filter_compile_attr_net4_handlers,
		attr_handlers
	);

	struct filter_compile_net_attr *net_attr =
		container_of(attr, struct filter_compile_net_attr, attr);

	struct filter_net4s nets;
	net_handlers->get_net4s(rule, &nets);
	const struct filter_net4s *net = &nets;

	uint32_t *range_index_values = ADDR_OF(&net_attr->range_index.values);

	for (uint32_t net_idx = 0; net_idx < net->count; ++net_idx) {
		const struct net4 *net4 = net->items + net_idx;

		uint8_t from[4];
		uint8_t to[4];
		for (uint32_t idx = 0; idx < 4; ++idx) {
			from[idx] = net4->addr[idx] & net4->mask[idx];
			to[idx] = net4->addr[idx] | ~net4->mask[idx];
		}
		filter_key_inc(4, to);

		uint32_t start =
			radix_lookup(&net_attr->range_index.radix, 4, from);
		uint32_t stop =
			radix_lookup(&net_attr->range_index.radix, 4, to);
		if (stop == 0) {
			/*
			 * The only chance get zero here is for the last one
			 * item.
			 */
			stop = net_attr->range_index.count;
		}

		for (uint32_t idx = start; idx < stop; ++idx) {
			if (iter_cb_func(
				    value_table_get_ptr(
					    &net_attr->value_table,
					    0,
					    range_index_values[idx]
				    ),
				    cb_func_data
			    )) {
				return -1;
			}
		}
	}

	return 0;
}

static inline int
filter_compile_attr_net_iterate_any(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	(void)attr_handlers;

	struct filter_compile_net_attr *net_attr =
		container_of(attr, struct filter_compile_net_attr, attr);

	for (uint32_t idx = 0; idx < net_attr->value_table.h_dim; ++idx) {
		if (iter_cb_func(
			    value_table_get_ptr(&net_attr->value_table, 0, idx),
			    cb_func_data
		    )) {
			return -1;
		}
	}

	return 0;
}

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
	value_table_free(&net_attr->value_table);

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

	struct lpm *lpm = &query_attr->lpm;

	lpm4_remap(lpm, &net_attr->value_table);
	lpm4_compact(lpm);

	net_attr->query_attr = NULL;

	filter_compile_attr_net_free(memory_context, attr);

	return &query_attr->attr;
}

static const struct filter_compile_attr_handlers filter_compile_get_net = {
	.create = filter_compile_attr_net_create,
	.size = filter_compile_attr_net_size,
	.rule_iter = filter_compile_attr_net_iterate,
	.rule_is_any = filter_compile_attr_net_rule_is_any,
	.iter = filter_compile_attr_net_iterate_any,
	.commit = filter_compile_attr_net_commit,
	.free_compile = filter_compile_attr_net_free,
	.free_query = filter_query_attr_net4_free,
};

static inline void
get_net_src(const struct filter_rule *rule, struct filter_net4s *net) {
	net->count = rule->net4.src_count;
	net->items = rule->net4.srcs;
}

static inline void
get_net_dst(const struct filter_rule *rule, struct filter_net4s *net) {
	net->count = rule->net4.dst_count;
	net->items = rule->net4.dsts;
}

static const struct filter_compile_attr_net4_handlers
	filter_compile_attr_net4_src = {
		.attr_handlers = filter_compile_get_net,
		.get_net4s = get_net_src,
};

static const struct filter_compile_attr_net4_handlers
	filter_compile_attr_net4_dst = {
		.attr_handlers = filter_compile_get_net,
		.get_net4s = get_net_dst,
};
