#pragma once

#include "common/container_of.h"
#include "common/memory.h"
#include "common/value.h"

#include "lib/filter2/filter.h"

struct filter_query_attr_device {
	struct filter_query_attr attr;
	struct vline line;
};

static inline void
filter_query_attr_device_free(
	struct memory_context *memory_context, struct filter_query_attr *attr
) {
	struct filter_query_attr_device *device_attr =
		container_of(attr, struct filter_query_attr_device, attr);

	vline_free(&device_attr->line);
	memory_bfree(
		memory_context,
		device_attr,
		sizeof(struct filter_query_attr_device)
	);
}
