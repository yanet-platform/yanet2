// Runtime test of the two stage compilation over explicitly composed
// classifiers, in the shape of the acl module: the ip6 core classifier
// over the network side attributes is built once over the union
// ruleset, the ports classifier over the port side projection, and the
// port scoped filter joins the two through its root joint; every
// filter decodes its own projection of the ruleset.
//
// Pins the projection isolation of the decoders, the shared core
// evaluation of both filters built from one core classifier, and the
// agreement of the composed port scoped filter with a flat single
// classifier holding the same six attributes.

#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/classify.h"
#include "lib/classify/compiler/device.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/line.h"
#include "lib/classify/compiler/net6.h"
#include "lib/classify/compiler/u16_ranges.h"
#include "lib/classify/query.h"
#include "lib/classify/rule.h"

#include "common/container_of.h"

#include "modules/acl/dataplane/filter_lookup.h"

#include "lib/utils/packet.h"

#include "common/memory.h"

#include <assert.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static void
put16(uint8_t *p, uint32_t g, uint16_t v) {
	p[g * 2] = v >> 8;
	p[g * 2 + 1] = v;
}

// The test rule: the network pair with the protocol and port windows,
// the rule format the compilers never see beyond its getters.
struct test_rule {
	struct classifier_rule rule;
	struct filter_net6s srcs;
	struct filter_net6s dsts;
	struct classify_line_ranges ipproto_ranges;
	struct filter_port_ranges src_port_ranges;
	struct filter_port_ranges dst_port_ranges;
};

static inline void
test_rule_get_devices(
	const struct classifier_rule *rule, struct filter_devices *devices
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	(void)test_rule;
	devices->count = 0;
	devices->items = NULL;
}

static inline void
test_rule_get_net6_srcs(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	*nets = test_rule->srcs;
}

static inline void
test_rule_get_net6_dsts(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	*nets = test_rule->dsts;
}

static inline void
test_rule_get_ipproto_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	*ranges = test_rule->ipproto_ranges;
}

static inline void
test_rule_get_src_port_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	ranges->count = test_rule->src_port_ranges.count;
	ranges->items = (const struct filter_u16_span *)
				test_rule->src_port_ranges.items;
}

static inline void
test_rule_get_dst_port_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	ranges->count = test_rule->dst_port_ranges.count;
	ranges->items = (const struct filter_u16_span *)
				test_rule->dst_port_ranges.items;
}

CLASSIFY_DEVICE_COMPILE(test_device, test_rule_get_devices)
CLASSIFY_NET6_COMPILE(test_net6_src, test_rule_get_net6_srcs)
CLASSIFY_NET6_COMPILE(test_net6_dst, test_rule_get_net6_dsts)
CLASSIFY_LINE_COMPILE(
	test_ipproto,
	struct classify_attr_line,
	0x100,
	test_rule_get_ipproto_ranges
)
CLASSIFY_U16_RANGES_COMPILE(
	test_src_port, struct classify_attr_port, test_rule_get_src_port_ranges
)
CLASSIFY_U16_RANGES_COMPILE(
	test_dst_port, struct classify_attr_port, test_rule_get_dst_port_ranges
)

// Fills a v6 rule matching the given networks and port windows; a
// window of NULL means the full range, which keeps the rule out of the
// port scoped projection.
static struct test_rule
make_rule(
	const uint8_t src[NET6_LEN],
	uint8_t src_bits,
	const uint8_t dst[NET6_LEN],
	uint8_t dst_bits,
	const struct filter_port_range *sport,
	const struct filter_port_range *dport,
	struct net6 *srcs,
	struct net6 *dsts,
	struct filter_port_range *ranges
) {
	memset(srcs, 0, sizeof(*srcs));
	memcpy(srcs->addr, src, NET6_LEN);
	memset(srcs->mask, 0xff, src_bits / 8);
	if (src_bits % 8) {
		srcs->mask[src_bits / 8] = 0xff << (8 - src_bits % 8);
	}

	memset(dsts, 0, sizeof(*dsts));
	memcpy(dsts->addr, dst, NET6_LEN);
	memset(dsts->mask, 0xff, dst_bits / 8);
	if (dst_bits % 8) {
		dsts->mask[dst_bits / 8] = 0xff << (8 - dst_bits % 8);
	}

	struct test_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.srcs.count = 1;
	rule.srcs.items = srcs;
	rule.dsts.count = 1;
	rule.dsts.items = dsts;

	ranges[0] = sport ? *sport : (struct filter_port_range){0, 65535};
	ranges[1] = dport ? *dport : (struct filter_port_range){0, 65535};
	rule.src_port_ranges.count = 1;
	rule.src_port_ranges.items = ranges;
	rule.dst_port_ranges.count = 1;
	rule.dst_port_ranges.items = ranges + 1;

	return rule;
}

