#pragma once

#include "common/container_of.h"
#include "common/value.h"

#include "lib/classify/classify.h"

struct classify_query_attr_proto_range {
	struct classify_query_attr attr;
	struct vline line;
};

static inline void
classify_query_attr_proto_range_free(
	struct memory_context *memory_context, struct classify_query_attr *attr
) {
	struct classify_query_attr_proto_range *proto_range_attr = container_of(
		attr, struct classify_query_attr_proto_range, attr
	);

	vline_free(&proto_range_attr->line);
	memory_bfree(
		memory_context,
		proto_range_attr,
		sizeof(struct classify_query_attr_proto_range)
	);
}
