// Pins the memory_context naming of the classification library: one
// entry - a compile, a join or a decode - titles every context it
// leaves behind with the caller name and its own artifact leaf, so no
// two siblings under one composition share a name.
//
// The composition below exercises every entry kind over one root
// context: the device, net4, net6, line, ipfrag and u16 range compiles,
// the joints chaining their stages, and the decoders of two
// projections. The teardown case also pins that the whole composition
// releases its root back to a childless state and the allocator to its
// starting balance.

#include "lib/classify/classifiers/device.h"
#include "lib/classify/classifiers/ipfrag.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/classify.h"
#include "lib/classify/compiler/device.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/ipfrag.h"
#include "lib/classify/compiler/line.h"
#include "lib/classify/compiler/net4.h"
#include "lib/classify/compiler/net6.h"
#include "lib/classify/compiler/u16_ranges.h"
#include "lib/classify/rule.h"

#include "common/asan.h"
#include "common/container_of.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// The test rule: the classifier base first, then one field per
// attribute view, like a module rule.
struct names_rule {
	struct classifier_rule rule;
	struct filter_devices devices;
	struct filter_net4s net4_srcs;
	struct filter_net4s net4_dsts;
	struct filter_net6s net6_srcs;
	struct filter_net6s net6_dsts;
	struct classify_line_ranges vlan_ranges;
	struct classify_line_ranges ipproto_ranges;
	struct filter_port_ranges src_port_ranges;
	struct filter_port_ranges dst_port_ranges;
	enum filter_ip_fragment fragment;
};

static inline void
names_rule_get_devices(
	const struct classifier_rule *rule, struct filter_devices *devices
) {
	const struct names_rule *names_rule =
		container_of(rule, struct names_rule, rule);
	*devices = names_rule->devices;
}

static inline void
names_rule_get_net4_srcs(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct names_rule *names_rule =
		container_of(rule, struct names_rule, rule);
	*nets = names_rule->net4_srcs;
}

static inline void
names_rule_get_net4_dsts(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct names_rule *names_rule =
		container_of(rule, struct names_rule, rule);
	*nets = names_rule->net4_dsts;
}

static inline void
names_rule_get_net6_srcs(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct names_rule *names_rule =
		container_of(rule, struct names_rule, rule);
	*nets = names_rule->net6_srcs;
}

static inline void
names_rule_get_net6_dsts(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct names_rule *names_rule =
		container_of(rule, struct names_rule, rule);
	*nets = names_rule->net6_dsts;
}

static inline void
names_rule_get_vlan_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct names_rule *names_rule =
		container_of(rule, struct names_rule, rule);
	*ranges = names_rule->vlan_ranges;
}

static inline void
names_rule_get_ipproto_ranges(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
) {
	const struct names_rule *names_rule =
		container_of(rule, struct names_rule, rule);
	*ranges = names_rule->ipproto_ranges;
}

static inline enum filter_ip_fragment
names_rule_get_fragment(const struct classifier_rule *rule) {
	const struct names_rule *names_rule =
		container_of(rule, struct names_rule, rule);
	return names_rule->fragment;
}

static inline void
names_rule_get_src_port_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct names_rule *names_rule =
		container_of(rule, struct names_rule, rule);
	ranges->count = names_rule->src_port_ranges.count;
	ranges->items = (const struct filter_u16_span *)
				names_rule->src_port_ranges.items;
}

static inline void
names_rule_get_dst_port_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct names_rule *names_rule =
		container_of(rule, struct names_rule, rule);
	ranges->count = names_rule->dst_port_ranges.count;
	ranges->items = (const struct filter_u16_span *)
				names_rule->dst_port_ranges.items;
}

