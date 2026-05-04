#include "common/container_of.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "filter/rule.h"

#include "filter/classifiers/device.h"

#include <stdint.h>

struct filter_compile_device_attr {
	struct filter_compile_attr attr;
	struct filter_query_attr_device *query_attr;
};

typedef void (*filter_rule_get_device_func)(
	const struct filter_rule *filter_rule, struct filter_devices *devices
);

struct filter_compile_attr_device_handlers {
	struct filter_compile_attr_handlers attr_handlers;
	filter_rule_get_device_func get_devices;
};

static inline struct filter_compile_attr *
filter_compile_attr_device_create(
	struct memory_context *memory_context,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
) {
	struct filter_compile_attr_device_handlers *device_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_device_handlers,
			attr_handlers
		);

	struct filter_compile_device_attr *attr =
		(struct filter_compile_device_attr *)memory_balloc(
			memory_context,
			sizeof(struct filter_compile_device_attr)
		);
	if (attr == NULL) {
		return NULL;
	}

	attr->query_attr = (struct filter_query_attr_device *)memory_balloc(
		memory_context, sizeof(struct filter_query_attr_device)
	);

	if (attr->query_attr == NULL)
		goto error_free;

	uint32_t max_device_id = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL)
			continue;

		struct filter_devices devices;
		device_handlers->get_devices(rule, &devices);

		for (uint32_t idx = 0; idx < devices.count; ++idx) {
			if (devices.items[idx].id > max_device_id) {
				max_device_id = devices.items[idx].id;
			}
		}
	}

	if (value_table_init(
		    &attr->query_attr->value_table,
		    memory_context,
		    1,
		    max_device_id + 1
	    )) {
		goto error_free_attr;
	}

	return &attr->attr;

error_free_attr:
	memory_bfree(
		memory_context,
		attr->query_attr,
		sizeof(struct filter_query_attr_device)
	);

error_free:
	memory_bfree(
		memory_context, attr, sizeof(struct filter_compile_device_attr)
	);

	return NULL;
}

static inline uint32_t
filter_compile_attr_device_size(const struct filter_compile_attr *attr) {
	struct filter_compile_device_attr *device_attr =
		container_of(attr, struct filter_compile_device_attr, attr);

	return device_attr->query_attr->value_table.h_dim *
	       device_attr->query_attr->value_table.v_dim;
}

static inline int
filter_compile_attr_device_iter(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	(void)attr_handlers;

	struct filter_compile_device_attr *device_attr =
		container_of(attr, struct filter_compile_device_attr, attr);

	struct filter_query_attr_device *query_attr = device_attr->query_attr;

	for (uint32_t h_idx = 0; h_idx < query_attr->value_table.h_dim;
	     ++h_idx) {
		if (iter_cb_func(
			    value_table_get_ptr(
				    &query_attr->value_table, 0, h_idx
			    ),
			    cb_func_data
		    )) {
			return -1;
		}
	}

	return 0;
}

static inline int
filter_compile_attr_device_rule_is_any(
	const struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {
	struct filter_compile_attr_device_handlers *device_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_device_handlers,
			attr_handlers
		);

	(void)attr;

	struct filter_devices devices;
	device_handlers->get_devices(rule, &devices);
	return devices.count == 0;
}

static inline int
filter_compile_attr_device_rule_iter(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	struct filter_compile_attr_device_handlers *device_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_device_handlers,
			attr_handlers
		);

	struct filter_compile_device_attr *device_attr =
		container_of(attr, struct filter_compile_device_attr, attr);

	struct filter_devices devices;
	device_handlers->get_devices(rule, &devices);

	struct filter_query_attr_device *query_attr = device_attr->query_attr;

	for (uint32_t idx = 0; idx < devices.count; ++idx) {
		if (iter_cb_func(
			    value_table_get_ptr(
				    &query_attr->value_table,
				    0,
				    devices.items[idx].id
			    ),
			    cb_func_data
		    )) {
			return -1;
		}
	}

	return 0;
}

static inline void
filter_compile_attr_device_free(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_device_attr *device_attr =
		container_of(attr, struct filter_compile_device_attr, attr);

	if (device_attr->query_attr != NULL) {
		value_table_free(&device_attr->query_attr->value_table);
		memory_bfree(
			memory_context,
			device_attr->query_attr,
			sizeof(struct filter_query_attr_device)
		);
	}

	memory_bfree(
		memory_context,
		device_attr,
		sizeof(struct filter_compile_device_attr)
	);
}

static inline struct filter_query_attr *
filter_compile_attr_device_commit(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_device_attr *device_attr =
		container_of(attr, struct filter_compile_device_attr, attr);

	struct filter_query_attr_device *query_attr = device_attr->query_attr;

	device_attr->query_attr = NULL;
	filter_compile_attr_device_free(memory_context, attr);

	return &query_attr->attr;
}

static const struct filter_compile_attr_handlers filter_compile_get_devices = {
	.create = filter_compile_attr_device_create,
	.size = filter_compile_attr_device_size,
	.iter = filter_compile_attr_device_iter,
	.rule_iter = filter_compile_attr_device_rule_iter,
	.rule_is_any = filter_compile_attr_device_rule_is_any,
	.commit = filter_compile_attr_device_commit,
	.free_compile = filter_compile_attr_device_free,
	.free_query = filter_query_attr_device_free,
};

static inline void
filter_rule_get_devices(
	const struct filter_rule *rule, struct filter_devices *devices
) {
	devices->count = rule->device_count;
	devices->items = rule->devices;
}

static const struct filter_compile_attr_device_handlers
	filter_compile_attr_device = {
		.attr_handlers = filter_compile_get_devices,
		.get_devices = filter_rule_get_devices,
};

int
FILTER_ATTR_COMPILER_INIT_FUNC(device)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_device.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(device)(
	void *data, struct memory_context *memory_context
) {
	struct filter_query_attr_device *attr =
		(struct filter_query_attr_device *)data;
	if (attr == NULL)
		return;

	filter_query_attr_device_free(memory_context, &attr->attr);
}
