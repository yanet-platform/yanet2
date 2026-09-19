#pragma once

#include "common/container_of.h"
#include "common/value.h"

#include "lib/classify/classify.h"

struct classify_query_attr_port {
	struct classify_query_attr attr;
	struct vline line;
};

static inline void
classify_query_attr_port_free(
	struct memory_context *memory_context, struct classify_query_attr *attr
) {
	struct classify_query_attr_port *port_attr =
		container_of(attr, struct classify_query_attr_port, attr);

	vline_free(&port_attr->line);
	memory_bfree(
		memory_context,
		port_attr,
		sizeof(struct classify_query_attr_port)
	);
}
