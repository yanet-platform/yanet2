#pragma once

#include "common/container_of.h"
#include "common/value.h"

#include "lib/filter2/filter.h"

struct filter_query_attr_proto_range {
	struct filter_query_attr attr;
	struct vline line;
};

static inline void
filter_query_attr_proto_range_free(
	struct memory_context *memory_context, struct filter_query_attr *attr
) {
	struct filter_query_attr_proto_range *proto_range_attr =
		container_of(attr, struct filter_query_attr_proto_range, attr);

	vline_free(&proto_range_attr->line);
	memory_bfree(
		memory_context,
		proto_range_attr,
		sizeof(struct filter_query_attr_proto_range)
	);
}
