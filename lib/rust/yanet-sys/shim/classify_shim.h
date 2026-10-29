// Primitive native classifier entrypoints over prepared borrowed views.
#pragma once
#include "common/filter_views.h"
#include "lib/classify/classifiers/device.h"
#include "lib/classify/classifiers/ipfrag.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/classify.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/u16_ranges.h"
#include "lib/classify/query.h"
#include "shim_visibility.h"

struct yanet_sys_net4_input {
	struct classifier_rule base;
	struct filter_net4s nets;
};
YANET_SYS_HIDDEN void *
yanet_sys_classify_alloc(size_t count, size_t size);
YANET_SYS_HIDDEN void
yanet_sys_classify_free(void *allocation);
YANET_SYS_HIDDEN void
yanet_sys_classify_stage_free(
	struct classifier *stage, struct memory_context *context, uint32_t count
);
YANET_SYS_HIDDEN int
yanet_sys_classify_net4_compile(
	struct memory_context *context,
	const struct classifier_rule *const *rules,
	uint32_t count,
	struct classify_attr_net4 *attr,
	struct classifier *stage
);
YANET_SYS_HIDDEN void
yanet_sys_classify_net4_free(
	struct memory_context *context, struct classify_attr_net4 *attr
);
YANET_SYS_HIDDEN void
yanet_sys_classify_joint_free(struct value_table *table);
YANET_SYS_HIDDEN void
yanet_sys_classify_decode_free(struct vline *line);
YANET_SYS_HIDDEN void
yanet_sys_classify_net4_lookup(
	const struct classify_attr_net4 *attr,
	const uint32_t *keys,
	uint32_t *classes,
	uint32_t count
);
YANET_SYS_HIDDEN void
yanet_sys_classify_joint_lookup(
	const struct value_table *table,
	const uint32_t *left,
	const uint32_t *right,
	uint32_t *classes,
	uint32_t count
);
YANET_SYS_HIDDEN void
yanet_sys_classify_decode_lookup(
	const struct vline *line,
	const uint32_t *classes,
	uint32_t *rules,
	uint32_t count
);

struct yanet_sys_net6_input {
	struct classifier_rule base;
	struct filter_net6s nets;
};
struct yanet_sys_byte_input {
	struct classifier_rule base;
	struct classify_line_ranges ranges;
};
struct yanet_sys_port_input {
	struct classifier_rule base;
	struct filter_u16_ranges ranges;
};
struct yanet_sys_device_input {
	struct classifier_rule base;
	struct filter_devices devices;
};
struct yanet_sys_fragment_input {
	struct classifier_rule base;
	enum filter_ip_fragment fragment;
};
YANET_SYS_HIDDEN int
yanet_sys_classify_net6_compile(struct memory_context *, const struct classifier_rule *const *, uint32_t, struct classify_attr_net6 *, struct classifier *);
YANET_SYS_HIDDEN void
yanet_sys_classify_net6_free(struct memory_context *, struct classify_attr_net6 *);
YANET_SYS_HIDDEN int
yanet_sys_classify_byte_compile(struct memory_context *, const struct classifier_rule *const *, uint32_t, struct classify_attr_line *, struct classifier *);
YANET_SYS_HIDDEN void
yanet_sys_classify_byte_free(struct memory_context *, struct classify_attr_line *);
YANET_SYS_HIDDEN int
yanet_sys_classify_port_compile(struct memory_context *, const struct classifier_rule *const *, uint32_t, struct classify_attr_port *, struct classifier *);
YANET_SYS_HIDDEN void
yanet_sys_classify_port_free(struct memory_context *, struct classify_attr_port *);
YANET_SYS_HIDDEN int
yanet_sys_classify_device_compile(struct memory_context *, const struct classifier_rule *const *, uint32_t, struct classify_attr_device *, struct classifier *);
YANET_SYS_HIDDEN void
yanet_sys_classify_device_free(struct memory_context *, struct classify_attr_device *);
YANET_SYS_HIDDEN int
yanet_sys_classify_fragment_compile(struct memory_context *, const struct classifier_rule *const *, uint32_t, struct classify_attr_ipfrag *, struct classifier *);
YANET_SYS_HIDDEN void
yanet_sys_classify_fragment_free(struct memory_context *, struct classify_attr_ipfrag *);
YANET_SYS_HIDDEN void
yanet_sys_classify_rule_table_free(struct rule_table *);
YANET_SYS_HIDDEN void
yanet_sys_classify_net6_lookup(
	const struct classify_attr_net6 *,
	const uint8_t (*)[16],
	uint32_t *,
	uint32_t
);
YANET_SYS_HIDDEN void
yanet_sys_classify_byte_lookup(
	const struct classify_attr_line *, const uint8_t *, uint32_t *, uint32_t
);
YANET_SYS_HIDDEN void
yanet_sys_classify_port_lookup(
	const struct classify_attr_port *,
	const uint16_t *,
	uint32_t *,
	uint32_t
);
YANET_SYS_HIDDEN void
yanet_sys_classify_fragment_lookup(
	const struct classify_attr_ipfrag *,
	const uint8_t *,
	uint32_t *,
	uint32_t
);
YANET_SYS_HIDDEN uint32_t
yanet_sys_classify_device_lookup(const struct classify_attr_device *, uint32_t);
YANET_SYS_HIDDEN void
yanet_sys_classify_joint_const_lookup(
	const struct value_table *,
	uint32_t,
	const uint32_t *,
	uint32_t *,
	uint32_t
);
YANET_SYS_HIDDEN void
yanet_sys_classify_rule_table_lookup(
	const struct rule_table *,
	const uint32_t *,
	const uint32_t *,
	uint32_t *,
	uint32_t
);
