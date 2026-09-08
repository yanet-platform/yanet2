#pragma once

#include "common/container_of.h"

#include "lib/filter2/filter.h"

struct filter_query_attr_port {
	struct filter_query_attr attr;
	struct vline line;
};

static inline void
filter_query_attr_port_free(
	struct memory_context *memory_context, struct filter_query_attr *attr
) {
	struct filter_query_attr_port *port_attr =
		container_of(attr, struct filter_query_attr_port, attr);

	vline_free(&port_attr->line);
	memory_bfree(
		memory_context, port_attr, sizeof(struct filter_query_attr_port)
	);
}
