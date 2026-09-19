#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "lib/classify/rule.h"

int
classify_merge_and_collect(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	const uint32_t *rule_to_group1,
	struct value_registry *registry2,
	const uint32_t *rule_to_group2,
	uint32_t rule_count,
	struct value_table *table,
	struct value_registry *registry,
	uint32_t *rule_to_group
);

int
classify_collect_rule_map(
	struct memory_context *memory_context,
	struct value_registry *registry,
	const uint32_t *rule_to_group,
	const struct filter_rule **rules,
	uint32_t rule_count,
	struct vline *rule_map
);