/*
 * The ip6 filter of the test: the decoder of the ip6 projection over
 * the classes of the shared core classifier.
 *
 * The module's ip6 filter carries a root joint onto the fragment
 * suffix classifier; the test's ip6 projection has no suffix, so its
 * decoder resolves the core classes directly and the joint of the
 * module shape stays out.
 */
struct test_filter_ip6 {
	struct vline rule_map;
};

/*
 * The composed classification of the test: the core and the ports
 * classifiers with the two filters over them. The port scoped filter
 * is the module struct; its family root joint joins the core classes
 * with the ports classes and carries the decoder of the port scoped
 * projection.
 */
struct test_composed {
	struct acl_classifier_core6 core6;
	struct acl_classifier_ports ports;
	struct test_filter_ip6 flt_ip6;
	struct acl_filter_ip6_tcp flt_ip6_port;
};

// Rule group mapping rows of the composed compile below: the five core
// attributes with their four joints, the two port attributes with
// their joint, and the family joint.
#define TEST_CORE_RULE_GROUP_ROWS 11

/*
 * Compiles the composed classification of the test, in the shape of
 * the acl module: the core classifier over the union of both
 * projections, the ports classifier over the port scoped projection,
 * and the family root joint joining the two; the ip6 filter decodes
 * the ip6 projection out of the core classes, the port scoped filter
 * decodes the port scoped projection out of the family classes.
 *
 * The registries and the rule group mappings are compile scratch,
 * released before the routine returns.
 */
