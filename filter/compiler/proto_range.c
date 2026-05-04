#include "proto_range.h"

#include "declare.h"

int
FILTER_ATTR_COMPILER_INIT_FUNC(proto_range)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_proto_range.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(proto_range)(
	void *data, struct memory_context *memory_context
) {
	struct filter_query_attr_proto_range *attr =
		(struct filter_query_attr_proto_range *)data;
	if (attr == NULL)
		return;

	filter_query_attr_proto_range_free(memory_context, &attr->attr);
}
