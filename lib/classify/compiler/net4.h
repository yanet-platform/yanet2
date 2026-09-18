#pragma once

#include "../rule.h"
#include "common/lpm.h"
#include "common/range_collector.h"
#include "common/registry.h"
#include "common/value.h"
#include "lib/classify/classifiers/net4.h"

#include "declare.h"
#include "helper.h"

typedef void (*filter_rule_get_net4s_func)(
	const struct filter_rule *filter_rule, struct filter_net4s *net
);

struct filter_compile_net_attr {
	struct classify_attr attr;
	struct classify_query_attr_net4 *query_attr;

	struct range_index range_index;
	struct value_table value_table;
};

struct classify_attr_net4_handlers {
	struct classify_attr_handlers attr_handlers;
	filter_rule_get_net4s_func get_net4s;
};

static inline struct classify_attr *
classify_attr_net_create(
	struct memory_context *memory_context,
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
) {
	struct classify_attr_net4_handlers *net_handlers = container_of(
		attr_handlers,
		struct classify_attr_net4_handlers,
		attr_handlers
	);

	struct filter_compile_net_attr *attr = memory_balloc(
		memory_context, sizeof(struct filter_compile_net_attr)
	);
	if (attr == NULL) {
		return NULL;
	}

	struct range_collector collector;
	if (range_collector_init(&collector, memory_context)) {
		goto error_free;
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

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
				    __builtin_popcountll(*(uint32_t *)
								  net4->mask)
			    )) {
				goto error_collector;
			}
		}
	}

	if (range_index_init(&attr->range_index, memory_context)) {
		goto error_collector;
	}

	attr->query_attr = (struct classify_query_attr_net4 *)memory_balloc(
		memory_context, sizeof(struct classify_query_attr_net4)
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

	if (value_table_init(
		    &attr->value_table,
		    memory_context,
		    "filter:net4",
		    1,
		    collector.count
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
		sizeof(struct classify_query_attr_net4)
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
classify_attr_net_size(const struct classify_attr *attr) {
	struct filter_compile_net_attr *net_attr =
		container_of(attr, struct filter_compile_net_attr, attr);

	return net_attr->value_table.v_dim * net_attr->value_table.h_dim;
}

static inline int
classify_attr_net_rule_is_any(
	const struct classify_attr *attr,
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {

	struct classify_attr_net4_handlers *net_handlers = container_of(
		attr_handlers,
		struct classify_attr_net4_handlers,
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
classify_attr_net_iterate(
	struct classify_attr *attr,
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule *rule,
	classify_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	struct classify_attr_net4_handlers *net_handlers = container_of(
		attr_handlers,
		struct classify_attr_net4_handlers,
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
			    ) < 0) {
				return -1;
			}
		}
	}

	return 0;
}

static inline int
classify_attr_net_iterate_any(
	struct classify_attr *attr,
	const struct classify_attr_handlers *attr_handlers,
	classify_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	(void)attr_handlers;

	struct filter_compile_net_attr *net_attr =
		container_of(attr, struct filter_compile_net_attr, attr);

	for (uint32_t idx = 0; idx < net_attr->value_table.h_dim; ++idx) {
		if (iter_cb_func(
			    value_table_get_ptr(&net_attr->value_table, 0, idx),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
	}

	return 0;
}

static inline void
classify_attr_net_free(
	struct memory_context *memory_context, struct classify_attr *attr
) {
	struct filter_compile_net_attr *net_attr =
		container_of(attr, struct filter_compile_net_attr, attr);

	if (net_attr->query_attr != NULL) {
		lpm_free(&net_attr->query_attr->lpm);
		memory_bfree(
			memory_context,
			net_attr->query_attr,
			sizeof(struct classify_query_attr_net4)
		);
	}

	range_index_free(&net_attr->range_index);
	value_table_free(&net_attr->value_table);

	memory_bfree(
		memory_context, attr, sizeof(struct filter_compile_net_attr)
	);
}

static inline struct classify_query_attr *
classify_attr_net_commit(
	struct memory_context *memory_context, struct classify_attr *attr
) {
	struct filter_compile_net_attr *net_attr =
		container_of(attr, struct filter_compile_net_attr, attr);

	struct classify_query_attr_net4 *query_attr = net_attr->query_attr;

	struct lpm *lpm = &query_attr->lpm;

	lpm4_remap(lpm, &net_attr->value_table);
	lpm4_compact(lpm);

	net_attr->query_attr = NULL;

	classify_attr_net_free(memory_context, attr);

	return &query_attr->attr;
}

static inline uint32_t
classify_attr_net4_hash(
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {
	const struct classify_attr_net4_handlers *net_handlers =
		container_of(
			attr_handlers,
			struct classify_attr_net4_handlers,
			attr_handlers
		);

	struct filter_net4s nets;
	net_handlers->get_net4s(rule, &nets);

	uint32_t hash = nets.count;
	for (uint32_t idx = 0; idx < nets.count; ++idx) {
		const struct net4 *net = nets.items + idx;
		for (uint32_t byte_idx = 0; byte_idx < 4; ++byte_idx) {
			hash = hash * 31 +
			       (net->addr[byte_idx] & net->mask[byte_idx]);
		}
		for (uint32_t byte_idx = 0; byte_idx < 4; ++byte_idx) {
			hash = hash * 31 + net->mask[byte_idx];
		}
	}
	return hash;
}

static inline int
classify_attr_net4_compare(
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule *first,
	const struct filter_rule *second
) {
	const struct classify_attr_net4_handlers *net_handlers =
		container_of(
			attr_handlers,
			struct classify_attr_net4_handlers,
			attr_handlers
		);

	struct filter_net4s first_nets;
	struct filter_net4s second_nets;
	net_handlers->get_net4s(first, &first_nets);
	net_handlers->get_net4s(second, &second_nets);

	if (first_nets.count != second_nets.count) {
		return 1;
	}

	for (uint32_t idx = 0; idx < first_nets.count; ++idx) {
		const struct net4 *first_net = first_nets.items + idx;
		const struct net4 *second_net = second_nets.items + idx;

		for (uint32_t byte_idx = 0; byte_idx < 4; ++byte_idx) {
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

static const struct classify_attr_handlers filter_compile_get_net = {
	.create = classify_attr_net_create,
	.size = classify_attr_net_size,
	.rule_iter = classify_attr_net_iterate,
	.rule_is_any = classify_attr_net_rule_is_any,
	.hash = classify_attr_net4_hash,
	.compare = classify_attr_net4_compare,
	.iter = classify_attr_net_iterate_any,
	.commit = classify_attr_net_commit,
	.free_compile = classify_attr_net_free,
	.free_query = classify_query_attr_net4_free,
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

static const struct classify_attr_net4_handlers
	classify_attr_net4_src = {
		.attr_handlers = filter_compile_get_net,
		.get_net4s = get_net_src,
};

static const struct classify_attr_net4_handlers
	classify_attr_net4_dst = {
		.attr_handlers = filter_compile_get_net,
		.get_net4s = get_net_dst,
};