static void
test_composed_compile(
	struct memory_context *mctx,
	const struct classifier_rule **union_rules,
	const struct classifier_rule **ip6_rules,
	const struct classifier_rule **port_rules,
	uint32_t rule_count,
	struct test_composed *composed
) {
	struct acl_classifier_core6 *core = &composed->core6;
	struct acl_classifier_ports *ports = &composed->ports;
	struct test_filter_ip6 *flt_ip6 = &composed->flt_ip6;
	struct acl_filter_ip6_tcp *flt_ip6_port = &composed->flt_ip6_port;

	memset(composed, 0, sizeof(*composed));

	struct classifier stage_dev = {0};
	struct classifier stage_n6_src = {0};
	struct classifier stage_n6_dst = {0};
	struct classifier stage_nets = {0};
	struct classifier stage_mid = {0};
	struct classifier stage_ipproto = {0};
	struct classifier stage_proto = {0};
	struct classifier stage_psrc = {0};
	struct classifier stage_pdst = {0};
	struct classifier stage_ports = {0};
	struct classifier stage_family = {0};

	// The core classifier over the union of both projections: the
	// network pair joins first, the device attribute joins its
	// classes, and the protocol attribute closes the root.
	assert(classify_test_device_compile(
		       mctx,
		       union_rules,
		       rule_count,
		       &core->dev_attr,
		       &stage_dev
	       ) == 0);
	assert(classify_test_net6_src_compile(
		       mctx,
		       union_rules,
		       rule_count,
		       &core->net6_src_attr,
		       &stage_n6_src
	       ) == 0);
	assert(classify_test_net6_dst_compile(
		       mctx,
		       union_rules,
		       rule_count,
		       &core->net6_dst_attr,
		       &stage_n6_dst
	       ) == 0);
	assert(classify_join(
		       mctx,
		       &stage_n6_src,
		       &stage_n6_dst,
		       rule_count,
		       &core->nets_joint,
		       &stage_nets
	       ) == 0);
	assert(classify_join(
		       mctx,
		       &stage_dev,
		       &stage_nets,
		       rule_count,
		       &core->mid_joint,
		       &stage_mid
	       ) == 0);
	assert(classify_test_ipproto_compile(
		       mctx,
		       union_rules,
		       rule_count,
		       &core->ipproto_attr,
		       &stage_ipproto
	       ) == 0);
	assert(classify_join(
		       mctx,
		       &stage_mid,
		       &stage_ipproto,
		       rule_count,
		       &core->proto_joint,
		       &stage_proto
	       ) == 0);
	assert(classify_join(
		       mctx,
		       &stage_mid,
		       &stage_ipproto,
		       rule_count,
		       &core->proto_joint,
		       &stage_proto
	       ) == 0);

	// The ip6 filter decodes its own projection out of the core
	// classes.
	assert(classify_decode(
		       mctx,
		       &stage_proto,
		       ip6_rules,
		       rule_count,
		       &flt_ip6->rule_map
	       ) == 0);

	// The ports classifier over the port scoped projection, joined
	// with the core classes through the family root joint.
	assert(classify_test_src_port_compile(
		       mctx,
		       port_rules,
		       rule_count,
		       &ports->src_attr,
		       &stage_psrc
	       ) == 0);
	assert(classify_test_dst_port_compile(
		       mctx,
		       port_rules,
		       rule_count,
		       &ports->dst_attr,
		       &stage_pdst
	       ) == 0);
	assert(classify_join(
		       mctx,
		       &stage_psrc,
		       &stage_pdst,
		       rule_count,
		       &ports->joint,
		       &stage_ports
	       ) == 0);
	assert(classify_join(
		       mctx,
		       &stage_proto,
		       &stage_ports,
		       rule_count,
		       &flt_ip6_port->root_joint,
		       &stage_family
	       ) == 0);
	assert(classify_decode(
		       mctx,
		       &stage_family,
		       port_rules,
		       rule_count,
		       &flt_ip6_port->rule_map
	       ) == 0);

	classifier_fini(&stage_dev, mctx, rule_count);
	classifier_fini(&stage_n6_src, mctx, rule_count);
	classifier_fini(&stage_n6_dst, mctx, rule_count);
	classifier_fini(&stage_nets, mctx, rule_count);
	classifier_fini(&stage_mid, mctx, rule_count);
	classifier_fini(&stage_proto, mctx, rule_count);
	classifier_fini(&stage_psrc, mctx, rule_count);
	classifier_fini(&stage_pdst, mctx, rule_count);
	classifier_fini(&stage_ports, mctx, rule_count);
	classifier_fini(&stage_family, mctx, rule_count);
}

/*
 * The flat classifier of the agreement check: the six attributes of
 * the composed classification joined in one struct, in the pairwise
 * order of the composed build.
 */
struct test_classifier_flat6 {
	struct classify_attr_device dev_attr;
	struct classify_attr_net6 net6_src_attr;
	struct classify_attr_net6 net6_dst_attr;
	struct classify_attr_line ipproto_attr;
	struct classify_attr_port port_src_attr;
	struct classify_attr_port port_dst_attr;
	struct value_table nets_joint;
	struct value_table mid_joint;
	struct value_table proto_joint;
	struct value_table root_joint;
	struct value_table ports_joint;
	struct value_table family_joint;
};

// Rule group mapping rows of the flat compile below: the six
// attributes and their five joints.
#define TEST_FLAT_RULE_GROUP_ROWS 11

/*
 * Compiles the flat classifier over the union ruleset and decodes the
 * port scoped projection out of its family classes; the registries and
 * the rule group mappings are compile scratch, released before the
 * routine returns.
 */