CLASSIFY_DEVICE_COMPILE(names_device, names_rule_get_devices)
CLASSIFY_NET4_COMPILE(names_net4_src, names_rule_get_net4_srcs)
CLASSIFY_NET4_COMPILE(names_net4_dst, names_rule_get_net4_dsts)
CLASSIFY_NET6_COMPILE(names_net6_src, names_rule_get_net6_srcs)
CLASSIFY_NET6_COMPILE(names_net6_dst, names_rule_get_net6_dsts)
CLASSIFY_LINE_COMPILE(
	names_vlan, struct classify_attr_line, 4096, names_rule_get_vlan_ranges
)
CLASSIFY_LINE_COMPILE(
	names_ipproto,
	struct classify_attr_line,
	0x100,
	names_rule_get_ipproto_ranges
)
CLASSIFY_IPFRAG_COMPILE(names_ipfrag, names_rule_get_fragment)
CLASSIFY_U16_RANGES_COMPILE(
	names_src_port,
	struct classify_attr_port,
	names_rule_get_src_port_ranges
)
CLASSIFY_U16_RANGES_COMPILE(
	names_dst_port,
	struct classify_attr_port,
	names_rule_get_dst_port_ranges
)

// One fixed net6 helper: a masked address built from a prefix length.
static struct net6
net6_cidr(const uint8_t addr[NET6_LEN], int prefix_len) {
	struct net6 net;
	memcpy(net.addr, addr, NET6_LEN);
	memset(net.mask, 0, NET6_LEN);
	for (int idx = 0; idx < prefix_len; ++idx) {
		net.mask[idx / 8] |= 0x80 >> (idx % 8);
	}
	return net;
}

static struct net4
net4_cidr(const uint8_t addr[NET4_LEN], int prefix_len) {
	struct net4 net;
	memcpy(net.addr, addr, NET4_LEN);
	memset(net.mask, 0, NET4_LEN);
	for (int idx = 0; idx < prefix_len; ++idx) {
		net.mask[idx / 8] |= 0x80 >> (idx % 8);
	}
	return net;
}

/*
 * The composition of the test: the embedded attributes of both
 * families, the joint tables chaining their stages, and the rule maps
 * of the decoders. Held together so the teardown can release
 * everything through each owner's free.
 */
struct names_composition {
	struct classify_attr_device dev_attr;
	struct classify_attr_net4 n4s_attr;
	struct classify_attr_net4 n4d_attr;
	struct classify_attr_net6 n6s_attr;
	struct classify_attr_net6 n6d_attr;
	struct classify_attr_line vlan_attr;
	struct classify_attr_line ipproto_attr;
	struct classify_attr_ipfrag frag_attr;
	struct classify_attr_port psrc_attr;
	struct classify_attr_port pdst_attr;

	struct value_table nets4;
	struct value_table mid4;
	struct value_table proto4;
	struct value_table root4;
	struct value_table nets6;
	struct value_table ports;

	struct classifier stage_dev;
	struct classifier stage_n4s;
	struct classifier stage_n4d;
	struct classifier stage_nets4;
	struct classifier stage_mid4;
	struct classifier stage_ipproto;
	struct classifier stage_proto4;
	struct classifier stage_frag;
	struct classifier stage_root4;
	struct classifier stage_n6s;
	struct classifier stage_n6d;
	struct classifier stage_nets6;
	struct classifier stage_vlan;
	struct classifier stage_psrc;
	struct classifier stage_pdst;
	struct classifier stage_ports;

	struct vline rule_map4;
	struct vline rule_map6;
};

