#include "net4.h"

#include "declare.h"

int
FILTER_ATTR_COMPILER_INIT_FUNC(net4_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_net4_src.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(net4_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_net4_dst.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net4_src)(
	void *data, struct memory_context *memory_context
) {
	struct filter_query_attr_net4 *attr =
		(struct filter_query_attr_net4 *)data;
	if (attr == NULL)
		return;

	filter_query_attr_net4_free(memory_context, &attr->attr);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net4_dst)(
	void *data, struct memory_context *memory_context
) {
	struct filter_query_attr_net4 *attr =
		(struct filter_query_attr_net4 *)data;
	if (attr == NULL)
		return;

	filter_query_attr_net4_free(memory_context, &attr->attr);
}
