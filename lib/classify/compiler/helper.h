#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "lib/classify/classify.h"

// The rule coverage classes of a stage's value space over a rule
// projection, written as a shared memory value line.
//
// The stage must be a join output: its classes are numbered from
// one, so zero never carries coverage.
int
classify_coverage_map(
	struct memory_context *memory_context,
	const struct classifier *stage,
	uint32_t rule_count,
	const uint8_t *rule_mask,
	struct vline *map
);

// Join over the left classes compacted by a coverage map.
//
// The left registry is cloned through the map; the joint table spans
// the compacted classes plus the zero row for the uncovered rest. The
// name scopes the joint memory contexts like classify_join's.
int
classify_join_compact(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *left,
	const struct classifier *right,
	uint32_t rule_count,
	const struct vline *compact_map,
	struct value_table *table,
	struct classifier *out
);

int
classify_join(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *left,
	const struct classifier *right,
	uint32_t rule_count,
	struct value_table *table,
	struct classifier *out
);

// The name titles the rule map memory context of the decode in the
// memory tree.
int
classify_decode(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *cls,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	struct vline *rule_map
);
