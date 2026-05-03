#pragma once

#include "common/container_of.h"

#include "filter/filter.h"

struct filter_query_attr_vlan {
	struct filter_query_attr attr;
	struct value_table value_table;
};

static inline void
filter_query_attr_vlan_free(
	struct memory_context *memory_context, struct filter_query_attr *attr
) {
	struct filter_query_attr_vlan *vlan_attr =
		container_of(attr, struct filter_query_attr_vlan, attr);

	value_table_free(&vlan_attr->value_table);
	memory_bfree(
		memory_context, vlan_attr, sizeof(struct filter_query_attr_vlan)
	);
}
