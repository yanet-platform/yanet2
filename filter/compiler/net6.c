#include "../rule.h"

#include "common/lpm.h"
#include "common/range_collector.h"
#include "common/registry.h"

#include "filter/classifiers/net6.h"

#include "declare.h"

typedef void (*filter_rule_get_net6s_func)(
	const struct filter_rule *filter_rule, struct filter_net6s *net6s
);

struct filter_compile_net6s_attr {
	struct filter_compile_attr attr;
	struct filter_query_attr_net6 *query_attr;

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
	for (uint8_t idx = 0; idx < 16; ++idx)
		dst->addr[idx] &= src->mask[idx];
}

static inline int
create_net6_range(
	struct memory_context *memory_context,
	const struct filter_rule **rules,
	uint32_t rule_count,
	filter_rule_get_net6s_func get_net6,
	net6_get_part_func get_part,
	struct lpm *lpm,
	struct range_index *ri
) {
	struct range_collector collector;
	if (range_collector_init(&collector, memory_context))
		goto error;

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];

		if (rule == NULL)
			continue;

		struct filter_net6s net6s;
		get_net6(rule, &net6s);
		const struct filter_net6s *nets = &net6s;

		for (struct net6 *rule_net = nets->items;
		     rule_net < nets->items + nets->count;
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

	attr->query_attr = (struct filter_query_attr_net6 *)memory_balloc(
		memory_context, sizeof(struct filter_query_attr_net6)
	);
	if (attr->query_attr == NULL)
		goto error_query;

	create_net6_range(
		memory_context,
		rules,
		rule_count,
		net6s_handlers->get_net6s,
		net6_get_hi_part,
		&attr->query_attr->hi,
		&attr->ri_hi
	);

	create_net6_range(
		memory_context,
		rules,
		rule_count,
		net6s_handlers->get_net6s,
		net6_get_lo_part,
		&attr->query_attr->lo,
		&attr->ri_lo
	);

	value_table_init(
		&attr->query_attr->comb,
		memory_context,
		attr->ri_hi.max_value + 1,
		attr->ri_lo.max_value + 1
	);

	return &attr->attr;

error_query:
	memory_bfree(
		memory_context, attr, sizeof(struct filter_compile_net6s_attr)
	);

	return NULL;
}

static inline uint32_t
filter_compile_attr_net6s_size(const struct filter_compile_attr *attr) {
	struct filter_compile_net6s_attr *net6s_attr =
		container_of(attr, struct filter_compile_net6s_attr, attr);

	return net6s_attr->query_attr->comb.h_dim *
	       net6s_attr->query_attr->comb.v_dim;
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

	struct value_table *value_table = &net6s_attr->query_attr->comb;
	for (uint32_t v_idx = 0; v_idx < value_table->v_dim; ++v_idx) {
		for (uint32_t h_idx = 0; h_idx < value_table->h_dim; ++h_idx) {
			if (iter_cb_func(
				    value_table_get_ptr(
					    value_table, v_idx, h_idx
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

	if (nets.count == 0)
		return 1;

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
	const struct filter_net6s *net6s = &nets;

	uint32_t *values_hi = ADDR_OF(&net6s_attr->ri_hi.values);
	uint32_t *values_lo = ADDR_OF(&net6s_attr->ri_lo.values);

	for (uint32_t net_idx = 0; net_idx < net6s->count; ++net_idx) {
		const struct net6 *net = net6s->items + net_idx;

		struct net6 net6_normalized;
		net6_normalize(net, &net6_normalized);
		struct net6 *net6 = &net6_normalized;

		uint8_t from_hi[8];
		uint8_t to_hi[8];
		for (uint32_t idx = 0; idx < 8; ++idx) {
			from_hi[idx] = net6->addr[idx];
			to_hi[idx] = net6->addr[idx] | ~net6->mask[idx];
		}
		filter_key_inc(8, to_hi);
		uint32_t start_hi =
			radix_lookup(&net6s_attr->ri_hi.radix, 8, from_hi);
		uint32_t stop_hi =
			radix_lookup(&net6s_attr->ri_hi.radix, 8, to_hi);
		if (stop_hi == 0) {
			/*
			 * The only chance get zero here is for the last one
			 * item.
			 */
			stop_hi = net6s_attr->ri_hi.count;
		}

		uint8_t from_lo[8];
		uint8_t to_lo[8];
		for (uint32_t idx = 0; idx < 8; ++idx) {
			from_lo[idx] = net6->addr[idx + 8];
			to_lo[idx] = net6->addr[idx + 8] | ~net6->mask[idx + 8];
		}
		filter_key_inc(8, to_lo);
		uint32_t start_lo =
			radix_lookup(&net6s_attr->ri_lo.radix, 8, from_lo);
		uint32_t stop_lo =
			radix_lookup(&net6s_attr->ri_lo.radix, 8, to_lo);
		if (stop_lo == 0) {
			/*
			 * The only chance get zero here is for the last one
			 * item.
			 */
			stop_lo = net6s_attr->ri_lo.count;
		}

		for (uint32_t idx_hi = start_hi; idx_hi < stop_hi; ++idx_hi) {
			for (uint32_t idx_lo = start_lo; idx_lo < stop_lo;
			     ++idx_lo) {
				if (iter_cb_func(
					    value_table_get_ptr(
						    &net6s_attr->query_attr
							     ->comb,
						    values_hi[idx_hi],
						    values_lo[idx_lo]
					    ),
					    cb_func_data
				    )) {
					return -1;
				}
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

static const struct filter_compile_attr_handlers filter_compile_get_net6s = {
	.create = filter_compile_attr_net6s_create,
	.size = filter_compile_attr_net6s_size,
	.iter = filter_compile_attr_net6s_iter,
	.rule_iter = filter_compile_attr_net6s_rule_iter,
	.rule_is_any = filter_compile_attr_net6s_rule_is_any,
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

// Allows to initialize attribute for IPv6 destination address.
int
FILTER_ATTR_COMPILER_INIT_FUNC(net6_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_net6_src.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

// Allows to initialize attribute for IPv6 source address.
int
FILTER_ATTR_COMPILER_INIT_FUNC(net6_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_net6_dst.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
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
	struct filter_query_attr_net6 *c =
		(struct filter_query_attr_net6 *)data;
	if (c == NULL)
		return;
	lpm_free(&c->lo);
	lpm_free(&c->hi);
	value_table_free(&c->comb);
	memory_bfree(memory_context, c, sizeof(struct filter_query_attr_net6));
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
