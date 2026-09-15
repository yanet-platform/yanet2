#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "lib/filter2/classifiers/port.h"
#include "lib/filter2/rule.h"
#include "u16_ranges.h"

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
	if (attr->query_attr == NULL) {
		goto error_free;
	}

	if (vline_init(
		    &attr->query_attr->line,
		    memory_context,
		    "filter:port",
		    65536
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

static inline void
filter_compile_attr_port_free(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_attr_port *port_ranges_attr =
		container_of(attr, struct filter_compile_attr_port, attr);

	if (port_ranges_attr->query_attr != NULL) {
		vline_free(&port_ranges_attr->query_attr->line);

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

FILTER_COMPILE_ATTR_U16_RANGES_BUILD_AS_DECLARE(port_src)
FILTER_COMPILE_ATTR_U16_RANGES_BUILD_AS_DECLARE(port_dst)

static const struct filter_compile_attr_handlers
	filter_compile_port_src_handlers = {
		.build = filter_compile_attr_port_src_build,
		.free_query = filter_query_attr_port_free,
};

static const struct filter_compile_attr_handlers
	filter_compile_port_dst_handlers = {
		.build = filter_compile_attr_port_dst_build,
		.free_query = filter_query_attr_port_free,
};

static const struct filter_compile_attr_port_handlers
	filter_compile_attr_port_src = {
		.attr_handlers = filter_compile_port_src_handlers,
		.get_port_ranges = filter_rule_get_port_ranges_src,
};

static const struct filter_compile_attr_port_handlers
	filter_compile_attr_port_dst = {
		.attr_handlers = filter_compile_port_dst_handlers,
		.get_port_ranges = filter_rule_get_port_ranges_dst,
};

FILTER_COMPILE_ATTR_U16_RANGES_BUILD_AS(
	port_src,
	filter_compile_attr_port,
	struct filter_port_ranges,
	get_port_ranges
)
FILTER_COMPILE_ATTR_U16_RANGES_BUILD_AS(
	port_dst,
	filter_compile_attr_port,
	struct filter_port_ranges,
	get_port_ranges
)
