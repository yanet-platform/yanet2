#pragma once

#include "common/container_of.h"
#include "common/memory.h"
#include "common/value.h"

#include "lib/classify/classify.h"

struct classify_query_attr_device {
	struct classify_query_attr attr;
	struct value_table value_table;
};

static inline void
classify_query_attr_device_free(
	struct memory_context *memory_context, struct classify_query_attr *attr
) {
	struct classify_query_attr_device *device_attr =
		container_of(attr, struct classify_query_attr_device, attr);

	value_table_free(&device_attr->value_table);
	memory_bfree(
		memory_context,
		device_attr,
		sizeof(struct classify_query_attr_device)
	);
}
