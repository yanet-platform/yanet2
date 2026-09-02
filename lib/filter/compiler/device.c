#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "lib/filter/rule.h"

#include <stdint.h>

int
FILTER_ATTR_COMPILER_INIT_FUNC(device)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	uint64_t max_device_id = 0;
	for (const struct filter_rule **r_ptr = rules;
	     r_ptr < rules + rule_count;
	     ++r_ptr) {
		if (*r_ptr == NULL) {
			continue;
		}
		const struct filter_rule *r = *r_ptr;

		for (uint16_t idx = 0; idx < r->device_count; ++idx) {
			if (r->devices[idx].id > max_device_id) {
				max_device_id = r->devices[idx].id;
			}
		}
	}

	struct vline *l = memory_balloc(memory_context, sizeof(struct vline));
	if (l == NULL) {
		return -1;
	}
	int res = vline_init(l, memory_context, "device", max_device_id + 1);
	if (res < 0) {
		goto error_init;
	}
	SET_OFFSET_OF(data, l);

	struct remap_table remap_table;
	if (remap_table_init(&remap_table, memory_context, max_device_id + 1)) {
		goto error_remap_table;
	}

	for (const struct filter_rule **r_ptr = rules;
	     r_ptr < rules + rule_count;
	     ++r_ptr) {
		if (*r_ptr == NULL) {
			continue;
		}
		const struct filter_rule *r = *r_ptr;

		if (r->device_count == 0) {
			continue;
		}
		remap_table_new_gen(&remap_table);
		for (uint16_t idx = 0; idx < r->device_count; ++idx) {
			uint32_t *value = vline_get_ptr(l, r->devices[idx].id);
			if (remap_table_touch(&remap_table, *value, value) <
			    0) {
				goto error_touch;
			}
		}
	}

	remap_table_compact(&remap_table);
	vline_compact(l, &remap_table);
	remap_table_free(&remap_table);

	for (const struct filter_rule **r_ptr = rules;
	     r_ptr < rules + rule_count;
	     ++r_ptr) {
		if (value_registry_start(registry)) {
			goto error_collect;
		}

		if (*r_ptr == NULL) {
			continue;
		}
		const struct filter_rule *r = *r_ptr;

		if (r->device_count == 0) {
			for (uint64_t id = 0; id < max_device_id + 1; ++id) {
				if (value_registry_collect(
					    registry, vline_get(l, id)
				    )) {
					goto error_collect;
				}
			}
		} else {
			for (uint16_t idx = 0; idx < r->device_count; ++idx) {
				if (value_registry_collect(
					    registry,
					    vline_get(l, r->devices[idx].id)
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
	vline_free(l);
	SET_OFFSET_OF(data, NULL);

error_init:
	memory_bfree(memory_context, l, sizeof(struct vline));

	return -1;
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(device)(
	void *data, struct memory_context *memory_context
) {
	struct vline *l = (struct vline *)data;
	if (l == NULL) {
		return;
	}
	vline_free(l);
	memory_bfree(memory_context, l, sizeof(struct vline));
}
