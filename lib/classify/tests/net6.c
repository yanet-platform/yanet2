// Runtime classification test for the net6 attribute of the
// region-based classification library.
//
// Pins the regression of combining the high and the low address halves
// through the shared classification: unique networks grouped across rules,
// masks spanning both halves, networks shared and nested across rules, a
// wildcard network, and the source with destination joint. Every scenario
// compiles an explicit classifier - the destination attribute alone, or
// the source and destination pair with their joint - decodes its ruleset
// and classifies synthetic packets through the packet getters and the
// library lookup core.

#include "lib/classify/compiler/net6.h"
#include "lib/classify/classify.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/query.h"

#include "common/memory.h"
#include "common/network.h"

#include "lib/classify/classifiers/net6.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

#include "lib/utils/packet.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

#include <assert.h>
#include <netinet/in.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// Reads the destination address of every packet of the batch out of
// its network header.
static void
test_get_net6_dst_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET6_LEN, ipv6_hdr->dst_addr, NET6_LEN);
	}
}

// Reads the source address of every packet of the batch out of its
// network header.
static void
test_get_net6_src_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET6_LEN, ipv6_hdr->src_addr, NET6_LEN);
	}
}

// The test rule: the network pair of the classified scenarios, the
// rule format the compilers never see beyond its getters.
struct test_rule {
	struct classifier_rule rule;
	struct filter_net6s srcs;
	struct filter_net6s dsts;
};

static inline void
test_rule_get_srcs(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	*nets = test_rule->srcs;
}

static inline void
test_rule_get_dsts(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	*nets = test_rule->dsts;
}

CLASSIFY_NET6_COMPILE(test_net6_src, test_rule_get_srcs)
CLASSIFY_NET6_COMPILE(test_net6_dst, test_rule_get_dsts)

/*
 * The pair classifier of the source with destination scenarios: the
 * two address attributes joined through their joint.
 */
struct test_classifier_net6 {
	struct classify_attr_net6 src_attr;
	struct classify_attr_net6 dst_attr;
	struct value_table joint;
};

// Builds a network of the given address with a mask of hi_bytes leading
// bytes in the high half and lo_bytes leading bytes in the low one.
static struct net6
net6_halves(const uint8_t addr[NET6_LEN], uint8_t hi_bytes, uint8_t lo_bytes) {
	struct net6 net = {0};
	memcpy(net.addr, addr, NET6_LEN);
	memset(net.mask, 0xff, hi_bytes);
	memset(net.mask + 8, 0xff, lo_bytes);
	return net;
}

// Fills a rule matching packets of the given destination networks.
static struct test_rule
rule_dst(struct net6 *nets, uint32_t count) {
	struct test_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.dsts.items = nets;
	rule.dsts.count = count;
	return rule;
}

// Fills a rule matching packets of the given source and destination
// networks.
static struct test_rule
rule_src_dst(
	struct net6 *srcs,
	uint32_t src_count,
	struct net6 *dsts,
	uint32_t dst_count
) {
	struct test_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.srcs.items = srcs;
	rule.srcs.count = src_count;
	rule.dsts.items = dsts;
	rule.dsts.count = dst_count;
	return rule;
}

/*
 * Compiled destination classifier of the destination only scenarios:
 * the attribute with the decoder of its ruleset.
 */
struct test_filter_net6_dst {
	struct classify_attr_net6 dst_attr;
	struct vline rule_map;
};

/*
 * Compiles the destination classifier of a ruleset and decodes its
 * rules; the registries and the rule group row are compile scratch,
 * released before the routine returns.
 */
static void
test_filter_net6_dst_compile(
	struct memory_context *mctx,
	const struct classifier_rule **rules,
	uint32_t rule_count,
	struct test_filter_net6_dst *flt
) {
	memset(flt, 0, sizeof(*flt));

	struct classifier stage_dst = {0};

	assert(classify_test_net6_dst_compile(
		       mctx, rules, rule_count, &flt->dst_attr, &stage_dst
	       ) == 0);
	assert(classify_decode(
		       mctx, &stage_dst, rules, rule_count, &flt->rule_map
	       ) == 0);

	classifier_fini(&stage_dst, mctx, rule_count);
}

