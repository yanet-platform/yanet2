#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "lib/classify/classifiers/device.h"
#include "lib/classify/rule.h"

#include <stdint.h>
#include <string.h>

/*
 * Getter of the device set of a module rule, filled by the module and
 * adapted to the rule agnostic core through the compile macro below.
 */
typedef void (*classify_device_get_devices_func)(
	const struct classifier_rule *rule, struct filter_devices *devices
);

/*
 * Compile scratch of the device attribute: the class table over the
 * device identifiers of the ruleset.
 */
struct classify_compile_device {
	classify_device_get_devices_func get_devices;
	struct value_table value_table;
};

static inline struct classify_compile_device *
classify_compile_device_create(
	struct memory_context *memory_context,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_device_get_devices_func get_devices
) {
	struct classify_compile_device *compile =
		(struct classify_compile_device *)memory_balloc(
			memory_context, sizeof(struct classify_compile_device)
		);
	if (compile == NULL) {
		return NULL;
	}

	compile->get_devices = get_devices;

	uint32_t max_device_id = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct classifier_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		struct filter_devices devices;
		get_devices(rule, &devices);

		for (uint32_t idx = 0; idx < devices.count; ++idx) {
			if (devices.items[idx].id > max_device_id) {
				max_device_id = devices.items[idx].id;
			}
		}
	}

	if (value_table_init(
		    &compile->value_table,
		    memory_context,
		    "filter:device",
		    1,
		    max_device_id + 1
	    )) {
		memory_bfree(
			memory_context,
			compile,
			sizeof(struct classify_compile_device)
		);

		return NULL;
	}

	return compile;
}

static inline uint32_t
classify_compile_device_size(const void *compile) {
	const struct classify_compile_device *device_compile = compile;

	return device_compile->value_table.h_dim *
	       device_compile->value_table.v_dim;
}

static inline int
classify_compile_device_iter(
	void *compile,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_device *device_compile = compile;

	for (uint32_t h_idx = 0; h_idx < device_compile->value_table.h_dim;
	     ++h_idx) {
		if (iter_cb_func(
			    value_table_get_ptr(
				    &device_compile->value_table, 0, h_idx
			    ),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
	}

	return 0;
}

static inline int
classify_compile_device_rule_is_any(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_device *device_compile = compile;

	struct filter_devices devices;
	device_compile->get_devices(rule, &devices);
	return devices.count == 0;
}

static inline int
classify_compile_device_rule_iter(
	void *compile,
	const struct classifier_rule *rule,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_device *device_compile = compile;

	struct filter_devices devices;
	device_compile->get_devices(rule, &devices);

	for (uint32_t idx = 0; idx < devices.count; ++idx) {
		if (iter_cb_func(
			    value_table_get_ptr(
				    &device_compile->value_table,
				    0,
				    devices.items[idx].id
			    ),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
	}

	return 0;
}

static inline void
classify_compile_device_free(
	struct memory_context *memory_context, void *compile
) {
	struct classify_compile_device *device_compile = compile;

	value_table_free(&device_compile->value_table);

	memory_bfree(
		memory_context,
		device_compile,
		sizeof(struct classify_compile_device)
	);
}

static inline int
classify_compile_device_commit(
	struct memory_context *memory_context, void *compile, void *attr
) {
	struct classify_compile_device *device_compile = compile;
	struct classify_attr_device *device_attr = attr;

	/*
	 * The classes are committed into a value line indexed by the raw
	 * device identifier, one flat read on the query path.
	 */
	if (vline_init(
		    &device_attr->line,
		    memory_context,
		    "filter:device",
		    device_compile->value_table.h_dim
	    )) {
		memset(device_attr, 0, sizeof(*device_attr));
		return -1;
	}

	for (uint32_t idx = 0; idx < device_compile->value_table.h_dim; ++idx) {
		*vline_get_ptr(&device_attr->line, idx) =
			value_table_get(&device_compile->value_table, 0, idx);
	}

	classify_compile_device_free(memory_context, compile);

	return 0;
}

static inline uint32_t
classify_compile_device_hash(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_device *device_compile = compile;

	struct filter_devices devices;
	device_compile->get_devices(rule, &devices);

	uint32_t hash = devices.count;
	for (uint32_t idx = 0; idx < devices.count; ++idx) {
		hash = hash * 31 + devices.items[idx].id;
	}
	return hash;
}

static inline int
classify_compile_device_compare(
	const void *compile,
	const struct classifier_rule *first,
	const struct classifier_rule *second
) {
	const struct classify_compile_device *device_compile = compile;

	struct filter_devices first_devices;
	struct filter_devices second_devices;
	device_compile->get_devices(first, &first_devices);
	device_compile->get_devices(second, &second_devices);

	if (first_devices.count != second_devices.count) {
		return 1;
	}

	for (uint32_t idx = 0; idx < first_devices.count; ++idx) {
		if (first_devices.items[idx].id !=
		    second_devices.items[idx].id) {
			return 1;
		}
	}

	return 0;
}

/*
 * Compiles the device classifier of a ruleset into an embedded
 * attribute.
 *
 * On success the attribute and the stage - the registry with the rule
 * group row - are owned by the caller, released through
 * classifier_fini; on failure every partial state is freed, the
 * attribute and the stage are left zeroed.
 */
static inline int
classify_device_compile(
	struct memory_context *memory_context,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_device_get_devices_func get_devices,
	struct classify_attr_device *attr,
	struct classifier *cls
) {
	struct classify_compile_device *compile =
		classify_compile_device_create(
			memory_context, rules, rule_count, get_devices
		);
	if (compile == NULL) {
		memset(attr, 0, sizeof(*attr));
		memset(cls, 0, sizeof(*cls));
		return -1;
	}

	const struct classify_attr_ops ops = {
		.size = classify_compile_device_size,
		.rule_is_any = classify_compile_device_rule_is_any,
		.hash = classify_compile_device_hash,
		.compare = classify_compile_device_compare,
		.iter = classify_compile_device_iter,
		.rule_iter = classify_compile_device_rule_iter,
		.commit = classify_compile_device_commit,
		.free_compile = classify_compile_device_free,
	};

	return classify_attr_compile(
		memory_context, &ops, compile, rules, rule_count, attr, cls
	);
}

/*
 * Declares the device compile entry of one consumer: the getter adapts
 * the module rule type to the rule agnostic core, and the entry takes
 * the module rule array the consumer owns.
 *
 * The name must carry the consumer prefix, so the generated symbols
 * never collide with the library ones.
 */
#define CLASSIFY_DEVICE_COMPILE(name, get_devices)                             \
	static inline int classify_##name##_compile(                           \
		struct memory_context *memory_context,                         \
		const struct classifier_rule **rules,                          \
		uint32_t rule_count,                                           \
		struct classify_attr_device *attr,                             \
		struct classifier *cls                                         \
	) {                                                                    \
		return classify_device_compile(                                \
			memory_context,                                        \
			rules,                                                 \
			rule_count,                                            \
			get_devices,                                           \
			attr,                                                  \
			cls                                                    \
		);                                                             \
	}
