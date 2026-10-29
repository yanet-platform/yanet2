#include "classify_shim.h"

#include "common/value.h"

#include "lib/classify/classifiers/device.h"
#include "lib/classify/classifiers/ipfrag.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/compiler/device.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/ipfrag.h"
#include "lib/classify/compiler/line.h"
#include "lib/classify/compiler/net4.h"
#include "lib/classify/compiler/net6.h"
#include "lib/classify/compiler/u16_ranges.h"
#include "lib/classify/rule_table.h"

// The getters of the attribute compiles: a rule is the address of the view
// the compile reads, so each getter hands out the view itself.
static void
get_devices(const struct classifier_rule *rule, struct filter_devices *view) {
	*view = *(const struct filter_devices *)rule;
}

static void
get_net4s(const struct classifier_rule *rule, struct filter_net4s *view) {
	*view = *(const struct filter_net4s *)rule;
}

static void
get_net6s(const struct classifier_rule *rule, struct filter_net6s *view) {
	*view = *(const struct filter_net6s *)rule;
}

static void
get_lines(
	const struct classifier_rule *rule, struct classify_line_ranges *view
) {
	*view = *(const struct classify_line_ranges *)rule;
}

static void
get_ports(const struct classifier_rule *rule, struct filter_u16_ranges *view) {
	const struct filter_port_ranges *ports =
		(const struct filter_port_ranges *)rule;
	view->count = ports->count;
	view->items = (const struct filter_u16_span *)ports->items;
}

static enum filter_ip_fragment
get_fragment(const struct classifier_rule *rule) {
	return *(const enum filter_ip_fragment *)rule;
}

_Static_assert(
	sizeof(struct filter_port_range) == sizeof(struct filter_u16_span),
	"a port range is read as the span of the u16 compile"
);

CLASSIFY_LINE_COMPILE(
	yanet_sys_line, struct classify_attr_line, 0x100, get_lines
)
CLASSIFY_U16_RANGES_COMPILE(
	yanet_sys_port, struct classify_attr_port, get_ports
)

int
yanet_sys_classify_device_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct filter_devices *const *rules,
	uint32_t rule_count,
	struct classify_attr_device *attr,
	struct classifier *stage
) {
	return classify_device_compile(
		memory_context,
		name,
		(const struct classifier_rule *const *)rules,
		rule_count,
		get_devices,
		attr,
		stage
	);
}

int
yanet_sys_classify_net4_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct filter_net4s *const *rules,
	uint32_t rule_count,
	struct classify_attr_net4 *attr,
	struct classifier *stage
) {
	return classify_net4_compile(
		memory_context,
		name,
		(const struct classifier_rule *const *)rules,
		rule_count,
		get_net4s,
		attr,
		stage
	);
}

int
yanet_sys_classify_net6_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct filter_net6s *const *rules,
	uint32_t rule_count,
	struct classify_attr_net6 *attr,
	struct classifier *stage
) {
	return classify_net6_compile(
		memory_context,
		name,
		(const struct classifier_rule *const *)rules,
		rule_count,
		get_net6s,
		attr,
		stage
	);
}

int
yanet_sys_classify_line_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct classify_line_ranges *const *rules,
	uint32_t rule_count,
	struct classify_attr_line *attr,
	struct classifier *stage
) {
	return classify_yanet_sys_line_compile(
		memory_context,
		name,
		(const struct classifier_rule **)rules,
		rule_count,
		attr,
		stage
	);
}

int
yanet_sys_classify_port_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct filter_port_ranges *const *rules,
	uint32_t rule_count,
	struct classify_attr_port *attr,
	struct classifier *stage
) {
	return classify_yanet_sys_port_compile(
		memory_context,
		name,
		(const struct classifier_rule **)rules,
		rule_count,
		attr,
		stage
	);
}

int
yanet_sys_classify_ipfrag_compile(
	struct memory_context *memory_context,
	const char *name,
	const enum filter_ip_fragment *const *rules,
	uint32_t rule_count,
	struct classify_attr_ipfrag *attr,
	struct classifier *stage
) {
	return classify_ipfrag_compile(
		memory_context,
		name,
		(const struct classifier_rule *const *)rules,
		rule_count,
		get_fragment,
		attr,
		stage
	);
}

void
yanet_sys_classify_stage_free(
	struct classifier *stage,
	struct memory_context *memory_context,
	uint32_t rule_count
) {
	classifier_fini(stage, memory_context, rule_count);
}

int
yanet_sys_classify_join(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *left,
	const struct classifier *right,
	uint32_t rule_count,
	struct value_table *joint,
	struct classifier *stage
) {
	return classify_join(
		memory_context, name, left, right, rule_count, joint, stage
	);
}

int
yanet_sys_classify_join_rules(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *left,
	const struct classifier *right,
	const void *const *rules,
	uint32_t rule_count,
	struct rule_table *table
) {
	return classify_join_rules(
		memory_context,
		name,
		left,
		right,
		(const struct classifier_rule *const *)rules,
		rule_count,
		table
	);
}

int
yanet_sys_classify_decode(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *stage,
	const void *const *rules,
	uint32_t rule_count,
	struct vline *rule_map
) {
	return classify_decode(
		memory_context,
		name,
		stage,
		(const struct classifier_rule *const *)rules,
		rule_count,
		rule_map
	);
}

void
yanet_sys_classify_device_free(struct classify_attr_device *attr) {
	classify_attr_device_free(NULL, attr);
}

void
yanet_sys_classify_net4_free(struct classify_attr_net4 *attr) {
	classify_attr_net4_free(NULL, attr);
}

void
yanet_sys_classify_net6_free(struct classify_attr_net6 *attr) {
	classify_attr_net6_free(NULL, attr);
}

void
yanet_sys_classify_line_free(struct classify_attr_line *attr) {
	classify_attr_line_free(NULL, attr);
}

void
yanet_sys_classify_port_free(struct classify_attr_port *attr) {
	classify_attr_port_free(NULL, attr);
}

void
yanet_sys_classify_ipfrag_free(struct classify_attr_ipfrag *attr) {
	classify_attr_ipfrag_free(NULL, attr);
}

void
yanet_sys_classify_joint_free(struct value_table *joint) {
	value_table_free(joint);
}

void
yanet_sys_classify_rule_table_free(struct rule_table *table) {
	rule_table_free(table);
}

void
yanet_sys_classify_rule_map_free(struct vline *rule_map) {
	vline_free(rule_map);
}