// Fills the two rules of the composition: every attribute view is set,
// so each compile entry builds real regions and each join real pairs.
static void
make_rules(struct names_rule rules[2]) {
	static struct filter_device devices[2] = {{"port0", 0}, {"port3", 3}};
	static struct net4 net4_nets[4] = {0};
	static struct net6 net6_nets[4] = {0};
	static struct filter_port_range port_ranges[4] = {
		{1000, 2000},
		{2000, 3000},
		{1000, 1500},
		{2500, 3000},
	};
	static const struct classify_line_range vlan_of_rule[2] = {
		{10, 10}, {20, 20}
	};
	static const struct classify_line_range ipproto_of_rule[2] = {
		{6, 6},
		{17, 17},
	};

	const uint8_t src4[NET4_LEN] = {10, 0, 0, 0};
	const uint8_t src4b[NET4_LEN] = {10, 2, 0, 0};
	const uint8_t dst4[NET4_LEN] = {10, 1, 0, 0};
	const uint8_t src6_prefix[NET6_LEN] = {0x2a, 0x02, 0x06, 0xb8};
	const uint8_t src6_host[NET6_LEN] = {0x2a, 0x02, 0x06, 0xb8};
	const uint8_t dst6_prefix[NET6_LEN] = {0xfe, 0x80};

	net4_nets[0] = net4_cidr(src4, 8);
	net4_nets[1] = net4_cidr(src4b, 16);
	net4_nets[2] = net4_cidr(dst4, 16);
	net4_nets[3] = net4_cidr(dst4, 16);

	// The second source carries a host address inside the /64 of the
	// first one, so the net6 compile splits the row and keeps the dense
	// join table alive.
	net6_nets[0] = net6_cidr(src6_prefix, 64);
	net6_nets[1] = net6_cidr(src6_host, 128);
	net6_nets[2] = net6_cidr(dst6_prefix, 64);
	net6_nets[3] = net6_cidr(dst6_prefix, 64);

	memset(rules, 0, sizeof(*rules) * 2);

	rules[0].devices.items = devices;
	rules[0].devices.count = 1;
	rules[1].devices.items = devices + 1;
	rules[1].devices.count = 1;

	rules[0].net4_srcs.items = net4_nets;
	rules[0].net4_srcs.count = 1;
	rules[1].net4_srcs.items = net4_nets + 1;
	rules[1].net4_srcs.count = 1;
	rules[0].net4_dsts.items = net4_nets + 2;
	rules[0].net4_dsts.count = 1;
	rules[1].net4_dsts.items = net4_nets + 3;
	rules[1].net4_dsts.count = 1;

	rules[0].net6_srcs.items = net6_nets;
	rules[0].net6_srcs.count = 1;
	rules[1].net6_srcs.items = net6_nets + 1;
	rules[1].net6_srcs.count = 1;
	rules[0].net6_dsts.items = net6_nets + 2;
	rules[0].net6_dsts.count = 1;
	rules[1].net6_dsts.items = net6_nets + 3;
	rules[1].net6_dsts.count = 1;

	rules[0].vlan_ranges.items = vlan_of_rule;
	rules[0].vlan_ranges.count = 1;
	rules[1].vlan_ranges.items = vlan_of_rule + 1;
	rules[1].vlan_ranges.count = 1;

	rules[0].ipproto_ranges.items = ipproto_of_rule;
	rules[0].ipproto_ranges.count = 1;
	rules[1].ipproto_ranges.items = ipproto_of_rule + 1;
	rules[1].ipproto_ranges.count = 1;

	rules[0].src_port_ranges.items = port_ranges;
	rules[0].src_port_ranges.count = 1;
	rules[1].src_port_ranges.items = port_ranges + 2;
	rules[1].src_port_ranges.count = 1;
	rules[0].dst_port_ranges.items = port_ranges + 1;
	rules[0].dst_port_ranges.count = 1;
	rules[1].dst_port_ranges.items = port_ranges + 3;
	rules[1].dst_port_ranges.count = 1;

	rules[0].fragment = FILTER_IP_FRAG_NONE;
	rules[1].fragment = FILTER_IP_FRAG_FRAG;
}

/*
 * Compiles the whole composition under one root context, with family
 * scoped names the way the modules pass them: the ip4 chain through
 * the fragment root, the ip6 network pair, the shared device side, and
 * the ports joint. Every entry keeps its stage only to chain the next
 * join or decoder; the rule maps resolve the two projections.
 *
 * Returns 0 on success, negative on the first failing entry, with the
 * composition left zeroed - a failed build leaves nothing behind.
 */