static void
test_flat_compile(
	struct memory_context *mctx,
	const struct classifier_rule **union_rules,
	const struct classifier_rule **port_rules,
	uint32_t rule_count,
	struct test_classifier_flat6 *cls,
	struct vline *rule_map
) {
	memset(cls, 0, sizeof(*cls));
	memset(rule_map, 0, sizeof(*rule_map));

	struct classifier stage_dev = {0};
	struct classifier stage_n6_src = {0};
	struct classifier stage_n6_dst = {0};
	struct classifier stage_nets = {0};
	struct classifier stage_mid = {0};
	struct classifier stage_ipproto = {0};
	struct classifier stage_proto = {0};
	struct classifier stage_psrc = {0};
	struct classifier stage_pdst = {0};
	struct classifier stage_ports = {0};
	struct classifier stage_family = {0};

	assert(classify_test_device_compile(
		       mctx, union_rules, rule_count, &cls->dev_attr, &stage_dev
	       ) == 0);
	assert(classify_test_net6_src_compile(
		       mctx,
		       union_rules,
		       rule_count,
		       &cls->net6_src_attr,
		       &stage_n6_src
	       ) == 0);
	assert(classify_test_net6_dst_compile(
		       mctx,
		       union_rules,
		       rule_count,
		       &cls->net6_dst_attr,
		       &stage_n6_dst
	       ) == 0);
	assert(classify_join(
		       mctx,
		       &stage_n6_src,
		       &stage_n6_dst,
		       rule_count,
		       &cls->nets_joint,
		       &stage_nets
	       ) == 0);
	assert(classify_join(
		       mctx,
		       &stage_dev,
		       &stage_nets,
		       rule_count,
		       &cls->mid_joint,
		       &stage_mid
	       ) == 0);
	assert(classify_test_ipproto_compile(
		       mctx,
		       union_rules,
		       rule_count,
		       &cls->ipproto_attr,
		       &stage_ipproto
	       ) == 0);
	assert(classify_join(
		       mctx,
		       &stage_mid,
		       &stage_ipproto,
		       rule_count,
		       &cls->proto_joint,
		       &stage_proto
	       ) == 0);

	assert(classify_test_src_port_compile(
		       mctx,
		       union_rules,
		       rule_count,
		       &cls->port_src_attr,
		       &stage_psrc
	       ) == 0);
	assert(classify_test_dst_port_compile(
		       mctx,
		       union_rules,
		       rule_count,
		       &cls->port_dst_attr,
		       &stage_pdst
	       ) == 0);
	assert(classify_join(
		       mctx,
		       &stage_psrc,
		       &stage_pdst,
		       rule_count,
		       &cls->ports_joint,
		       &stage_ports
	       ) == 0);
	assert(classify_join(
		       mctx,
		       &stage_proto,
		       &stage_ports,
		       rule_count,
		       &cls->family_joint,
		       &stage_family
	       ) == 0);
	assert(classify_decode(
		       mctx, &stage_family, port_rules, rule_count, rule_map
	       ) == 0);

	classifier_fini(&stage_dev, mctx, rule_count);
	classifier_fini(&stage_n6_src, mctx, rule_count);
	classifier_fini(&stage_n6_dst, mctx, rule_count);
	classifier_fini(&stage_nets, mctx, rule_count);
	classifier_fini(&stage_mid, mctx, rule_count);
	classifier_fini(&stage_proto, mctx, rule_count);
	classifier_fini(&stage_psrc, mctx, rule_count);
	classifier_fini(&stage_pdst, mctx, rule_count);
	classifier_fini(&stage_ports, mctx, rule_count);
	classifier_fini(&stage_family, mctx, rule_count);
}

// Fills one synthetic v6 packet of the given address and port pair.
static void
make_packet(
	struct packet *packet,
	const uint8_t src[NET6_LEN],
	const uint8_t dst[NET6_LEN],
	uint16_t sport,
	uint16_t dport
) {
	memset(packet, 0, sizeof(*packet));
	assert(fill_packet_net6(
		       packet, src, dst, sport, dport, IPPROTO_UDP, 0
	       ) == 0);
}

