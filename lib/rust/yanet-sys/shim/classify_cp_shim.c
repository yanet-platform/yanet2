#include "classify_shim.h"
#include "lib/classify/compiler/net4.h"
#include <stdlib.h>

void *
yanet_sys_classify_alloc(size_t count, size_t size) {
	return calloc(count, size);
}
void
yanet_sys_classify_free(void *allocation) {
	free(allocation);
}
void
yanet_sys_classify_stage_free(
	struct classifier *stage, struct memory_context *context, uint32_t count
) {
	classifier_fini(stage, context, count);
}
static void
net4_view(const struct classifier_rule *rule, struct filter_net4s *nets) {
	const struct yanet_sys_net4_input *input =
		(const struct yanet_sys_net4_input *)rule;
	*nets = input->nets;
}
int
yanet_sys_classify_net4_compile(
	struct memory_context *context,
	const struct classifier_rule *const *rules,
	uint32_t count,
	struct classify_attr_net4 *attr,
	struct classifier *stage
) {
	return classify_net4_compile(
		context, "net4", rules, count, net4_view, attr, stage
	);
}
void
yanet_sys_classify_net4_free(
	struct memory_context *context, struct classify_attr_net4 *attr
) {
	classify_attr_net4_free(context, attr);
}
void
yanet_sys_classify_joint_free(struct value_table *table) {
	value_table_free(table);
}
void
yanet_sys_classify_decode_free(struct vline *line) {
	vline_free(line);
}

#include "lib/classify/compiler/device.h"
#include "lib/classify/compiler/ipfrag.h"
#include "lib/classify/compiler/line.h"
#include "lib/classify/compiler/net6.h"
static void
net6_view(const struct classifier_rule *r, struct filter_net6s *v) {
	*v = ((const struct yanet_sys_net6_input *)r)->nets;
}
static void
byte_view(const struct classifier_rule *r, struct classify_line_ranges *v) {
	*v = ((const struct yanet_sys_byte_input *)r)->ranges;
}
static void
port_view(const struct classifier_rule *r, struct filter_u16_ranges *v) {
	*v = ((const struct yanet_sys_port_input *)r)->ranges;
}
static void
device_view(const struct classifier_rule *r, struct filter_devices *v) {
	*v = ((const struct yanet_sys_device_input *)r)->devices;
}
static enum filter_ip_fragment
fragment_view(const struct classifier_rule *r) {
	return ((const struct yanet_sys_fragment_input *)r)->fragment;
}
CLASSIFY_LINE_COMPILE(sdk_byte, struct classify_attr_line, 256, byte_view)
CLASSIFY_U16_RANGES_COMPILE(sdk_port, struct classify_attr_port, port_view)
int
yanet_sys_classify_net6_compile(
	struct memory_context *c,
	const struct classifier_rule *const *r,
	uint32_t n,
	struct classify_attr_net6 *a,
	struct classifier *s
) {
	return classify_net6_compile(c, "net6", r, n, net6_view, a, s);
}
int
yanet_sys_classify_device_compile(
	struct memory_context *c,
	const struct classifier_rule *const *r,
	uint32_t n,
	struct classify_attr_device *a,
	struct classifier *s
) {
	return classify_device_compile(c, "device", r, n, device_view, a, s);
}
int
yanet_sys_classify_fragment_compile(
	struct memory_context *c,
	const struct classifier_rule *const *r,
	uint32_t n,
	struct classify_attr_ipfrag *a,
	struct classifier *s
) {
	return classify_ipfrag_compile(
		c, "fragment", r, n, fragment_view, a, s
	);
}
int
yanet_sys_classify_byte_compile(
	struct memory_context *c,
	const struct classifier_rule *const *r,
	uint32_t n,
	struct classify_attr_line *a,
	struct classifier *s
) {
	return classify_sdk_byte_compile(
		c, "byte", (const struct classifier_rule **)r, n, a, s
	);
}
int
yanet_sys_classify_port_compile(
	struct memory_context *c,
	const struct classifier_rule *const *r,
	uint32_t n,
	struct classify_attr_port *a,
	struct classifier *s
) {
	return classify_sdk_port_compile(
		c, "port", (const struct classifier_rule **)r, n, a, s
	);
}
void
yanet_sys_classify_net6_free(
	struct memory_context *c, struct classify_attr_net6 *a
) {
	classify_attr_net6_free(c, a);
}
void
yanet_sys_classify_byte_free(
	struct memory_context *c, struct classify_attr_line *a
) {
	classify_attr_line_free(c, a);
}
void
yanet_sys_classify_port_free(
	struct memory_context *c, struct classify_attr_port *a
) {
	classify_attr_port_free(c, a);
}
void
yanet_sys_classify_device_free(
	struct memory_context *c, struct classify_attr_device *a
) {
	classify_attr_device_free(c, a);
}
void
yanet_sys_classify_fragment_free(
	struct memory_context *c, struct classify_attr_ipfrag *a
) {
	classify_attr_ipfrag_free(c, a);
}
void
yanet_sys_classify_rule_table_free(struct rule_table *t) {
	rule_table_free(t);
}
