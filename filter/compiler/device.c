#include "device.h"

#include "declare.h"

int
FILTER_ATTR_COMPILER_INIT_FUNC(device)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_device.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(device)(
	void *data, struct memory_context *memory_context
) {
	struct filter_query_attr_device *attr =
		(struct filter_query_attr_device *)data;
	if (attr == NULL)
		return;

	filter_query_attr_device_free(memory_context, &attr->attr);
}