/*
 * Classifies one synthetic packet through the ip6 filter: the core
 * dispatcher yields the core classes, the decoder resolves them into
 * the rules of the ip6 projection.
 */
static uint32_t
classify_ip6(
	const struct acl_classifier_core6 *core,
	const uint64_t *device_map,
	const struct test_filter_ip6 *flt,
	const uint8_t src[NET6_LEN],
	const uint8_t dst[NET6_LEN],
	uint16_t sport,
	uint16_t dport
) {
	struct packet packet;
	make_packet(&packet, src, dst, sport, dport);
	struct packet *packet_ptr = &packet;

	uint32_t classes[1];
	uint32_t results[1];
	acl_classify_core6(
		core,
		device_map,
		(const struct packet **)&packet_ptr,
		classes,
		1
	);
	classify_resolve(&flt->rule_map, classes, results, 1);

	free_packet(&packet);
	return results[0];
}

/*
 * Classifies one synthetic packet through the port scoped filter: the
 * core dispatcher yields the shared core classes once, the ports
 * dispatcher yields the ports classes, and the family root joint
 * resolves both into the rules of the port scoped projection.
 */
static uint32_t
classify_ip6_port(
	const struct acl_classifier_core6 *core,
	const uint64_t *device_map,
	const struct acl_classifier_ports *ports,
	const struct acl_filter_ip6_tcp *flt,
	const uint8_t src[NET6_LEN],
	const uint8_t dst[NET6_LEN],
	uint16_t sport,
	uint16_t dport
) {
	struct packet packet;
	make_packet(&packet, src, dst, sport, dport);
	struct packet *packet_ptr = &packet;

	uint32_t core_classes[1];
	uint32_t port_classes[1];
	uint32_t results[1];
	acl_classify_core6(
		core,
		device_map,
		(const struct packet **)&packet_ptr,
		core_classes,
		1
	);
	acl_classify_ports(
		ports, (const struct packet **)&packet_ptr, port_classes, 1
	);
	classify_combine(
		&flt->root_joint,
		&flt->rule_map,
		core_classes,
		port_classes,
		results,
		1
	);

	free_packet(&packet);
	return results[0];
}

/*
 * Classifies one synthetic packet through the flat classifier: the
 * six leaf lookups and the five joins run as separated statements of
 * one body, resolved through the decoder of the port scoped
 * projection.
 */
static uint32_t
classify_flat(
	const struct test_classifier_flat6 *cls,
	const uint64_t *device_map,
	const struct vline *rule_map,
	const uint8_t src[NET6_LEN],
	const uint8_t dst[NET6_LEN],
	uint16_t sport,
	uint16_t dport
) {
	struct packet packet;
	make_packet(&packet, src, dst, sport, dport);
	const struct packet *batch[1] = {&packet};

	uint32_t dev[1];
	uint32_t n6s[1];
	uint32_t n6d[1];
	uint32_t ipproto[1];
	uint32_t psrc[1];
	uint32_t pdst[1];
	uint8_t addrs6[1][NET6_LEN];

	acl_lookup_device(&cls->dev_attr, device_map, batch, dev, 1);
	acl_packet_get_net6_src_batch(batch, addrs6[0], 1);
	classify_net6_lookup(&cls->net6_src_attr, addrs6[0], n6s, 1);
	acl_packet_get_net6_dst_batch(batch, addrs6[0], 1);
	classify_net6_lookup(&cls->net6_dst_attr, addrs6[0], n6d, 1);
	acl_lookup_ipproto(&cls->ipproto_attr, batch, ipproto, 1);
	acl_lookup_ports(
		&cls->port_src_attr, &cls->port_dst_attr, batch, psrc, pdst, 1
	);

	// The joint stages chain in place through the class arrays, in the
	// join order of the compile.
	classify_joint_lookup(&cls->nets_joint, n6s, n6d, n6s, 1);
	classify_joint_lookup(&cls->mid_joint, dev, n6s, dev, 1);
	classify_joint_lookup(&cls->proto_joint, dev, ipproto, dev, 1);
	classify_joint_lookup(&cls->ports_joint, psrc, pdst, psrc, 1);

	uint32_t results[1];
	classify_combine(&cls->family_joint, rule_map, dev, psrc, results, 1);

	free_packet(&packet);
	return results[0];
}

