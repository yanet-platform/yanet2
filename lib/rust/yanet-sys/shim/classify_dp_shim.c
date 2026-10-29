#include "classify_shim.h"
void
yanet_sys_classify_net4_lookup(
	const struct classify_attr_net4 *attr,
	const uint32_t *keys,
	uint32_t *classes,
	uint32_t count
) {
	classify_net4_lookup(attr, keys, classes, count);
}
void
yanet_sys_classify_joint_lookup(
	const struct value_table *table,
	const uint32_t *left,
	const uint32_t *right,
	uint32_t *classes,
	uint32_t count
) {
	classify_joint_lookup(table, left, right, classes, count);
}
void
yanet_sys_classify_decode_lookup(
	const struct vline *line,
	const uint32_t *classes,
	uint32_t *rules,
	uint32_t count
) {
	if (!line->size) {
		for (uint32_t i = 0; i < count; ++i) {
			rules[i] = UINT32_MAX;
		}
		return;
	}
	classify_resolve(line, classes, rules, count);
}

void
yanet_sys_classify_net6_lookup(
	const struct classify_attr_net6 *a,
	const uint8_t (*k)[16],
	uint32_t *v,
	uint32_t n
) {
	classify_net6_lookup(a, (const uint8_t *)k, v, n);
}
void
yanet_sys_classify_byte_lookup(
	const struct classify_attr_line *a,
	const uint8_t *k,
	uint32_t *v,
	uint32_t n
) {
	for (uint32_t i = 0; i < n; ++i) {
		v[i] = vline_get((struct vline *)&a->line, k[i]);
	}
}
void
yanet_sys_classify_port_lookup(
	const struct classify_attr_port *a,
	const uint16_t *k,
	uint32_t *v,
	uint32_t n
) {
	for (uint32_t i = 0; i < n; ++i) {
		v[i] = vline_get((struct vline *)&a->line, k[i]);
	}
}
void
yanet_sys_classify_fragment_lookup(
	const struct classify_attr_ipfrag *a,
	const uint8_t *k,
	uint32_t *v,
	uint32_t n
) {
	for (uint32_t i = 0; i < n; ++i) {
		v[i] = value_table_get(&a->value_table, 0, k[i]);
	}
}
uint32_t
yanet_sys_classify_device_lookup(
	const struct classify_attr_device *a, uint32_t k
) {
	if (k >= a->line.size) {
		k = 0;
	}
	return vline_get((struct vline *)&a->line, k);
}
void
yanet_sys_classify_joint_const_lookup(
	const struct value_table *t,
	uint32_t l,
	const uint32_t *r,
	uint32_t *v,
	uint32_t n
) {
	classify_joint_lookup_const(t, l, r, v, n);
}
void
yanet_sys_classify_rule_table_lookup(
	const struct rule_table *t,
	const uint32_t *l,
	const uint32_t *r,
	uint32_t *v,
	uint32_t n
) {
	if (!t->v_dim) {
		for (uint32_t i = 0; i < n; ++i) {
			v[i] = UINT32_MAX;
		}
		return;
	}
	classify_rule_table_lookup(t, l, r, v, n);
}