static int
build_composition(
	struct memory_context *mctx,
	const struct classifier_rule **rule_ptrs,
	struct names_composition *comp
) {
	memset(comp, 0, sizeof(*comp));

	if (classify_names_device_compile(
		    mctx,
		    "l2:device",
		    rule_ptrs,
		    2,
		    &comp->dev_attr,
		    &comp->stage_dev
	    )) {
		return -1;
	}
	if (classify_names_net4_src_compile(
		    mctx,
		    "ip4:net4_src",
		    rule_ptrs,
		    2,
		    &comp->n4s_attr,
		    &comp->stage_n4s
	    )) {
		return -1;
	}
	if (classify_names_net4_dst_compile(
		    mctx,
		    "ip4:net4_dst",
		    rule_ptrs,
		    2,
		    &comp->n4d_attr,
		    &comp->stage_n4d
	    )) {
		return -1;
	}
	if (classify_join(
		    mctx,
		    "ip4:nets_joint",
		    &comp->stage_n4s,
		    &comp->stage_n4d,
		    2,
		    &comp->nets4,
		    &comp->stage_nets4
	    )) {
		return -1;
	}
	if (classify_join(
		    mctx,
		    "ip4:mid_joint",
		    &comp->stage_dev,
		    &comp->stage_nets4,
		    2,
		    &comp->mid4,
		    &comp->stage_mid4
	    )) {
		return -1;
	}
	if (classify_names_ipproto_compile(
		    mctx,
		    "ip4:ipproto",
		    rule_ptrs,
		    2,
		    &comp->ipproto_attr,
		    &comp->stage_ipproto
	    )) {
		return -1;
	}
	if (classify_join(
		    mctx,
		    "ip4:proto_joint",
		    &comp->stage_mid4,
		    &comp->stage_ipproto,
		    2,
		    &comp->proto4,
		    &comp->stage_proto4
	    )) {
		return -1;
	}
	if (classify_names_ipfrag_compile(
		    mctx,
		    "ip4:ipfrag",
		    rule_ptrs,
		    2,
		    &comp->frag_attr,
		    &comp->stage_frag
	    )) {
		return -1;
	}
	if (classify_join(
		    mctx,
		    "ip4:root_joint",
		    &comp->stage_proto4,
		    &comp->stage_frag,
		    2,
		    &comp->root4,
		    &comp->stage_root4
	    )) {
		return -1;
	}
	if (classify_decode(
		    mctx,
		    "ip4:rules",
		    &comp->stage_root4,
		    (const struct classifier_rule *const *)rule_ptrs,
		    2,
		    &comp->rule_map4
	    )) {
		return -1;
	}

	if (classify_names_net6_src_compile(
		    mctx,
		    "ip6:net6_src",
		    rule_ptrs,
		    2,
		    &comp->n6s_attr,
		    &comp->stage_n6s
	    )) {
		return -1;
	}
	if (classify_names_net6_dst_compile(
		    mctx,
		    "ip6:net6_dst",
		    rule_ptrs,
		    2,
		    &comp->n6d_attr,
		    &comp->stage_n6d
	    )) {
		return -1;
	}
	if (classify_join(
		    mctx,
		    "ip6:nets_joint",
		    &comp->stage_n6s,
		    &comp->stage_n6d,
		    2,
		    &comp->nets6,
		    &comp->stage_nets6
	    )) {
		return -1;
	}
	if (classify_decode(
		    mctx,
		    "ip6:rules",
		    &comp->stage_nets6,
		    (const struct classifier_rule *const *)rule_ptrs,
		    2,
		    &comp->rule_map6
	    )) {
		return -1;
	}

	if (classify_names_vlan_compile(
		    mctx,
		    "l2:vlan",
		    rule_ptrs,
		    2,
		    &comp->vlan_attr,
		    &comp->stage_vlan
	    )) {
		return -1;
	}
	if (classify_names_src_port_compile(
		    mctx,
		    "l4:src_port",
		    rule_ptrs,
		    2,
		    &comp->psrc_attr,
		    &comp->stage_psrc
	    )) {
		return -1;
	}
	if (classify_names_dst_port_compile(
		    mctx,
		    "l4:dst_port",
		    rule_ptrs,
		    2,
		    &comp->pdst_attr,
		    &comp->stage_pdst
	    )) {
		return -1;
	}
	if (classify_join(
		    mctx,
		    "l4:ports_joint",
		    &comp->stage_psrc,
		    &comp->stage_pdst,
		    2,
		    &comp->ports,
		    &comp->stage_ports
	    )) {
		return -1;
	}

	return 0;
}

