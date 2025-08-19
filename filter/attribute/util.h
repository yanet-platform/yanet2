#pragma once

#include "common/registry.h"
#include "common/value.h"

////////////////////////////////////////////////////////////////////////////////

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