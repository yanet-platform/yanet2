#pragma once

#include "common/registry.h"
#include "attribute.h"

struct filter_vertex {
    struct value_registry registry;
    struct value_table table;
    int32_t slots[2];
};

struct filter_leaf {
    attr_init_func init_func;
    attr_lookup_func lookup_func;
    struct value_registry registry;
    void *data;
};

struct filter {
    struct filter_vertex vertices[MAX_ATTRIBUTES];
    struct filter_leaf leaves[MAX_ATTRIBUTES];
    struct memory_context memory_context;
    uint32_t attributes_count;
};

int filter_init(
    struct filter *filter,
    const struct filter_attribute *attributes,
    uint32_t attributes_count,
    const struct filter_action *actions,
	uint32_t actions_count,
    struct memory_context *memory_context
);

int filter_query(
    struct filter *filter,
    struct packet_info packet_view,
	uint32_t **actions,
	uint32_t *count
);