// Releases the composition through each owner's free, in the reverse
// order of the build.
static void
free_composition(struct memory_context *mctx, struct names_composition *comp) {
	classifier_fini(&comp->stage_ports, mctx, 2);
	value_table_free(&comp->ports);
	classifier_fini(&comp->stage_pdst, mctx, 2);
	classifier_fini(&comp->stage_psrc, mctx, 2);
	classify_attr_port_free(mctx, &comp->psrc_attr);
	classify_attr_port_free(mctx, &comp->pdst_attr);
	classifier_fini(&comp->stage_vlan, mctx, 2);
	classify_attr_line_free(mctx, &comp->vlan_attr);
	vline_free(&comp->rule_map6);
	classifier_fini(&comp->stage_nets6, mctx, 2);
	value_table_free(&comp->nets6);
	classify_attr_net6_free(mctx, &comp->n6s_attr);
	classify_attr_net6_free(mctx, &comp->n6d_attr);
	vline_free(&comp->rule_map4);
	classifier_fini(&comp->stage_root4, mctx, 2);
	value_table_free(&comp->root4);
	classify_attr_ipfrag_free(mctx, &comp->frag_attr);
	classifier_fini(&comp->stage_frag, mctx, 2);
	classifier_fini(&comp->stage_proto4, mctx, 2);
	value_table_free(&comp->proto4);
	classifier_fini(&comp->stage_ipproto, mctx, 2);
	classify_attr_line_free(mctx, &comp->ipproto_attr);
	classifier_fini(&comp->stage_mid4, mctx, 2);
	value_table_free(&comp->mid4);
	classifier_fini(&comp->stage_nets4, mctx, 2);
	value_table_free(&comp->nets4);
	classify_attr_net4_free(mctx, &comp->n4s_attr);
	classify_attr_net4_free(mctx, &comp->n4d_attr);
	classify_attr_device_free(mctx, &comp->dev_attr);
	classifier_fini(&comp->stage_dev, mctx, 2);
	classifier_fini(&comp->stage_n4s, mctx, 2);
	classifier_fini(&comp->stage_n4d, mctx, 2);
	classifier_fini(&comp->stage_n6s, mctx, 2);
	classifier_fini(&comp->stage_n6d, mctx, 2);
}

#define CTX_NAMES_ARENA_SIZE (1 << 24)

// Shared per-case fixture: a root memory_context over a fresh block
// allocator, the two rules, and the allocator's free-byte count
// captured before the composition is built.
struct ctx_names_fixture {
	struct block_allocator allocator;
	struct memory_context mctx;
	struct names_rule rules[2];
	const struct classifier_rule *rule_ptrs[2];
	struct names_composition comp;
	size_t free_before;
};

// Builds the fixture: the root context, the rules and their pointer
// row, and the free-byte anchor for the teardown balance check.
static int
fixture_init(struct ctx_names_fixture *fx, void *arena) {
	block_allocator_init(&fx->allocator);
	block_allocator_put_arena(&fx->allocator, arena, CTX_NAMES_ARENA_SIZE);
	if (memory_context_init(&fx->mctx, "test", &fx->allocator)) {
		return -1;
	}
	make_rules(fx->rules);
	fx->rule_ptrs[0] = &fx->rules[0].rule;
	fx->rule_ptrs[1] = &fx->rules[1].rule;
	fx->free_before = block_allocator_free_size(&fx->allocator);
	return 0;
}

static int
fixture_build(struct ctx_names_fixture *fx) {
	return build_composition(&fx->mctx, fx->rule_ptrs, &fx->comp);
}

