#pragma once

#include "common/container_of.h"
#include "common/value.h"

#include "filter/filter.h"

#include "segments.h"

struct filter_query_attr_proto_range {
	struct filter_query_attr attr;
	struct value_table value_table;
};

static inline void
filter_query_attr_proto_range_free(
	struct memory_context *memory_context, struct filter_query_attr *attr
) {
	struct filter_query_attr_proto_range *proto_range_attr =
		container_of(attr, struct filter_query_attr_proto_range, attr);

	value_table_free(&proto_range_attr->value_table);
	memory_bfree(
		memory_context,
		proto_range_attr,
		sizeof(struct filter_query_attr_proto_range)
	);
}

struct proto_range_fast_classifier {
	struct segment_u16_classifier classifier;
};
