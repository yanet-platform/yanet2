#include "port.h"

#include "declare.h"

int
FILTER_ATTR_COMPILER_INIT_FUNC(port_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_port_dst.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(port_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_port_src.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(port_src)(
	void *data, struct memory_context *memory_context
) {
	struct filter_query_attr_port *attr =
		(struct filter_query_attr_port *)data;
	if (attr == NULL)
		return;

	filter_query_attr_port_free(memory_context, &attr->attr);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(port_dst)(
	void *data, struct memory_context *memory_context
) {
	struct filter_query_attr_port *attr =
		(struct filter_query_attr_port *)data;
	if (attr == NULL)
		return;

	filter_query_attr_port_free(memory_context, &attr->attr);
}
