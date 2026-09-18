#pragma once

#include "common/container_of.h"

#include "lib/classify/classify.h"

struct classify_query_attr_vlan {
	struct classify_query_attr attr;
	struct value_table value_table;
};

static inline void
classify_query_attr_vlan_free(
	struct memory_context *memory_context, struct classify_query_attr *attr
) {
	struct classify_query_attr_vlan *vlan_attr =
		container_of(attr, struct classify_query_attr_vlan, attr);

	value_table_free(&vlan_attr->value_table);
	memory_bfree(
		memory_context, vlan_attr, sizeof(struct classify_query_attr_vlan)
	);
}
