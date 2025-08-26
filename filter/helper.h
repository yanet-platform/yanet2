#pragma once

#include "common/memory.h"
#include "common/registry.h"

#include "rule.h"
#include "trie.h"

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

////////////////////////////////////////////////////////////////////////////////

int
fill_rule_registry_by_trie(
	const struct trie *trie,
	uint32_t rule_count,
	struct value_registry *registry,
	struct memory_context *mctx
);