// Releases the destination classifier; the decoder goes before the
// attribute it was decoded from.
static void
test_filter_net6_dst_free(
	struct memory_context *mctx, struct test_filter_net6_dst *flt
) {
	vline_free(&flt->rule_map);
	classify_attr_net6_free(mctx, &flt->dst_attr);
}

// Classifies one synthetic packet of the given address pair against
// the destination attribute and returns the resolved rule index.
static uint32_t
classify_dst(
	const struct test_filter_net6_dst *flt,
	const uint8_t src[NET6_LEN],
	const uint8_t dst[NET6_LEN]
) {
	struct packet packet = {0};
	assert(fill_packet_net6(&packet, src, dst, 100, 200, IPPROTO_UDP, 0) ==
	       0);
	struct packet *packet_ptr = &packet;

	uint8_t addrs[1][NET6_LEN];
	uint32_t classes[1];
	uint32_t results[1];
	test_get_net6_dst_batch(
		(const struct packet **)&packet_ptr, addrs[0], 1
	);
	classify_net6_lookup(&flt->dst_attr, addrs[0], classes, 1);
	classify_resolve(&flt->rule_map, classes, results, 1);

	free_packet(&packet);
	return results[0];
}

/*
 * Compiled pair classifier of the source with destination scenarios:
 * the attributes with their joint and the decoder of the ruleset.
 */
struct test_filter_net6_both {
	struct test_classifier_net6 cls;
	struct vline rule_map;
};

/*
 * Compiles the pair classifier of a ruleset and decodes its rules; the
 * registries and the rule group rows are compile scratch, released
 * before the routine returns.
 */
static void
test_filter_net6_both_compile(
	struct memory_context *mctx,
	const struct classifier_rule **rules,
	uint32_t rule_count,
	struct test_filter_net6_both *flt
) {
	memset(flt, 0, sizeof(*flt));

	struct classifier stage_src = {0};
	struct classifier stage_dst = {0};
	struct classifier stage_pair = {0};

	assert(classify_test_net6_src_compile(
		       mctx, rules, rule_count, &flt->cls.src_attr, &stage_src
	       ) == 0);
	assert(classify_test_net6_dst_compile(
		       mctx, rules, rule_count, &flt->cls.dst_attr, &stage_dst
	       ) == 0);
	assert(classify_join(
		       mctx,
		       &stage_src,
		       &stage_dst,
		       rule_count,
		       &flt->cls.joint,
		       &stage_pair
	       ) == 0);
	assert(classify_decode(
		       mctx, &stage_pair, rules, rule_count, &flt->rule_map
	       ) == 0);

	classifier_fini(&stage_src, mctx, rule_count);
	classifier_fini(&stage_dst, mctx, rule_count);
	classifier_fini(&stage_pair, mctx, rule_count);
}

// Releases the pair classifier; the decoder goes before the classifier
// it was decoded from.
static void
test_filter_net6_both_free(
	struct memory_context *mctx, struct test_filter_net6_both *flt
) {
	vline_free(&flt->rule_map);
	classify_attr_net6_free(mctx, &flt->cls.src_attr);
	classify_attr_net6_free(mctx, &flt->cls.dst_attr);
	value_table_free(&flt->cls.joint);
}

// Classifies one synthetic packet of the given address pair against
// the source and destination pair classifier and returns the resolved
// rule index.
static uint32_t
classify_both(
	const struct test_filter_net6_both *flt,
	const uint8_t src[NET6_LEN],
	const uint8_t dst[NET6_LEN]
) {
	struct packet packet = {0};
	assert(fill_packet_net6(&packet, src, dst, 100, 200, IPPROTO_UDP, 0) ==
	       0);
	struct packet *packet_ptr = &packet;

	uint8_t src_addrs[1][NET6_LEN];
	uint8_t dst_addrs[1][NET6_LEN];
	uint32_t src_classes[1];
	uint32_t dst_classes[1];
	uint32_t classes[1];
	uint32_t results[1];
	test_get_net6_src_batch(
		(const struct packet **)&packet_ptr, src_addrs[0], 1
	);
	classify_net6_lookup(&flt->cls.src_attr, src_addrs[0], src_classes, 1);
	test_get_net6_dst_batch(
		(const struct packet **)&packet_ptr, dst_addrs[0], 1
	);
	classify_net6_lookup(&flt->cls.dst_attr, dst_addrs[0], dst_classes, 1);
	classify_joint_lookup(
		&flt->cls.joint, src_classes, dst_classes, classes, 1
	);
	classify_resolve(&flt->rule_map, classes, results, 1);

	free_packet(&packet);
	return results[0];
}