int
main(void) {
	uint8_t net_a[NET6_LEN] = {0};
	put16(net_a, 0, 0x2a02);
	uint8_t net_b[NET6_LEN] = {0};
	put16(net_b, 0, 0x2a0d);

	struct net6 srcs[3], dsts[3];
	struct filter_port_range ranges[3][2];
	const struct filter_port_range window = {100, 200};

	// rule 0: full port windows, present in the ip6 projection only;
	// rules 1 and 2: port restricted, present in the port scoped
	// projection only.
	struct test_rule rule0 = make_rule(
		net_a, 32, net_b, 32, NULL, NULL, &srcs[0], &dsts[0], ranges[0]
	);
	struct test_rule rule1 = make_rule(
		net_a,
		32,
		net_b,
		32,
		&window,
		NULL,
		&srcs[1],
		&dsts[1],
		ranges[1]
	);
	struct test_rule rule2 = make_rule(
		net_b,
		32,
		net_a,
		32,
		NULL,
		&window,
		&srcs[2],
		&dsts[2],
		ranges[2]
	);

	const struct classifier_rule *union_rules[3] = {
		&rule0.rule, &rule1.rule, &rule2.rule
	};
	const struct classifier_rule *ip6_rules[3] = {&rule0.rule, NULL, NULL};
	const struct classifier_rule *port_rules[3] = {
		NULL, &rule1.rule, &rule2.rule
	};

	void *memory = malloc(1 << 24);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);
	struct memory_context mctx;
	assert(memory_context_init(&mctx, "test", &allocator) == 0);

	// The composed classification: the core classifier and the ports
	// classifier are built once, and both filters reference them.
	struct test_composed composed;
	test_composed_compile(
		&mctx, union_rules, ip6_rules, port_rules, 3, &composed
	);
	struct acl_classifier_core6 *core6 = &composed.core6;
	struct acl_classifier_ports *ports = &composed.ports;

	// The device lookups resolve through the global-to-module mapping
	// the module binds at execution-context commit; the test rules name
	// no devices, so an identity of one entry stands in for it. Both
	// filters share the core classifier, so one binding covers both.
	static const uint64_t device_map[1] = {0};

	// The ip6 filter resolves through the core classifier alone and
	// sees the full port rule only.
	assert(classify_ip6(
		       core6,
		       device_map,
		       &composed.flt_ip6,
		       net_a,
		       net_b,
		       150,
		       1000
	       ) == 0);
	assert(classify_ip6(
		       core6,
		       device_map,
		       &composed.flt_ip6,
		       net_a,
		       net_b,
		       500,
		       1000
	       ) == 0);
	assert(classify_ip6(
		       core6,
		       device_map,
		       &composed.flt_ip6,
		       net_b,
		       net_a,
		       150,
		       150
	       ) == CLASSIFY_RULE_INVALID);

	// The port scoped filter resolves through the family joint and
	// never sees the full port rule.
	assert(classify_ip6_port(
		       core6,
		       device_map,
		       ports,
		       &composed.flt_ip6_port,
		       net_a,
		       net_b,
		       150,
		       1000
	       ) == 1);
	assert(classify_ip6_port(
		       core6,
		       device_map,
		       ports,
		       &composed.flt_ip6_port,
		       net_a,
		       net_b,
		       500,
		       1000
	       ) == CLASSIFY_RULE_INVALID);
	assert(classify_ip6_port(
		       core6,
		       device_map,
		       ports,
		       &composed.flt_ip6_port,
		       net_b,
		       net_a,
		       150,
		       150
	       ) == 2);
	assert(classify_ip6_port(
		       core6,
		       device_map,
		       ports,
		       &composed.flt_ip6_port,
		       net_b,
		       net_a,
		       150,
		       300
	       ) == CLASSIFY_RULE_INVALID);

	// A flat build of the same attributes resolves the same rule
	// indices on every probe above, and the shared path - the core
	// classes evaluated once and combined through the family joint -
	// agrees with the full explicit composition on every probe packet.
	struct test_classifier_flat6 flat6;
	struct vline flat_rule_map;
	test_flat_compile(
		&mctx, union_rules, port_rules, 3, &flat6, &flat_rule_map
	);

	struct {
		const uint8_t *src;
		const uint8_t *dst;
		uint16_t sport;
		uint16_t dport;
	} const probes[] = {
		{net_a, net_b, 150, 1000},
		{net_a, net_b, 500, 1000},
		{net_b, net_a, 150, 150},
		{net_b, net_a, 150, 300},
	};

	assert(classify_flat(
		       &flat6,
		       device_map,
		       &flat_rule_map,
		       net_a,
		       net_b,
		       150,
		       1000
	       ) == 1);
	assert(classify_flat(
		       &flat6,
		       device_map,
		       &flat_rule_map,
		       net_a,
		       net_b,
		       500,
		       1000
	       ) == CLASSIFY_RULE_INVALID);
	assert(classify_flat(
		       &flat6,
		       device_map,
		       &flat_rule_map,
		       net_b,
		       net_a,
		       150,
		       150
	       ) == 2);
	assert(classify_flat(
		       &flat6,
		       device_map,
		       &flat_rule_map,
		       net_b,
		       net_a,
		       150,
		       300
	       ) == CLASSIFY_RULE_INVALID);

	for (uint32_t idx = 0; idx < sizeof(probes) / sizeof(*probes); ++idx) {
		assert(classify_flat(
			       &flat6,
			       device_map,
			       &flat_rule_map,
			       probes[idx].src,
			       probes[idx].dst,
			       probes[idx].sport,
			       probes[idx].dport
		       ) ==
		       classify_ip6_port(
			       core6,
			       device_map,
			       ports,
			       &composed.flt_ip6_port,
			       probes[idx].src,
			       probes[idx].dst,
			       probes[idx].sport,
			       probes[idx].dport
		       ));
	}

	// The filters are freed before the classifiers they reference, in
	// the order of the module destroy.
	value_table_free(&composed.flt_ip6_port.root_joint);
	vline_free(&composed.flt_ip6_port.rule_map);
	vline_free(&composed.flt_ip6.rule_map);

	classify_attr_device_free(&mctx, &core6->dev_attr);
	classify_attr_net6_free(&mctx, &core6->net6_src_attr);
	classify_attr_net6_free(&mctx, &core6->net6_dst_attr);
	classify_attr_line_free(&mctx, &core6->ipproto_attr);
	value_table_free(&core6->nets_joint);
	value_table_free(&core6->mid_joint);
	value_table_free(&core6->proto_joint);
	value_table_free(&core6->proto_joint);

	classify_attr_port_free(&mctx, &ports->src_attr);
	classify_attr_port_free(&mctx, &ports->dst_attr);
	value_table_free(&ports->joint);

	vline_free(&flat_rule_map);
	classify_attr_device_free(&mctx, &flat6.dev_attr);
	classify_attr_net6_free(&mctx, &flat6.net6_src_attr);
	classify_attr_net6_free(&mctx, &flat6.net6_dst_attr);
	classify_attr_line_free(&mctx, &flat6.ipproto_attr);
	classify_attr_port_free(&mctx, &flat6.port_src_attr);
	classify_attr_port_free(&mctx, &flat6.port_dst_attr);
	value_table_free(&flat6.nets_joint);
	value_table_free(&flat6.mid_joint);
	value_table_free(&flat6.proto_joint);

	value_table_free(&flat6.ports_joint);
	value_table_free(&flat6.family_joint);

	memory_context_fini(&mctx);
	free(memory);
	return 0;
}
