#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "filter/rule.h"

#include <stdint.h>

int
FILTER_ATTR_COMPILER_INIT_FUNC(vlan)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	struct value_table *t =
		memory_balloc(memory_context, sizeof(struct value_table));
	if (t == NULL) {
		return -1;
	}

	if (value_table_init(t, memory_context, 1, 4096)) {
		goto error_init;
	}
	SET_OFFSET_OF(data, t);

	struct remap_table remap_table;
	if (remap_table_init(&remap_table, memory_context, 4096)) {
		goto error_remap_table;
	}

	for (const struct filter_rule *r = rules; r < rules + rule_count; ++r) {
		if (r->vlan_range_count == 0) {
			continue;
		}
		remap_table_new_gen(&remap_table);
		for (uint32_t idx = 0; idx < r->vlan_range_count; ++idx) {
			for (uint16_t vlan = r->vlan_ranges[idx].from;
			     vlan <= r->vlan_ranges[idx].to;
			     ++vlan) {
				uint32_t *value =
					value_table_get_ptr(t, 0, vlan);
				if (remap_table_touch(
					    &remap_table, *value, value
				    ) < 0) {
					goto error_touch;
				}
			}
		}
	}

	remap_table_compact(&remap_table);
	value_table_compact(t, &remap_table);
	remap_table_free(&remap_table);

	for (const struct filter_rule *r = rules; r < rules + rule_count; ++r) {
		value_registry_start(registry);
		if (r->vlan_range_count == 0) {
			for (uint16_t vlan = 0; vlan <= 4095; ++vlan) {
				value_registry_collect(
					registry, value_table_get(t, 0, vlan)
				);
			}
		}
		for (uint32_t idx = 0; idx < r->vlan_range_count; ++idx) {
			for (uint16_t vlan = r->vlan_ranges[idx].from;
			     vlan <= r->vlan_ranges[idx].to;
			     ++vlan) {
				if (value_registry_collect(
					    registry,
					    value_table_get(t, 0, vlan)
				    )) {
					goto error_collect;
				}
			}
		}
	}
	return 0;

error_touch:
	remap_table_free(&remap_table);

error_collect:
error_remap_table:
	value_table_free(t);
	SET_OFFSET_OF(data, NULL);

error_init:
	memory_bfree(memory_context, t, sizeof(struct value_table));

	return -1;
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(vlan)(
	void *data, struct memory_context *memory_context
) {
	struct value_table *t = (struct value_table *)data;
	if (t == NULL)
		return;
	value_table_free(t);
	memory_bfree(memory_context, t, sizeof(struct value_table));
}
