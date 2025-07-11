#pragma once

#include "attribute.h"
#include "common/registry.h"

// Represents vertex in the classfication tree
struct filter_vertex {
	struct value_registry registry;

	// 2-dim table
	// [left_son_classifier][right_son_classifier]
	// -> combined classifier
	//
	// does not fill the table for leaves
	struct value_table table;

	// Calculated classifier
	// from left son and right son.
	//
	// Not relevant for leaves.
	uint32_t slots[2];

	// Data structure which allows to
	// lookup packet classifier corresponds to
	// leaf.x
	void *data;
};

struct filter {
	// Vertices in the classification tree.
	//
	// Vertices enumerated in [1..2*n-1].
	// Leaves are in [n..2*n-1].
	// 1 is root.
	struct filter_vertex v[2 * MAX_ATTRIBUTES];

	// Filter attributes
	struct filter_attribute attr[MAX_ATTRIBUTES];

	// Attributes count
	uint32_t n;

	struct memory_context memory_context;
};

// Allows to initialize filter with provided attributes and actions.
int
filter_init(
	struct filter *filter,
	const struct filter_attribute *attributes,
	uint32_t attributes_count,
	const struct filter_rule *actions,
	uint32_t actions_count,
	struct memory_context *memory_context
);

// Allows to query actions corresponds to the provided packet.
int
filter_query(
	struct filter *filter,
	struct packet *packet,
	uint32_t **actions,
	uint32_t *count
);

void
filter_free(struct filter *filter);