static int
fixture_check_balance(struct ctx_names_fixture *fx) {
	TEST_ASSERT_EQUAL(
		block_allocator_free_size(&fx->allocator),
		fx->free_before,
		"arena free size mismatch after the composition free"
	);
	return TEST_SUCCESS;
}

static void
fixture_fini(struct ctx_names_fixture *fx) {
	memory_context_fini(&fx->mctx);
}

static struct memory_context *
ctx_child(struct memory_context *ctx) {
	return ADDR_OF(&ctx->first_child);
}

static struct memory_context *
ctx_sibling(struct memory_context *ctx) {
	return ADDR_OF(&ctx->next_sibling);
}

// Counts the direct children of ctx named name.
static int
direct_child_count(struct memory_context *ctx, const char *name) {
	int count = 0;
	for (struct memory_context *child = ctx_child(ctx); child != NULL;
	     child = ctx_sibling(child)) {
		if (strcmp(child->name, name) == 0) {
			++count;
		}
	}
	return count;
}

// Recursively checks that no two siblings, at any depth under ctx,
// share a name.
static int
no_duplicate_siblings(struct memory_context *ctx) {
	for (struct memory_context *child = ctx_child(ctx); child != NULL;
	     child = ctx_sibling(child)) {
		for (struct memory_context *sibling = ctx_sibling(child);
		     sibling != NULL;
		     sibling = ctx_sibling(sibling)) {
			TEST_ASSERT(
				strcmp(child->name, sibling->name) != 0,
				"duplicate sibling name '%s' under '%s'",
				child->name,
				ctx->name
			);
		}
		TEST_ASSERT_SUCCESS(
			no_duplicate_siblings(child),
			"duplicate siblings under '%s'",
			child->name
		);
	}
	return TEST_SUCCESS;
}

// Verifies that no two siblings anywhere under the composition's root
// share a name, for both families and every joint between them.
static int
test_no_sibling_name_collision(void *arena) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena), "failed to init root memory context"
	);
	TEST_ASSERT_SUCCESS(fixture_build(&fx), "failed to build composition");

	TEST_ASSERT_SUCCESS(
		no_duplicate_siblings(&fx.mctx),
		"sibling name collision under the composition"
	);

	free_composition(&fx.mctx, &fx.comp);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the composition"
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

// Verifies that a joint leaves exactly one table and one registry
// child behind, leaf named after the joint, so the two allocations of
// one join are told apart in the memory tree.
static int
test_joint_children_are_leaf_named(void *arena) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena), "failed to init root memory context"
	);
	TEST_ASSERT_SUCCESS(fixture_build(&fx), "failed to build composition");

	static const char *joints[] = {
		"ip4:nets_joint",
		"ip4:mid_joint",
		"ip4:proto_joint",
		"ip4:root_joint",
		"ip6:nets_joint",
		"l4:ports_joint",
	};
	for (size_t i = 0; i < sizeof(joints) / sizeof(joints[0]); ++i) {
		char table_name[MEMORY_CONTEXT_NAME_SIZE];
		char registry_name[MEMORY_CONTEXT_NAME_SIZE];
		classify_leaf_name(
			table_name, sizeof(table_name), joints[i], "table"
		);
		classify_leaf_name(
			registry_name,
			sizeof(registry_name),
			joints[i],
			"registry"
		);
		TEST_ASSERT_EQUAL(
			direct_child_count(&fx.mctx, table_name),
			1,
			"joint '%s' is not a table child exactly once",
			joints[i]
		);
		TEST_ASSERT_EQUAL(
			direct_child_count(&fx.mctx, registry_name),
			1,
			"joint '%s' is not a registry child exactly once",
			joints[i]
		);
	}

	free_composition(&fx.mctx, &fx.comp);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the composition"
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

