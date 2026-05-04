#include "net6.h"

#include "declare.h"

// Allows to initialize attribute for IPv6 destination address.
int
FILTER_ATTR_COMPILER_INIT_FUNC(net6_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_net6_src.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

// Allows to initialize attribute for IPv6 source address.
int
FILTER_ATTR_COMPILER_INIT_FUNC(net6_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_net6_dst.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

////////////////////////////////////////////////////////////////////////////////
// Free
////////////////////////////////////////////////////////////////////////////////

// Allows to free data for IPv6 classification.
static inline void
free_net6(void *data, struct memory_context *memory_context) {
	if (data == NULL)
		return;
	struct filter_query_attr_net6 *c =
		(struct filter_query_attr_net6 *)data;
	if (c == NULL)
		return;
	lpm_free(&c->lo);
	lpm_free(&c->hi);
	value_table_free(&c->comb);
	memory_bfree(memory_context, c, sizeof(struct filter_query_attr_net6));
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net6_src)(
	void *data, struct memory_context *memory_context
) {
	free_net6(data, memory_context);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net6_dst)(
	void *data, struct memory_context *memory_context
) {
	free_net6(data, memory_context);
}
