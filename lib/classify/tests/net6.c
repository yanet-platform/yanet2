// Runtime classification test for the net6 attribute of the
// region-based classification library.
//
// Pins the regression of combining the high and the low address halves
// through the shared classification: unique networks grouped across rules,
// masks spanning both halves, networks shared and nested across rules, a
// wildcard network, the source with destination joint, and the high half
// trie built once with its final values matching the former build with
// the region ordinals overwritten by those values. Every classification
// scenario compiles an explicit classifier - the destination attribute
// alone, or the source and destination pair with their joint - decodes
// its ruleset and classifies synthetic packets through the packet getters
// and the library lookup core. One more scenario pins the unwind of a
// failed attribute commit.

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
		       mctx,
		       "test_net6_dst",
		       rules,
		       rule_count,
		       &flt->dst_attr,
		       &stage_dst
	       ) == 0);
	assert(classify_decode(
		       mctx,
		       "rule_map",
		       &stage_dst,
		       rules,
		       rule_count,
		       &flt->rule_map
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
		       mctx,
		       "test_net6_src",
		       rules,
		       rule_count,
		       &flt->cls.src_attr,
		       &stage_src
	       ) == 0);
	assert(classify_test_net6_dst_compile(
		       mctx,
		       "test_net6_dst",
		       rules,
		       rule_count,
		       &flt->cls.dst_attr,
		       &stage_dst
	       ) == 0);
	assert(classify_join(
		       mctx,
		       "joint",
		       &stage_src,
		       &stage_dst,
		       rule_count,
		       &flt->cls.joint,
		       &stage_pair
	       ) == 0);
	assert(classify_decode(
		       mctx,
		       "rule_map",
		       &stage_pair,
		       rules,
		       rule_count,
		       &flt->rule_map
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

// The context the commit below allocates from: an allocator without any
// arena, so the first allocation of the commit is refused.
static struct memory_context *starved_commit_context;

// Runs the attribute commit on the starved context instead of the
// compile one.
static int
starved_net6_commit(
	struct memory_context *memory_context, void *compile, void *attr
) {
	(void)memory_context;
	return classify_compile_net6_commit(
		starved_commit_context, compile, attr
	);
}

// Verifies that a commit refused its first allocation fails, frees
// nothing it did not allocate, and leaves the garbage-filled attribute
// and the stage zeroed.
static void
run_failed_commit_zeroes_attr_test(struct memory_context *mctx, void *memory) {
	(void)memory;

	struct block_allocator starved_allocator;
	block_allocator_init(&starved_allocator);
	struct memory_context starved_ctx;
	assert(memory_context_init(
		       &starved_ctx, "starved", &starved_allocator
	       ) == 0);
	starved_commit_context = &starved_ctx;

	struct block_allocator *allocator = ADDR_OF(&mctx->block_allocator);
	size_t initial = block_allocator_free_size(allocator);

	uint8_t addr[NET6_LEN] = {0};
	addr[0] = 0x20;
	struct net6 nets[1] = {net6_halves(addr, 2, 1)};
	struct test_rule rule = rule_dst(nets, 1);
	const struct classifier_rule *rules[1] = {&rule.rule};

	struct classify_compile_net6 *compile = classify_compile_net6_create(
		mctx, rules, 1, test_rule_get_dsts, "failed_commit"
	);
	assert(compile != NULL);
	const struct classify_attr_ops ops = {
		.size = classify_compile_net6_size,
		.rule_is_any = classify_compile_net6_rule_is_any,
		.hash = classify_compile_net6_hash,
		.compare = classify_compile_net6_compare,
		.iter = classify_compile_net6_iter,
		.rule_iter = classify_compile_net6_rule_iter,
		.commit = starved_net6_commit,
		.free_compile = classify_compile_net6_free,
	};

	struct classify_attr_net6 attr;
	memset(&attr, 0x5a, sizeof(attr));
	struct classifier stage;
	memset(&stage, 0x5a, sizeof(stage));
	assert(classify_attr_compile(
		       mctx,
		       "failed_commit",
		       &ops,
		       compile,
		       rules,
		       1,
		       &attr,
		       &stage
	       ) == -1);

	struct classify_attr_net6 zero_attr;
	memset(&zero_attr, 0, sizeof(zero_attr));
	assert(memcmp(&attr, &zero_attr, sizeof(attr)) == 0);
	struct classifier zero_stage;
	memset(&zero_stage, 0, sizeof(zero_stage));
	assert(memcmp(&stage, &zero_stage, sizeof(stage)) == 0);
	assert(starved_ctx.bfree_count == 0);
	assert(block_allocator_free_size(&starved_allocator) == 0);
	assert(block_allocator_free_size(allocator) == initial);

	memory_context_fini(&starved_ctx);
}

/*
 * Reference of the high half trie of one compile: the partition range
 * starts captured before the commit releases them and a trie built over
 * them twice, first with the region ordinals and then with the values
 * the commit resolved.
 */
struct test_hi_reference {
	struct lpm lpm;
	uint8_t (*starts)[8];
	uint32_t count;
};

// The commit wrapper below has no data argument of its own.
static struct test_hi_reference test_hi_ref;

// Builds the ordinal pass of the reference out of the partition and
// commits the attribute.
static int
test_commit_with_hi_reference(
	struct memory_context *mctx, void *compile, void *attr
) {
	struct classify_compile_net6 *net6_compile = compile;
	struct test_hi_reference *ref = &test_hi_ref;

	const struct classify_partition *part_hi = &net6_compile->part_hi;

	assert(lpm_init(&ref->lpm, mctx, "hi_ref") == 0);
	assert(classify_partition_build_lpm(
		       part_hi, part_hi->values, &ref->lpm
	       ) == 0);

	ref->starts = realloc(ref->starts, (size_t)part_hi->count * 8);
	assert(ref->starts != NULL);
	for (uint32_t idx = 0; idx < part_hi->count; ++idx) {
		classify_key_store(part_hi->starts[idx], 64, ref->starts[idx]);
	}
	ref->count = part_hi->count;

	return classify_compile_net6_commit(mctx, compile, attr);
}

// Overwrites every partition range of the reference with the value the
// committed trie holds at the range start.
static void
test_hi_reference_revalue(struct test_hi_reference *ref, const struct lpm *hi) {
	for (uint32_t idx = 0; idx < ref->count; ++idx) {
		uint8_t to[8];
		if (idx + 1 < ref->count) {
			memcpy(to, ref->starts[idx + 1], 8);
			filter_key_dec(8, to);
		} else {
			memset(to, 0xff, 8);
		}
		uint32_t value = lpm_lookup(hi, 8, ref->starts[idx]);
		assert(lpm_insert(&ref->lpm, 8, ref->starts[idx], to, value) ==
		       0);
	}
}

// Returns the index of a page of the trie.
static uint32_t
test_lpm_page_index(const struct lpm *lpm, const struct lpm_page *page) {
	for (uint32_t idx = 0; idx < lpm->page_count; ++idx) {
		if (lpm_page(lpm, idx) == page) {
			return idx;
		}
	}
	assert(0);
	return 0;
}

// Asserts that two tries hold the same pages in the same order with the
// same leaf values and the same child page indices.
static void
assert_lpm_equal(const struct lpm *first, const struct lpm *second) {
	assert(first->page_count == second->page_count);

	for (uint32_t page_idx = 0; page_idx < first->page_count; ++page_idx) {
		struct lpm_page *first_page = lpm_page(first, page_idx);
		struct lpm_page *second_page = lpm_page(second, page_idx);
		for (uint32_t slot = 0; slot < 256; ++slot) {
			union lpm_value *first_value =
				first_page->values + slot;
			union lpm_value *second_value =
				second_page->values + slot;
			if (first_value->value & LPM_VALUE_FLAG) {
				assert(first_value->value == second_value->value
				);
				continue;
			}
			assert(!(second_value->value & LPM_VALUE_FLAG));
			assert(test_lpm_page_index(
				       first, ADDR_OF(&first_value->page)
			       ) ==
			       test_lpm_page_index(
				       second, ADDR_OF(&second_value->page)
			       ));
		}
	}
}

// Returns the next value of a fixed seed generator.
static uint32_t
test_rand(uint64_t *state) {
	*state = *state * 6364136223846793005ull + 1442695040888963407ull;
	return (uint32_t)(*state >> 33);
}

// Builds a network of the given address masked to the leading hi_bits
// of the high half and the leading lo_bits of the low one.
static struct net6
net6_bits(const uint8_t addr[NET6_LEN], uint8_t hi_bits, uint8_t lo_bits) {
	struct net6 net = {0};
	for (uint8_t bit = 0; bit < hi_bits; ++bit) {
		net.mask[bit / 8] |= 0x80 >> (bit % 8);
	}
	for (uint8_t bit = 0; bit < lo_bits; ++bit) {
		net.mask[8 + bit / 8] |= 0x80 >> (bit % 8);
	}
	for (uint32_t idx = 0; idx < NET6_LEN; ++idx) {
		net.addr[idx] = addr[idx] & net.mask[idx];
	}
	return net;
}

// Returns the first rule network covering the address, the brute force
// oracle of a one network per rule ruleset.
static uint32_t
test_first_match(
	const struct net6 *nets, uint32_t count, const uint8_t addr[NET6_LEN]
) {
	for (uint32_t rule_idx = 0; rule_idx < count; ++rule_idx) {
		const struct net6 *net = nets + rule_idx;
		uint32_t idx = 0;
		while (idx < NET6_LEN &&
		       (addr[idx] & net->mask[idx]) == net->addr[idx]) {
			++idx;
		}
		if (idx == NET6_LEN) {
			return rule_idx;
		}
	}
	return CLASSIFY_RULE_INVALID;
}

#define TEST_HI_SINGLE_BUILD_RULES 96

// Verifies that the high half trie built in one pass with the resolved
// values equals the ordinal build overwritten with the same values, page
// by page, and classifies seeded overlapping prefixes as the oracle.
static void
run_hi_single_build_test(struct memory_context *mctx, void *memory) {
	(void)memory;

	uint64_t seed = 0x6e657436;
	uint8_t bytes[4] = {0x00, 0x01, 0x80, 0xff};

	struct net6 nets[TEST_HI_SINGLE_BUILD_RULES];
	struct test_rule rules[TEST_HI_SINGLE_BUILD_RULES];
	const struct classifier_rule *rule_ptrs[TEST_HI_SINGLE_BUILD_RULES];
	for (uint32_t idx = 0; idx < TEST_HI_SINGLE_BUILD_RULES; ++idx) {
		uint8_t addr[NET6_LEN] = {0x20, 0x01, 0x0d, 0xb8};
		for (uint32_t byte = 4; byte < NET6_LEN; ++byte) {
			addr[byte] = bytes[test_rand(&seed) % 4];
		}
		uint8_t hi_bits = 16 + test_rand(&seed) % 49;
		uint8_t lo_bits =
			test_rand(&seed) % 4 ? 0 : test_rand(&seed) % 65;
		nets[idx] = net6_bits(addr, hi_bits, lo_bits);
		rules[idx] = rule_dst(nets + idx, 1);
		rule_ptrs[idx] = &rules[idx].rule;
	}

	struct classify_compile_net6 *compile = classify_compile_net6_create(
		mctx,
		rule_ptrs,
		TEST_HI_SINGLE_BUILD_RULES,
		test_rule_get_dsts,
		"test_net6_dst"
	);
	assert(compile != NULL);
	const struct classify_attr_ops ops = {
		.size = classify_compile_net6_size,
		.rule_is_any = classify_compile_net6_rule_is_any,
		.hash = classify_compile_net6_hash,
		.compare = classify_compile_net6_compare,
		.iter = classify_compile_net6_iter,
		.rule_iter = classify_compile_net6_rule_iter,
		.commit = test_commit_with_hi_reference,
		.free_compile = classify_compile_net6_free,
	};

	struct test_filter_net6_dst flt;
	memset(&flt, 0, sizeof(flt));
	memset(&test_hi_ref, 0, sizeof(test_hi_ref));
	struct classifier stage_dst = {0};
	assert(classify_attr_compile(
		       mctx,
		       "test_net6_dst",
		       &ops,
		       compile,
		       rule_ptrs,
		       TEST_HI_SINGLE_BUILD_RULES,
		       &flt.dst_attr,
		       &stage_dst
	       ) == 0);
	assert(classify_decode(
		       mctx,
		       "rule_map",
		       &stage_dst,
		       rule_ptrs,
		       TEST_HI_SINGLE_BUILD_RULES,
		       &flt.rule_map
	       ) == 0);
	classifier_fini(&stage_dst, mctx, TEST_HI_SINGLE_BUILD_RULES);

	test_hi_reference_revalue(&test_hi_ref, &flt.dst_attr.hi);
	assert(test_hi_ref.lpm.page_count > 1);
	assert_lpm_equal(&flt.dst_attr.hi, &test_hi_ref.lpm);

	uint8_t src[NET6_LEN] = {0};
	for (uint32_t idx = 0; idx < TEST_HI_SINGLE_BUILD_RULES; ++idx) {
		uint8_t addr[NET6_LEN];
		memcpy(addr, nets[idx].addr, NET6_LEN);
		assert(classify_dst(&flt, src, addr) ==
		       test_first_match(nets, TEST_HI_SINGLE_BUILD_RULES, addr)
		);

		for (uint32_t byte = 4; byte < NET6_LEN; ++byte) {
			addr[byte] |= bytes[test_rand(&seed) % 4] &
				      ~nets[idx].mask[byte];
		}
		assert(classify_dst(&flt, src, addr) ==
		       test_first_match(nets, TEST_HI_SINGLE_BUILD_RULES, addr)
		);
	}

	lpm_free(&test_hi_ref.lpm);
	free(test_hi_ref.starts);
	test_filter_net6_dst_free(mctx, &flt);
}

int
main(void) {
	void *memory = malloc(1 << 24);

	run_scenario(run_mask_split_test, memory);
	run_scenario(run_shared_networks_test, memory);
	run_scenario(run_nested_networks_test, memory);
	run_scenario(run_src_dst_test, memory);
	run_scenario(run_failed_commit_zeroes_attr_test, memory);
	run_scenario(run_hi_single_build_test, memory);

	free(memory);
	return 0;
}