// Verifies that every attribute compile leaves its artifact and its
// stage registry behind as leaf named children of the entry name.
static int
test_attribute_children_are_leaf_named(void *arena) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena), "failed to init root memory context"
	);
	TEST_ASSERT_SUCCESS(fixture_build(&fx), "failed to build composition");

	static const char *leaf_names[] = {
		"l2:device:line",	 "l2:device:registry",
		"l2:vlan:line",		 "l2:vlan:registry",
		"ip4:net4_src:lpm",	 "ip4:net4_src:registry",
		"ip4:net4_dst:lpm",	 "ip4:net4_dst:registry",
		"ip4:ipproto:line",	 "ip4:ipproto:registry",
		"ip4:ipfrag:table",	 "ip4:ipfrag:registry",
		"ip6:net6_src:hi",	 "ip6:net6_src:lo",
		"ip6:net6_src:comb",	 "ip6:net6_src:registry",
		"ip6:net6_dst:hi",	 "ip6:net6_dst:lo",
		"ip6:net6_dst:registry", "l4:src_port:line",
		"l4:src_port:registry",	 "l4:dst_port:line",
		"l4:dst_port:registry",
	};
	for (size_t i = 0; i < sizeof(leaf_names) / sizeof(leaf_names[0]);
	     ++i) {
		TEST_ASSERT_EQUAL(
			direct_child_count(&fx.mctx, leaf_names[i]),
			1,
			"'%s' is not a direct child exactly once",
			leaf_names[i]
		);
	}

	free_composition(&fx.mctx, &fx.comp);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the composition"
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

// Verifies that the decoder keeps the plain entry name for its rule
// map: a decode leaves a single context behind, so no leaf is needed
// to tell siblings apart.
static int
test_decode_rule_map_keeps_entry_name(void *arena) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena), "failed to init root memory context"
	);
	TEST_ASSERT_SUCCESS(fixture_build(&fx), "failed to build composition");

	TEST_ASSERT_EQUAL(
		direct_child_count(&fx.mctx, "ip4:rules"),
		1,
		"the ip4 rule map is not a direct child exactly once"
	);
	TEST_ASSERT_EQUAL(
		direct_child_count(&fx.mctx, "ip6:rules"),
		1,
		"the ip6 rule map is not a direct child exactly once"
	);

	free_composition(&fx.mctx, &fx.comp);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the composition"
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

// Verifies that freeing the composition leaves the root context
// childless and hands the allocator back every byte the build took.
static int
test_teardown_leaves_nothing_behind(void *arena) {
	struct ctx_names_fixture fx;
	TEST_ASSERT_SUCCESS(
		fixture_init(&fx, arena), "failed to init root memory context"
	);
	TEST_ASSERT_SUCCESS(fixture_build(&fx), "failed to build composition");

	free_composition(&fx.mctx, &fx.comp);

	TEST_ASSERT_NULL(
		ctx_child(&fx.mctx), "root context still has children"
	);
	TEST_ASSERT_SUCCESS(
		fixture_check_balance(&fx), "leak freeing the composition"
	);
	fixture_fini(&fx);
	return TEST_SUCCESS;
}

int
main() {
	log_enable_name("debug");
	void *arena = malloc(CTX_NAMES_ARENA_SIZE);
	int failed = 0;

	struct {
		const char *name;
		int (*func)(void *);
	} cases[] = {
		{"no_sibling_name_collision", test_no_sibling_name_collision},
		{"joint_children_are_leaf_named",
		 test_joint_children_are_leaf_named},
		{"attribute_children_are_leaf_named",
		 test_attribute_children_are_leaf_named},
		{"decode_rule_map_keeps_entry_name",
		 test_decode_rule_map_keeps_entry_name},
		{"teardown_leaves_nothing_behind",
		 test_teardown_leaves_nothing_behind},
	};

	for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); ++i) {
		// The previous case's frees left the freed blocks
		// ASan-poisoned, so the arena needs unpoisoning before this
		// case's fill can touch it.
		asan_unpoison_memory_region(arena, CTX_NAMES_ARENA_SIZE);
		memset(arena, 0, CTX_NAMES_ARENA_SIZE);
		if (cases[i].func(arena) != TEST_SUCCESS) {
			failed = 1;
			continue;
		}
	}

	free(arena);

	if (failed) {
		return 1;
	}
	return 0;
}
