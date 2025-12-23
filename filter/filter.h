#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include <stdint.h>
#include <threads.h>

// Maximum number of attributes supported by the filter
#define MAX_ATTRIBUTES 10

// Represents vertex in the classification tree
struct filter_vertex {
	struct value_registry registry;
	struct value_table table;
	void *data; // relative pointer compatible
};

// Temporary storage for intermediate classifiers during query
struct filter_slots {
	uint32_t slots[2 * MAX_ATTRIBUTES][2];
};

static inline void
filter_slots_put_value(struct filter_slots *slots, uint32_t v, uint32_t value) {
	slots->slots[v / 2][v & 1] = value;
}

static inline uint32_t
filter_vertex_left_slot(struct filter_slots *slots, uint32_t v) {
	return slots->slots[v][0];
}

static inline uint32_t
filter_vertex_right_slot(struct filter_slots *slots, uint32_t v) {
	return slots->slots[v][1];
}

struct filter {
	struct filter_vertex v[2 * MAX_ATTRIBUTES];
	struct memory_context memory_context;
};