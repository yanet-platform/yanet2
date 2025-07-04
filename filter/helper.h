#include "common/memory.h"
#include "common/registry.h"

#include "action.h"

int merge_and_collect_registry(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
);

int
set_registry_values(
	struct memory_context *memory_context,
	const struct filter_action *actions,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
);
