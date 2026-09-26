// Runtime test of the acl module fragment classification contract, at
// the class level: a protocol-only rule pair - a protocol-zero deny
// rule and an any-protocol allow rule - must resolve non-initial
// fragments (declared protocol, absent transport header) through the
// allow rule only, while a real protocol-zero packet resolves through
// the deny rule.
//
// Pins the projection split (plain takes the whole low byte rules),
// the ipproto line over the declared protocol, and the plain joint
// decoding both rules.

#include "lib/classify/classify.h"
#include "lib/classify/compiler/device.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/ipfrag.h"
#include "lib/classify/compiler/line.h"
#include "lib/classify/compiler/net4.h"
#include "lib/classify/query.h"
#include "lib/classify/rule.h"

#include "common/container_of.h"
#include "common/memory.h"

#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// The test rule: the network pair with the authored protocol ranges.
struct fragment_rule {
	struct classifier_rule rule;
	struct filter_net4s srcs;
	struct filter_net4s dsts;
	struct classify_line_range proto_items[1];
	struct classify_line_ranges protos;
	enum filter_ip_fragment fragment;
};

static struct fragment_rule
make_rule(
	uint8_t proto_lo, uint8_t proto_hi, enum filter_ip_fragment fragment
) {
	struct fragment_rule frag_rule;
	memset(&frag_rule, 0, sizeof(frag_rule));

	static const struct net4 any = {
		.addr = {0, 0, 0, 0}, .mask = {0, 0, 0, 0}
	};
	frag_rule.srcs.count = 1;
	frag_rule.srcs.items = (struct net4 *)&any;

	frag_rule.dsts.count = 1;
	frag_rule.dsts.items = (struct net4 *)&any;

	// The control plane derives the protocol interval out of the
	// authored range: the high byte span of the protocol number. The
	// items pointer stays unset here - the rule travels by value, so
	// the receiver rebinds it to its own copy.
	frag_rule.proto_items[0].from = proto_lo;
	frag_rule.proto_items[0].to = proto_hi;
	frag_rule.protos.count = 1;
	frag_rule.fragment = fragment;

	return frag_rule;
}

static inline void
fragment_rule_get_devices(
	const struct classifier_rule *rule, struct filter_devices *devices
) {
	const struct fragment_rule *fragment_rule =
		container_of(rule, struct fragment_rule, rule);
	(void)fragment_rule;
	devices->count = 0;
	devices->items = NULL;
}

static inline void
fragment_rule_get_net4_srcs(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct fragment_rule *fragment_rule =
		container_of(rule, struct fragment_rule, rule);
	*nets = fragment_rule->srcs;
}

static inline void
fragment_rule_get_net4_dsts(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct fragment_rule *fragment_rule =
		container_of(rule, struct fragment_rule, rule);
	*nets = fragment_rule->dsts;
}

static inline void
fragment_rule_get_ipproto_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct fragment_rule *fragment_rule =
		container_of(rule, struct fragment_rule, rule);
	*ranges = fragment_rule->protos;
}

static inline enum filter_ip_fragment
fragment_rule_get_fragment(const struct classifier_rule *rule) {
	const struct fragment_rule *fragment_rule =
		container_of(rule, struct fragment_rule, rule);
	return fragment_rule->fragment;
}

CLASSIFY_DEVICE_COMPILE(fragment_device, fragment_rule_get_devices)
CLASSIFY_NET4_COMPILE(fragment_net4_src, fragment_rule_get_net4_srcs)
CLASSIFY_NET4_COMPILE(fragment_net4_dst, fragment_rule_get_net4_dsts)
CLASSIFY_LINE_COMPILE(
	fragment_ipproto,
	struct classify_attr_line,
	0x100,
	fragment_rule_get_ipproto_ranges
)
CLASSIFY_IPFRAG_COMPILE(fragment_ipfrag, fragment_rule_get_fragment)

// Resolves the plain decoder for one packet shape: the ipproto class
// of the declared protocol and the frag class of the fragment state
// combined onto the mid class of the rule pair.
static uint32_t
resolve_plain(
	const struct classify_attr_line *ipproto_attr,
	const struct value_table *proto_joint,
	const struct classify_attr_ipfrag *frag_attr,
	const struct value_table *root_joint,
	const struct vline *rule_map,
	uint32_t mid_class,
	uint32_t proto,
	uint32_t frag_id
) {
	uint32_t proto_class =
		vline_get((struct vline *)&ipproto_attr->line, proto);
	uint32_t core = value_table_get(proto_joint, mid_class, proto_class);
	uint32_t frag_class =
		value_table_get(&frag_attr->value_table, 0, frag_id);
	uint32_t result;
	classify_combine(
		(struct value_table *)root_joint,
		(struct vline *)rule_map,
		&core,
		&frag_class,
		&result,
		1
	);
	return result;
}

