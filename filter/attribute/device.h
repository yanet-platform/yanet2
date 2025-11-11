#include "../helper.h"
#include "../rule.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "dataplane/packet/packet.h"

#include <stdint.h>

static inline int
init_device(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	uint64_t max_device_id = 0;
	for (const struct filter_rule *r = rules; r < rules + rule_count; ++r) {
		for (uint16_t idx = 0; idx < r->device_count; ++idx) {
			if (r->devices[idx].id > max_device_id) {
				max_device_id = r->devices[idx].id;
			}
		}
	}

	struct value_table *t =
		memory_balloc(memory_context, sizeof(struct value_table));
	if (t == NULL) {
		return -1;
	}
	int res = value_table_init(t, memory_context, 1, max_device_id + 1);
	if (res < 0) {
		goto error_init;
	}
	// FIXME: handle errors
	SET_OFFSET_OF(data, t);
	for (const struct filter_rule *r = rules; r < rules + rule_count; ++r) {
		if (r->device_count == 0) {
			continue;
		}
		value_table_new_gen(t);
		for (uint16_t idx = 0; idx < r->device_count; ++idx) {
			value_table_touch(t, 0, r->devices[idx].id);
		}
	}
	value_table_compact(t);

	for (const struct filter_rule *r = rules; r < rules + rule_count; ++r) {
		value_registry_start(registry);
		if (r->device_count == 0) {
			for (uint64_t id = 0; id < max_device_id + 1; ++id) {
				value_registry_collect(
					registry, value_table_get(t, 0, id)
				);
			}
		} else {
			for (uint16_t idx = 0; idx < r->device_count; ++idx) {
				value_registry_collect(
					registry,
					value_table_get(
						t, 0, r->devices[idx].id
					)
				);
			}
		}
	}
	return 0;

error_init:
	memory_bfree(memory_context, t, sizeof(struct value_table));

	return -1;
}

static inline uint32_t
lookup_device(struct packet *packet, void *data) {
	struct value_table *t = (struct value_table *)data;
	uint64_t device_id = packet->module_device_id;
	return value_table_get(t, 0, device_id);
}

static inline void
free_device(void *data, struct memory_context *memory_context) {
	struct value_table *t = (struct value_table *)data;
	value_table_free(t);
	memory_bfree(memory_context, t, sizeof(struct value_table));
}
