// Smoke test for the region-based classification library.
//
// Instantiates the compile entries of the device, vlan, ipfrag, net4,
// net6 and u16 range attributes over a smoke rule type with module
// style field selectors, so the per-attribute machinery (static
// inline in the headers) is instantiated and compile-checked on the
// compile path, and the join helpers of the query header on the lookup
// path. This does not run classification; that needs the
// shared-memory test harness and lives in the other tests.

#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/classify.h"
#include "lib/classify/compiler/device.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/ipfrag.h"
#include "lib/classify/compiler/line.h"
#include "lib/classify/compiler/net4.h"
#include "lib/classify/compiler/net6.h"
#include "lib/classify/compiler/u16_ranges.h"
#include "lib/classify/query.h"
#include "lib/classify/rule.h"

#include "common/container_of.h"

#include <stdint.h>

// The smoke rule: the classifier base first, then one field per
// attribute view, like a module rule.
struct smoke_rule {
	struct classifier_rule rule;
	struct filter_devices devices;
	struct classify_line_ranges vlan_ranges;
	struct filter_net4s net4_srcs;
	struct filter_net4s net4_dsts;
	struct filter_net6s net6_srcs;
	struct filter_net6s net6_dsts;
	struct classify_line_ranges ipproto_ranges;
	struct filter_port_ranges src_port_ranges;
	struct filter_port_ranges dst_port_ranges;
	enum filter_ip_fragment fragment;
};

static inline void
smoke_rule_get_devices(
	const struct classifier_rule *rule, struct filter_devices *devices
) {
	const struct smoke_rule *smoke_rule =
		container_of(rule, struct smoke_rule, rule);
	*devices = smoke_rule->devices;
}

static inline void
smoke_rule_get_vlan_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct smoke_rule *smoke_rule =
		container_of(rule, struct smoke_rule, rule);
	*ranges = smoke_rule->vlan_ranges;
}

static inline void
smoke_rule_get_net4_srcs(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct smoke_rule *smoke_rule =
		container_of(rule, struct smoke_rule, rule);
	*nets = smoke_rule->net4_srcs;
}

static inline void
smoke_rule_get_net4_dsts(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct smoke_rule *smoke_rule =
		container_of(rule, struct smoke_rule, rule);
	*nets = smoke_rule->net4_dsts;
}

static inline void
smoke_rule_get_net6_srcs(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct smoke_rule *smoke_rule =
		container_of(rule, struct smoke_rule, rule);
	*nets = smoke_rule->net6_srcs;
}

static inline void
smoke_rule_get_net6_dsts(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct smoke_rule *smoke_rule =
		container_of(rule, struct smoke_rule, rule);
	*nets = smoke_rule->net6_dsts;
}

static inline enum filter_ip_fragment
smoke_rule_get_fragment(const struct classifier_rule *rule) {
	const struct smoke_rule *smoke_rule =
		container_of(rule, struct smoke_rule, rule);
	return smoke_rule->fragment;
}

static inline void
smoke_rule_get_ipproto_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct smoke_rule *smoke_rule =
		container_of(rule, struct smoke_rule, rule);
	*ranges = smoke_rule->ipproto_ranges;
}

static inline void
smoke_rule_get_l4_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	(void)rule;
	static const struct classify_line_range whole[1] = {{0, 0x101}};
	ranges->items = whole;
	ranges->count = 1;
}

static inline void
smoke_rule_get_src_port_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct smoke_rule *smoke_rule =
		container_of(rule, struct smoke_rule, rule);
	ranges->count = smoke_rule->src_port_ranges.count;
	ranges->items = (const struct filter_u16_span *)
				smoke_rule->src_port_ranges.items;
}

static inline void
smoke_rule_get_dst_port_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct smoke_rule *smoke_rule =
		container_of(rule, struct smoke_rule, rule);
	ranges->count = smoke_rule->dst_port_ranges.count;
	ranges->items = (const struct filter_u16_span *)
				smoke_rule->dst_port_ranges.items;
}

CLASSIFY_DEVICE_COMPILE(smoke_device, smoke_rule_get_devices)
CLASSIFY_LINE_COMPILE(
	smoke_vlan, struct classify_attr_line, 4096, smoke_rule_get_vlan_ranges
)
CLASSIFY_IPFRAG_COMPILE(smoke_ipfrag, smoke_rule_get_fragment)
CLASSIFY_NET4_COMPILE(smoke_net4_src, smoke_rule_get_net4_srcs)
CLASSIFY_NET4_COMPILE(smoke_net4_dst, smoke_rule_get_net4_dsts)
CLASSIFY_NET6_COMPILE(smoke_net6_src, smoke_rule_get_net6_srcs)
CLASSIFY_NET6_COMPILE(smoke_net6_dst, smoke_rule_get_net6_dsts)
CLASSIFY_LINE_COMPILE(
	smoke_ipproto,
	struct classify_attr_line,
	0x100,
	smoke_rule_get_ipproto_ranges
)
CLASSIFY_LINE_COMPILE(
	smoke_l4, struct classify_attr_line, 0x101, smoke_rule_get_l4_ranges
)
CLASSIFY_U16_RANGES_COMPILE(
	smoke_src_port,
	struct classify_attr_port,
	smoke_rule_get_src_port_ranges
)
CLASSIFY_U16_RANGES_COMPILE(
	smoke_dst_port,
	struct classify_attr_port,
	smoke_rule_get_dst_port_ranges
)

static void (*const sign_compile[])(void) = {
	(void (*)(void))classify_smoke_device_compile,
	(void (*)(void))classify_smoke_vlan_compile,
	(void (*)(void))classify_smoke_ipfrag_compile,
	(void (*)(void))classify_smoke_net4_src_compile,
	(void (*)(void))classify_smoke_net4_dst_compile,
	(void (*)(void))classify_smoke_net6_src_compile,
	(void (*)(void))classify_smoke_net6_dst_compile,
	(void (*)(void))classify_smoke_ipproto_compile,
	(void (*)(void))classify_smoke_l4_compile,
	(void (*)(void))classify_smoke_src_port_compile,
	(void (*)(void))classify_smoke_dst_port_compile,
};

static void (*const sign_query[])(void) = {
	(void (*)(void))classify_joint_lookup,
	(void (*)(void))classify_combine,
	(void (*)(void))classify_resolve,
};

int
main(void) {
	(void)sign_compile;
	(void)sign_query;
	return 0;
}