static void
run_scenario(
	struct fragment_rule rule0,
	struct fragment_rule rule1,
	uint32_t proto_a,
	uint32_t frag_a,
	uint32_t expect_a,
	uint32_t proto_b,
	uint32_t frag_b,
	uint32_t expect_b
) {
	rule0.protos.items = rule0.proto_items;
	rule1.protos.items = rule1.proto_items;

	const struct classifier_rule *rules[2] = {
		&rule0.rule,
		&rule1.rule,
	};

	void *memory = malloc(1 << 22);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 22);
	struct memory_context mctx;
	assert(memory_context_init(&mctx, "test", &allocator) == 0);

	struct classifier stage_dev = {0};
	struct classifier stage_n4_src = {0};
	struct classifier stage_n4_dst = {0};
	struct classifier stage_nets = {0};
	struct classifier stage_mid = {0};
	struct classifier stage_ipproto = {0};
	struct classifier core_stage = {0};
	struct classifier stage_frag = {0};
	struct classifier plain_stage = {0};

	struct classify_attr_device dev_attr;
	struct classify_attr_net4 src_attr, dst_attr;
	struct classify_attr_line ipproto_attr;
	struct value_table nets_joint, mid_joint, proto_joint, root_joint;
	struct classify_attr_ipfrag frag_attr;
	struct vline rule_map;
	memset(&dev_attr, 0, sizeof(dev_attr));
	memset(&src_attr, 0, sizeof(src_attr));
	memset(&dst_attr, 0, sizeof(dst_attr));
	memset(&ipproto_attr, 0, sizeof(ipproto_attr));
	memset(&nets_joint, 0, sizeof(nets_joint));
	memset(&mid_joint, 0, sizeof(mid_joint));
	memset(&proto_joint, 0, sizeof(proto_joint));
	memset(&root_joint, 0, sizeof(root_joint));
	memset(&frag_attr, 0, sizeof(frag_attr));
	memset(&rule_map, 0, sizeof(rule_map));

	assert(classify_fragment_device_compile(
		       &mctx, rules, 2, &dev_attr, &stage_dev
	       ) == 0);
	assert(classify_fragment_net4_src_compile(
		       &mctx, rules, 2, &src_attr, &stage_n4_src
	       ) == 0);
	assert(classify_fragment_net4_dst_compile(
		       &mctx, rules, 2, &dst_attr, &stage_n4_dst
	       ) == 0);
	assert(classify_join(
		       &mctx,
		       &stage_n4_src,
		       &stage_n4_dst,
		       2,
		       &nets_joint,
		       &stage_nets
	       ) == 0);
	assert(classify_join(
		       &mctx, &stage_dev, &stage_nets, 2, &mid_joint, &stage_mid
	       ) == 0);
	assert(classify_fragment_ipproto_compile(
		       &mctx, rules, 2, &ipproto_attr, &stage_ipproto
	       ) == 0);
	assert(classify_join(
		       &mctx,
		       &stage_mid,
		       &stage_ipproto,
		       2,
		       &proto_joint,
		       &core_stage
	       ) == 0);
	assert(classify_fragment_ipfrag_compile(
		       &mctx, rules, 2, &frag_attr, &stage_frag
	       ) == 0);
	assert(classify_join(
		       &mctx,
		       &core_stage,
		       &stage_frag,
		       2,
		       &root_joint,
		       &plain_stage
	       ) == 0);
	assert(classify_decode(&mctx, &plain_stage, rules, 2, &rule_map) == 0);

	uint32_t mid_class = value_table_get(&mid_joint, 0, 0);

	uint32_t result_a = resolve_plain(
		&ipproto_attr,
		&proto_joint,
		&frag_attr,
		&root_joint,
		&rule_map,
		mid_class,
		proto_a,
		frag_a
	);
	uint32_t result_b = resolve_plain(
		&ipproto_attr,
		&proto_joint,
		&frag_attr,
		&root_joint,
		&rule_map,
		mid_class,
		proto_b,
		frag_b
	);
	printf("scenario: result_a=%u (expect %u) result_b=%u (expect %u)\n",
	       result_a,
	       expect_a,
	       result_b,
	       expect_b);
	assert(result_a == expect_a);
	assert(result_b == expect_b);

	classifier_fini(&plain_stage, &mctx, 2);
	classifier_fini(&stage_frag, &mctx, 2);
	classifier_fini(&core_stage, &mctx, 2);
	classifier_fini(&stage_ipproto, &mctx, 2);
	classifier_fini(&stage_mid, &mctx, 2);
	classifier_fini(&stage_nets, &mctx, 2);
	classifier_fini(&stage_n4_dst, &mctx, 2);
	classifier_fini(&stage_n4_src, &mctx, 2);
	classifier_fini(&stage_dev, &mctx, 2);
	classify_attr_ipfrag_free(&mctx, &frag_attr);
	vline_free(&rule_map);
	value_table_free(&root_joint);
	classify_attr_line_free(&mctx, &ipproto_attr);
	value_table_free(&proto_joint);
	value_table_free(&mid_joint);
	value_table_free(&nets_joint);
	classify_attr_net4_free(&mctx, &src_attr);
	classify_attr_net4_free(&mctx, &dst_attr);
	classify_attr_device_free(&mctx, &dev_attr);

	memory_context_fini(&mctx);
	free(memory);
}

int
main(void) {
	// Scenario one: a protocol-zero deny rule and an any-protocol
	// allow rule; a real protocol-zero packet resolves through the
	// deny rule, a declared-protocol fragment through the allow rule.
	run_scenario(
		make_rule(0, 0, FILTER_IP_FRAG_ANY),
		make_rule(0, 255, FILTER_IP_FRAG_ANY),
		0,
		0,
		0,
		6,
		1,
		1
	);

	// Scenario two: a TCP deny rule for non-initial fragments and an
	// any-protocol allow rule; a TCP fragment resolves through the
	// deny rule, a UDP fragment through the allow rule.
	run_scenario(
		make_rule(6, 6, FILTER_IP_FRAG_FRAG),
		make_rule(0, 255, FILTER_IP_FRAG_ANY),
		6,
		1,
		0,
		17,
		1,
		1
	);

	return 0;
}
