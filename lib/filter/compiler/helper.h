#pragma once

#include <stdbool.h>

#include "common/memory.h"
#include "common/network.h"
#include "common/registry.h"
#include "common/value.h"

#include "lib/filter/rule.h"

int
merge_and_collect_registry(
	struct memory_context *memory_context,
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry,
	const char *table_name
);

int
merge_and_set_registry_values(
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

// Checks that an IPv4 network mask is a contiguous prefix mask.
//
// A contiguous prefix mask is a run of set high bits followed only by zero
// bits, which is the only shape the LPM-derived prefix length classifiers in
// net4.c can represent correctly.
bool
filter_net4_mask_is_valid(const uint8_t mask[NET4_LEN]);

// Checks that an IPv6 network mask is bi-contiguous.
//
// Bi-contiguous means each 64-bit half of the mask (bytes[0..7] and
// bytes[8..15]) is independently a contiguous prefix mask, which matches how
// net6.c derives a prefix length per half via popcount. A hole exactly at
// the /64 boundary is therefore still accepted; a hole within a half is not.
bool
filter_net6_mask_is_valid(const uint8_t mask[NET6_LEN]);
