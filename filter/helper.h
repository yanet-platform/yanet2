#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "rule.h"

int
merge_and_collect_registry(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
);

int
merge_and_set_registry_values(
	struct memory_context *memory_context,
	const struct filter_rule *actions,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
);

int
init_dummy_registry(
	struct memory_context *memory_context,
	uint32_t actions,
	struct value_registry *registry
);