// Runs one scenario on a fresh block allocator over the shared arena.
typedef void (*scenario_func)(struct memory_context *, void *);

static void
run_scenario(scenario_func scenario, void *memory) {
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context mctx;
	assert(memory_context_init(&mctx, "test", &allocator) == 0);

	scenario(&mctx, memory);

	memory_context_fini(&mctx);
}

// Verifies that a mask spanning both halves matches exactly the addresses
// inside its intervals, with the skipped bytes of each half free.
static void
run_mask_split_test(struct memory_context *mctx, void *memory) {
	(void)memory;

	uint8_t base[NET6_LEN] = {0};
	base[0] = 0x20;
	base[1] = 0x01;
	base[2] = 0x0d;
	base[3] = 0xb8;
	base[4] = 0x11;
	base[9] = 0x55;

	struct net6 nets[1] = {net6_halves(base, 5, 3)};
	struct test_rule rule = rule_dst(nets, 1);
	const struct classifier_rule *rules[1] = {&rule.rule};

	struct test_filter_net6_dst flt;
	test_filter_net6_dst_compile(mctx, rules, 1, &flt);

	uint8_t src[NET6_LEN] = {0};

	// the same address
	assert(classify_dst(&flt, src, base) == 0);
	// the skipped high half bytes are free
	uint8_t inside_hi[NET6_LEN];
	memcpy(inside_hi, base, NET6_LEN);
	inside_hi[5] = 0xff;
	inside_hi[7] = 0x99;
	assert(classify_dst(&flt, src, inside_hi) == 0);
	// the skipped low half bytes are free
	uint8_t inside_lo[NET6_LEN];
	memcpy(inside_lo, base, NET6_LEN);
	inside_lo[11] = 0xff;
	inside_lo[15] = 0x77;
	assert(classify_dst(&flt, src, inside_lo) == 0);
	// an address outside the masked high interval
	uint8_t outside_hi[NET6_LEN];
	memcpy(outside_hi, base, NET6_LEN);
	outside_hi[4] = 0x12;
	assert(classify_dst(&flt, src, outside_hi) == CLASSIFY_RULE_INVALID);
	// an address outside the masked low interval
	uint8_t outside_lo[NET6_LEN];
	memcpy(outside_lo, base, NET6_LEN);
	outside_lo[9] = 0x56;
	assert(classify_dst(&flt, src, outside_lo) == CLASSIFY_RULE_INVALID);

	test_filter_net6_dst_free(mctx, &flt);
}

// Verifies that a network shared by several rules is grouped once and
// every packet resolves to the first rule covering it.
static void
run_shared_networks_test(struct memory_context *mctx, void *memory) {
	(void)memory;

	uint8_t addr_n1[NET6_LEN] = {0};
	addr_n1[0] = 0xbb;
	uint8_t addr_n2[NET6_LEN] = {0};
	addr_n2[0] = 0xcc;

	struct net6 n1 = net6_halves(addr_n1, 1, 0);
	struct net6 n2 = net6_halves(addr_n2, 1, 0);

	struct net6 rule0_nets[1] = {n1};
	struct test_rule rule0 = rule_dst(rule0_nets, 1);

	struct net6 rule1_nets[2] = {n1, n2};
	struct test_rule rule1 = rule_dst(rule1_nets, 2);

	struct net6 rule2_nets[1] = {n2};
	struct test_rule rule2 = rule_dst(rule2_nets, 1);

	// holds the exact value of the first rule
	struct net6 rule3_nets[1] = {n1};
	struct test_rule rule3 = rule_dst(rule3_nets, 1);

	const struct classifier_rule *rules[4] = {
		&rule0.rule, &rule1.rule, &rule2.rule, &rule3.rule
	};

	struct test_filter_net6_dst flt;
	test_filter_net6_dst_compile(mctx, rules, 4, &flt);

	uint8_t src[NET6_LEN] = {0};

	assert(classify_dst(&flt, src, addr_n1) == 0);
	assert(classify_dst(&flt, src, addr_n2) == 1);

	uint8_t other[NET6_LEN] = {0};
	other[0] = 0xdd;
	assert(classify_dst(&flt, src, other) == CLASSIFY_RULE_INVALID);

	test_filter_net6_dst_free(mctx, &flt);
}

