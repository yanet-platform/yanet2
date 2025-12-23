#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "filter/rule.h"

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

static inline int
lpm_collect_value_iterator(uint32_t value, void *data) {
	struct value_table *table = (struct value_table *)data;
	return value_table_touch(table, 0, value);
}

static inline int
lpm_collect_registry_iterator(uint32_t value, void *data) {
	struct value_registry *registry = (struct value_registry *)data;
	return value_registry_collect(registry, value);
}