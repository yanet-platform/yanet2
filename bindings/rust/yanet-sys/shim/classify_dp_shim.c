#include "classify_shim.h"

#include "common/value.h"

#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/query.h"
#include "lib/classify/rule_table.h"

_Static_assert(
	YANET_SYS_CLASSIFY_NO_RULE == CLASSIFY_RULE_INVALID,
	"the shim reports no match as the library does"
);

// The class a joint holds for a pair, class zero for a pair outside it.
static inline uint32_t
joint_get(const struct value_table *joint, uint32_t left, uint32_t right) {
	if (left >= joint->v_dim || right >= joint->h_dim) {
		return 0;
	}
	return value_table_get(joint, left, right);
}

void
yanet_sys_classify_net4_lookup(
	const struct classify_attr_net4 *attr,
	const uint32_t *addrs,
	uint32_t *classes,
	uint32_t count
) {
	classify_net4_lookup(attr, addrs, classes, count);
}

void
yanet_sys_classify_net6_lookup(
	const struct classify_attr_net6 *attr,
	const uint8_t *addrs,
	uint32_t *classes,
	uint32_t count
) {
	classify_net6_lookup(attr, addrs, classes, count);
}

void
yanet_sys_classify_line_lookup(
	const struct classify_attr_line *attr,
	const uint8_t *values,
	uint32_t *classes,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		classes[idx] =
			vline_get((struct vline *)&attr->line, values[idx]);
	}
}

void
yanet_sys_classify_port_lookup(
	const struct classify_attr_port *attr,
	const uint16_t *ports,
	uint32_t *classes,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		classes[idx] =
			vline_get((struct vline *)&attr->line, ports[idx]);
	}
}

void
yanet_sys_classify_joint_lookup(
	const struct value_table *joint,
	const uint32_t *left,
	const uint32_t *right,
	uint32_t *classes,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		classes[idx] = joint_get(joint, left[idx], right[idx]);
	}
}

void
yanet_sys_classify_rule_table_lookup(
	const struct rule_table *table,
	const uint32_t *left,
	const uint32_t *right,
	uint32_t *rules,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		rules[idx] =
			left[idx] < table->v_dim && right[idx] < table->h_dim
				? rule_table_get(table, left[idx], right[idx])
				: CLASSIFY_RULE_INVALID;
	}
}

void
yanet_sys_classify_resolve(
	const struct vline *rule_map,
	const uint32_t *classes,
	uint32_t *rules,
	uint32_t count
) {
	for (uint32_t idx = 0; idx < count; ++idx) {
		rules[idx] =
			classes[idx] < rule_map->size
				? vline_get(
					  (struct vline *)rule_map, classes[idx]
				  )
				: CLASSIFY_RULE_INVALID;
	}
}

void
yanet_sys_classify_results_min(
	uint32_t *rules, const uint32_t *extra, uint32_t count
) {
	classify_results_min(rules, extra, count);
}

uint32_t
yanet_sys_classify_net4_class(
	const struct classify_attr_net4 *attr, uint32_t addr
) {
	return lpm4_lookup(&attr->lpm, (const uint8_t *)&addr);
}

// Two LPM walks are past the inliner's budget, so the IPv6 lookup forces its
// own inlining where its bitcode meets the Rust that calls it, and flattens
// the batch lookup of one address into itself. gcc, which builds the shim as
// machine code only, rejects the former on a function of external linkage.
#ifdef __clang__
#define YANET_SYS_INLINE_ACROSS __attribute__((always_inline))
#else
#define YANET_SYS_INLINE_ACROSS
#endif

YANET_SYS_INLINE_ACROSS __attribute__((flatten)) uint32_t
yanet_sys_classify_net6_class(
	const struct classify_attr_net6 *attr, const uint8_t *addr
) {
	uint32_t class;
	classify_net6_lookup(attr, addr, &class, 1);
	return class;
}

uint32_t
yanet_sys_classify_line_class(
	const struct classify_attr_line *attr, uint8_t value
) {
	return vline_get((struct vline *)&attr->line, value);
}

uint32_t
yanet_sys_classify_port_class(
	const struct classify_attr_port *attr, uint16_t port
) {
	return vline_get((struct vline *)&attr->line, port);
}

uint32_t
yanet_sys_classify_joint_class(
	const struct value_table *joint, uint32_t left, uint32_t right
) {
	return value_table_get(joint, left, right);
}

uint32_t
yanet_sys_classify_rule_table_rule(
	const struct rule_table *table, uint32_t left, uint32_t right
) {
	return rule_table_get(table, left, right);
}

uint32_t
yanet_sys_classify_rule_map_rule(const struct vline *rule_map, uint32_t class) {
	return vline_get((struct vline *)rule_map, class);
}