// Verifies that nested networks split the domain and a wildcard network
// closes it, with the first matching rule winning.
static void
run_nested_networks_test(struct memory_context *mctx, void *memory) {
	(void)memory;

	uint8_t addr_specific[NET6_LEN] = {0};
	addr_specific[0] = 0x20;
	addr_specific[1] = 0x01;
	addr_specific[2] = 0x0d;
	addr_specific[3] = 0xb8;
	addr_specific[4] = 0x00;
	addr_specific[5] = 0x01;

	uint8_t addr_broad[NET6_LEN] = {0};
	addr_broad[0] = 0x20;
	addr_broad[1] = 0x01;
	addr_broad[2] = 0x0d;
	addr_broad[3] = 0xb8;

	struct net6 specific = net6_halves(addr_specific, 6, 0);
	struct net6 broad = net6_halves(addr_broad, 4, 0);
	struct net6 any = net6_halves(addr_broad, 0, 0);

	struct net6 rule0_nets[1] = {specific};
	struct test_rule rule0 = rule_dst(rule0_nets, 1);
	struct net6 rule1_nets[1] = {broad};
	struct test_rule rule1 = rule_dst(rule1_nets, 1);
	struct net6 rule2_nets[1] = {any};
	struct test_rule rule2 = rule_dst(rule2_nets, 1);

	const struct classifier_rule *rules[3] = {
		&rule0.rule, &rule1.rule, &rule2.rule
	};

	struct test_filter_net6_dst flt;
	test_filter_net6_dst_compile(mctx, rules, 3, &flt);

	uint8_t src[NET6_LEN] = {0};

	// inside both the specific and the broad network
	assert(classify_dst(&flt, src, addr_specific) == 0);
	// inside the broad network only
	uint8_t broad_only[NET6_LEN];
	memcpy(broad_only, addr_broad, NET6_LEN);
	broad_only[5] = 0x99;
	assert(classify_dst(&flt, src, broad_only) == 1);
	// outside every masked network, inside the wildcard one
	uint8_t other[NET6_LEN] = {0};
	other[0] = 0xdd;
	assert(classify_dst(&flt, src, other) == 2);

	test_filter_net6_dst_free(mctx, &flt);
}

// Verifies that the source and destination attributes join into the
// combined classes and an unmatched half pair resolves to no rule.
static void
run_src_dst_test(struct memory_context *mctx, void *memory) {
	(void)memory;

	uint8_t addr_s1[NET6_LEN] = {0};
	addr_s1[0] = 0xbb;
	uint8_t addr_s2[NET6_LEN] = {0};
	addr_s2[0] = 0xcc;
	uint8_t addr_d1[NET6_LEN] = {0};
	addr_d1[0] = 0x11;
	uint8_t addr_d2[NET6_LEN] = {0};
	addr_d2[0] = 0x22;

	struct net6 s1 = net6_halves(addr_s1, 1, 0);
	struct net6 s2 = net6_halves(addr_s2, 1, 0);
	struct net6 d1 = net6_halves(addr_d1, 1, 0);
	struct net6 d2 = net6_halves(addr_d2, 1, 0);

	struct test_rule rule0 = rule_src_dst(&s1, 1, &d1, 1);
	struct test_rule rule1 = rule_src_dst(&s1, 1, &d2, 1);
	struct test_rule rule2 = rule_src_dst(&s2, 1, &d1, 1);

	const struct classifier_rule *rules[3] = {
		&rule0.rule, &rule1.rule, &rule2.rule
	};

	struct test_filter_net6_both flt;
	test_filter_net6_both_compile(mctx, rules, 3, &flt);

	assert(classify_both(&flt, addr_s1, addr_d1) == 0);
	assert(classify_both(&flt, addr_s1, addr_d2) == 1);
	assert(classify_both(&flt, addr_s2, addr_d1) == 2);
	assert(classify_both(&flt, addr_s2, addr_d2) == CLASSIFY_RULE_INVALID);

	test_filter_net6_both_free(mctx, &flt);
}

int
main(void) {
	void *memory = malloc(1 << 24);

	run_scenario(run_mask_split_test, memory);
	run_scenario(run_shared_networks_test, memory);
	run_scenario(run_nested_networks_test, memory);
	run_scenario(run_src_dst_test, memory);

	free(memory);
	return 0;
}
