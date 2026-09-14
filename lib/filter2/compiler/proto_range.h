#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "lib/filter2/classifiers/proto_range.h"
#include "lib/filter2/rule.h"
#include "u16_ranges.h"

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
	if (attr->query_attr == NULL) {
		goto error_free;
	}

	if (vline_init(
		    &attr->query_attr->line,
		    memory_context,
		    "filter:proto_range",
		    65536
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

static inline void
filter_compile_attr_proto_free(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_attr_proto *proto_ranges_attr =
		container_of(attr, struct filter_compile_attr_proto, attr);

	if (proto_ranges_attr->query_attr != NULL) {
		vline_free(&proto_ranges_attr->query_attr->line);

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

static inline void
filter_rule_get_proto_ranges(
	const struct filter_rule *rule, struct filter_proto_ranges *proto_ranges
) {
	proto_ranges->count = rule->transport.proto_count;
	proto_ranges->items = rule->transport.protos;
}

FILTER_COMPILE_ATTR_U16_RANGES_BUILD_AS_DECLARE(proto_range)

static const struct filter_compile_attr_handlers
	filter_compile_proto_range_handlers = {
		.build = filter_compile_attr_proto_range_build,
		.free_query = filter_query_attr_proto_range_free,
};

static const struct filter_compile_attr_proto_handlers
	filter_compile_attr_proto_range = {
		.attr_handlers = filter_compile_proto_range_handlers,
		.get_proto_ranges = filter_rule_get_proto_ranges,
};

FILTER_COMPILE_ATTR_U16_RANGES_BUILD_AS(
	proto_range,
	filter_compile_attr_proto,
	struct filter_proto_ranges,
	get_proto_ranges
)
