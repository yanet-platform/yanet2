#include "filter/classifiers/port.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "filter/rule.h"

#include <stdint.h>

typedef void (*filter_rule_get_port_ranges_func)(
	const struct filter_rule *filter_rule,
	struct filter_port_ranges *port_ranges
);

struct filter_compile_attr_port {
	struct filter_compile_attr attr;
	struct filter_query_attr_port *query_attr;
};

struct filter_compile_attr_port_handlers {
	struct filter_compile_attr_handlers attr_handlers;
	filter_rule_get_port_ranges_func get_port_ranges;
};

static inline struct filter_compile_attr *
filter_compile_attr_port_create(
	struct memory_context *memory_context,
	const struct filter_compile_attr_handlers *handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
) {
	(void)handlers;
	(void)rules;
	(void)rule_count;

	struct filter_compile_attr_port *attr =
		(struct filter_compile_attr_port *)memory_balloc(
			memory_context, sizeof(struct filter_compile_attr_port)
		);
	if (attr == NULL) {
		return NULL;
	}

	attr->query_attr = (struct filter_query_attr_port *)memory_balloc(
		memory_context, sizeof(struct filter_query_attr_port)
	);
	if (attr->query_attr == NULL)
		goto error_free;

	if (value_table_init(
		    &attr->query_attr->value_table, memory_context, 1, 65536
	    )) {
		goto error_free_attr;
	}

	return &attr->attr;

error_free_attr:
	memory_bfree(
		memory_context,
		attr->query_attr,
		sizeof(struct filter_query_attr_port)
	);

error_free:
	memory_bfree(
		memory_context, attr, sizeof(struct filter_compile_attr_port)
	);

	return NULL;
}

static inline uint32_t
filter_compile_attr_port_size(const struct filter_compile_attr *attr) {
	(void)attr;
	return 65536;
}

static inline int
filter_compile_attr_port_iter(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	(void)attr_handlers;

	struct filter_compile_attr_port *port_ranges_attr =
		container_of(attr, struct filter_compile_attr_port, attr);

	struct filter_query_attr_port *query_attr =
		port_ranges_attr->query_attr;

	for (uint32_t idx = 0; idx < 65536; ++idx)
		if (iter_cb_func(
			    value_table_get_ptr(
				    &query_attr->value_table, 0, idx
			    ),
			    cb_func_data
		    ))
			return -1;
	return 0;
}

static inline int
filter_compile_attr_port_rule_is_any(
	const struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {
	struct filter_compile_attr_port_handlers *port_ranges_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_port_handlers,
			attr_handlers
		);
	(void)attr;

	struct filter_port_ranges ranges;
	port_ranges_handlers->get_port_ranges(rule, &ranges);

	return ranges.count == 0 ||
	       ranges.items[0].to - ranges.items[0].from == 65535;
}

static inline int
filter_compile_attr_port_rule_iter(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	struct filter_compile_attr_port_handlers *port_ranges_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_port_handlers,
			attr_handlers
		);

	struct filter_compile_attr_port *port_ranges_attr =
		container_of(attr, struct filter_compile_attr_port, attr);

	struct filter_query_attr_port *query_attr =
		port_ranges_attr->query_attr;

	struct filter_port_ranges ranges;
	port_ranges_handlers->get_port_ranges(rule, &ranges);
	const struct filter_port_ranges *port_ranges = &ranges;

	for (uint32_t range_idx = 0; range_idx < port_ranges->count;
	     ++range_idx) {
		const struct filter_port_range *port_range =
			port_ranges->items + range_idx;
		for (uint32_t port = port_range->from; port <= port_range->to;
		     ++port) {
			if (iter_cb_func(
				    value_table_get_ptr(
					    &query_attr->value_table, 0, port
				    ),
				    cb_func_data
			    )) {
				return -1;
			}
		}
	}

	return 0;
}

static inline void
filter_compile_attr_port_free(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_attr_port *port_ranges_attr =
		container_of(attr, struct filter_compile_attr_port, attr);

	if (port_ranges_attr->query_attr != NULL) {
		value_table_free(&port_ranges_attr->query_attr->value_table);

		memory_bfree(
			memory_context,
			port_ranges_attr->query_attr,
			sizeof(struct filter_query_attr_port)
		);
	}

	memory_bfree(
		memory_context,
		port_ranges_attr,
		sizeof(struct filter_compile_attr_port)
	);
}

static inline struct filter_query_attr *
filter_compile_attr_port_commit(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_attr_port *port_ranges_attr =
		container_of(attr, struct filter_compile_attr_port, attr);

	struct filter_query_attr_port *query_attr =
		port_ranges_attr->query_attr;
	port_ranges_attr->query_attr = NULL;

	filter_compile_attr_port_free(memory_context, attr);

	return &query_attr->attr;
}

static const struct filter_compile_attr_handlers
	filter_compile_attr_port_handlers = {
		.create = filter_compile_attr_port_create,
		.size = filter_compile_attr_port_size,
		.iter = filter_compile_attr_port_iter,
		.rule_is_any = filter_compile_attr_port_rule_is_any,
		.rule_iter = filter_compile_attr_port_rule_iter,
		.commit = filter_compile_attr_port_commit,
		.free_compile = filter_compile_attr_port_free,
		.free_query = filter_query_attr_port_free,
};

static inline void
filter_rule_get_port_ranges_src(
	const struct filter_rule *rule, struct filter_port_ranges *port_ranges
) {
	port_ranges->count = rule->transport.src_count;
	port_ranges->items = rule->transport.srcs;
}

static inline void
filter_rule_get_port_ranges_dst(
	const struct filter_rule *rule, struct filter_port_ranges *port_ranges
) {
	port_ranges->count = rule->transport.dst_count;
	port_ranges->items = rule->transport.dsts;
}

static const struct filter_compile_attr_port_handlers
	filter_compile_attr_port_src = {
		.attr_handlers = filter_compile_attr_port_handlers,
		.get_port_ranges = filter_rule_get_port_ranges_src,
};

static const struct filter_compile_attr_port_handlers
	filter_compile_attr_port_dst = {
		.attr_handlers = filter_compile_attr_port_handlers,
		.get_port_ranges = filter_rule_get_port_ranges_dst,
};

int
FILTER_ATTR_COMPILER_INIT_FUNC(port_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_port_dst.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(port_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_port_src.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(port_src)(
	void *data, struct memory_context *memory_context
) {
	struct filter_query_attr_port *attr =
		(struct filter_query_attr_port *)data;
	if (attr == NULL)
		return;

	filter_query_attr_port_free(memory_context, &attr->attr);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(port_dst)(
	void *data, struct memory_context *memory_context
) {
	struct filter_query_attr_port *attr =
		(struct filter_query_attr_port *)data;
	if (attr == NULL)
		return;

	filter_query_attr_port_free(memory_context, &attr->attr);
}
