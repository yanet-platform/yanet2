#pragma once

#include "common/container_of.h"

#include "filter/filter.h"

struct filter_query_attr_port {
	struct filter_query_attr attr;
	struct value_table value_table;
};

static inline void
filter_query_attr_port_free(
	struct memory_context *memory_context, struct filter_query_attr *attr
) {
	struct filter_query_attr_port *port_attr =
		container_of(attr, struct filter_query_attr_port, attr);

	value_table_free(&port_attr->value_table);
	memory_bfree(
		memory_context, port_attr, sizeof(struct filter_query_attr_port)
	);
}
