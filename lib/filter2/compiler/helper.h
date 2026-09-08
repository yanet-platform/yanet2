#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "lib/filter2/rule.h"

#include <stdbool.h>

// True when the mask is a contiguous address prefix.
bool
filter2_net4_mask_is_valid(const uint8_t mask[NET4_LEN]);

// True when both halves of the mask are contiguous prefixes.
bool
filter2_net6_mask_is_valid(const uint8_t mask[NET6_LEN]);

int
filter2_merge_and_collect_registry(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry
);

int
filter2_merge_and_set_registry_values(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table
);

int
init_dummy_registry(
	struct memory_context *memory_context,
	uint32_t actions,
	struct value_registry *registry
);

static inline int
lpm_collect_registry_iterator(uint32_t value, void *data) {
	struct value_registry *registry = (struct value_registry *)data;
	return value_registry_collect(registry, value);
}
