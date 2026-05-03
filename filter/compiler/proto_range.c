#include "filter/classifiers/proto_range.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "filter/rule.h"

#include <stdint.h>

typedef void (*filter_rule_get_proto_ranges_func)(
	const struct filter_rule *filter_rule,
	struct filter_proto_ranges *proto_ranges
);

struct filter_compile_attr_proto {
	struct filter_compile_attr attr;
	struct filter_query_attr_proto_range *query_attr;
};

struct filter_compile_attr_proto_handlers {
	struct filter_compile_attr_handlers attr_handlers;
	filter_rule_get_proto_ranges_func get_proto_ranges;
};

static inline struct filter_compile_attr *
filter_compile_attr_proto_create(
	struct memory_context *memory_context,
	const struct filter_compile_attr_handlers *handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
) {
	(void)handlers;
	(void)rules;
	(void)rule_count;

	struct filter_compile_attr_proto *attr =
		(struct filter_compile_attr_proto *)memory_balloc(
			memory_context, sizeof(struct filter_compile_attr_proto)
		);
	if (attr == NULL) {
		return NULL;
	}

	attr->query_attr =
		(struct filter_query_attr_proto_range *)memory_balloc(
			memory_context,
			sizeof(struct filter_query_attr_proto_range)
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
		sizeof(struct filter_query_attr_proto_range)
	);

error_free:
	memory_bfree(
		memory_context, attr, sizeof(struct filter_compile_attr_proto)
	);

	return NULL;
}

static inline uint32_t
filter_compile_attr_proto_size(const struct filter_compile_attr *attr) {
	(void)attr;
	return 65536;
}

static inline int
filter_compile_attr_proto_iter(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	(void)attr_handlers;

	struct filter_compile_attr_proto *proto_ranges_attr =
		container_of(attr, struct filter_compile_attr_proto, attr);

	struct filter_query_attr_proto_range *query_attr =
		proto_ranges_attr->query_attr;

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
filter_compile_attr_proto_rule_is_any(
	const struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {
	struct filter_compile_attr_proto_handlers *proto_ranges_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_proto_handlers,
			attr_handlers
		);
	(void)attr;

	struct filter_proto_ranges ranges;
	proto_ranges_handlers->get_proto_ranges(rule, &ranges);

	return ranges.count == 0 ||
	       ranges.items[0].to - ranges.items[0].from == 65535;
}

static inline int
filter_compile_attr_proto_rule_iter(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	struct filter_compile_attr_proto_handlers *proto_ranges_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_proto_handlers,
			attr_handlers
		);

	struct filter_compile_attr_proto *proto_ranges_attr =
		container_of(attr, struct filter_compile_attr_proto, attr);

	struct filter_query_attr_proto_range *query_attr =
		proto_ranges_attr->query_attr;

	struct filter_proto_ranges ranges;
	proto_ranges_handlers->get_proto_ranges(rule, &ranges);
	const struct filter_proto_ranges *proto_ranges = &ranges;

	for (uint32_t range_idx = 0; range_idx < proto_ranges->count;
	     ++range_idx) {
		const struct filter_proto_range *proto_range =
			proto_ranges->items + range_idx;
		for (uint32_t proto = proto_range->from;
		     proto <= proto_range->to;
		     ++proto) {
			if (iter_cb_func(
				    value_table_get_ptr(
					    &query_attr->value_table, 0, proto
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
filter_compile_attr_proto_free(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_attr_proto *proto_ranges_attr =
		container_of(attr, struct filter_compile_attr_proto, attr);

	if (proto_ranges_attr->query_attr != NULL) {
		value_table_free(&proto_ranges_attr->query_attr->value_table);

		memory_bfree(
			memory_context,
			proto_ranges_attr->query_attr,
			sizeof(struct filter_query_attr_proto_range)
		);
	}

	memory_bfree(
		memory_context,
		proto_ranges_attr,
		sizeof(struct filter_compile_attr_proto)
	);
}

static inline struct filter_query_attr *
filter_compile_attr_proto_commit(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_attr_proto *proto_ranges_attr =
		container_of(attr, struct filter_compile_attr_proto, attr);

	struct filter_query_attr_proto_range *query_attr =
		proto_ranges_attr->query_attr;
	proto_ranges_attr->query_attr = NULL;

	filter_compile_attr_proto_free(memory_context, attr);

	return &query_attr->attr;
}

static const struct filter_compile_attr_handlers
	filter_compile_attr_proto_handlers = {
		.create = filter_compile_attr_proto_create,
		.size = filter_compile_attr_proto_size,
		.iter = filter_compile_attr_proto_iter,
		.rule_is_any = filter_compile_attr_proto_rule_is_any,
		.rule_iter = filter_compile_attr_proto_rule_iter,
		.commit = filter_compile_attr_proto_commit,
		.free_compile = filter_compile_attr_proto_free,
		.free_query = filter_query_attr_proto_range_free,
};

static inline void
filter_rule_get_proto_ranges(
	const struct filter_rule *rule, struct filter_proto_ranges *proto_ranges
) {
	proto_ranges->count = rule->transport.proto_count;
	proto_ranges->items = rule->transport.protos;
}

static const struct filter_compile_attr_proto_handlers
	filter_compile_attr_proto_range = {
		.attr_handlers = filter_compile_attr_proto_handlers,
		.get_proto_ranges = filter_rule_get_proto_ranges,
};

int
FILTER_ATTR_COMPILER_INIT_FUNC(proto_range)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_proto_range.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(proto_range)(
	void *data, struct memory_context *memory_context
) {
	struct filter_query_attr_proto_range *attr =
		(struct filter_query_attr_proto_range *)data;
	if (attr == NULL)
		return;

	filter_query_attr_proto_range_free(memory_context, &attr->attr);
}
