#pragma once

#include "common/registry.h"
#include "lib/dataplane/packet/packet.h"

#include "action.h"

#define MAX_ATTRIBUTES 10

// This function is provided by user.
// It should initialize user-defined data-structure for
// classifying packet and initialize registry according to
// the following rules:
// 	1. i-th registry range corresponds to the i-th action
// 	2. values for the i-th range corresponds to the classifiers from i-th
// action
typedef int (*attr_init_func)(
	struct value_registry *registry,
	void **data,
	const struct filter_action *actions,
	size_t actions_count,
	struct memory_context *memory_context
);

typedef uint32_t (*attr_lookup_func)(struct packet *packet, void *data);

struct filter_attribute {
	attr_init_func init_func;
	attr_lookup_func lookup_func;
};

extern struct filter_attribute src_port_attribute;
extern struct filter_attribute dst_port_attribute;