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
// failed attribute commit, and the region scenarios pin the combined
// regions to the enumeration of every network cross, kept here as the
// reference.

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

// Appends one partition range start to the reference.
static int
test_hi_reference_collect(
	uint8_t key_size, const uint8_t *key, uint32_t value, void *data
) {
	(void)value;
	assert(key_size == 8);

	struct test_hi_reference *ref = data;
	ref->starts = realloc(ref->starts, (ref->count + 1) * 8);
	assert(ref->starts != NULL);
	memcpy(ref->starts[ref->count], key, 8);
	++ref->count;
	return 0;
}

// Builds the ordinal pass of the reference out of the partition and
// commits the attribute.
static int
test_commit_with_hi_reference(
	struct memory_context *mctx, void *compile, void *attr
) {
	struct classify_compile_net6 *net6_compile = compile;
	struct test_hi_reference *ref = &test_hi_ref;

	assert(lpm_init(&ref->lpm, mctx, "hi_ref") == 0);
	assert(range_index_build_lpm(&net6_compile->ri_hi, 8, &ref->lpm) == 0);
	assert(radix_walk(
		       &net6_compile->ri_hi.radix,
		       8,
		       test_hi_reference_collect,
		       ref
	       ) == 0);

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

/*
 * Combined regions of one compile assigned by the enumeration the
 * builder replaces: every network touches every cell of its cross in
 * network order, then walks the cross again to collect its range.
 */
struct test_regions_reference {
	uint8_t *row_split;
	struct value_table comb;
	struct vline uniform;
	struct value_registry registry;
	uint32_t region_count;
};

// Iterates the cells of a network cross, a uniform row as its line value.
static void
test_reference_iter(
	struct classify_compile_net6 *compile,
	struct test_regions_reference *ref,
	const struct net6 *net,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	uint32_t bounds[4];
	filter_net6_net_bounds(compile, net, bounds);

	const uint32_t *values_hi = ADDR_OF(&compile->ri_hi.values);
	const uint32_t *values_lo = ADDR_OF(&compile->ri_lo.values);
	const int full_lo = bounds[2] == 0 && bounds[3] == compile->ri_lo.count;

	for (uint32_t idx_hi = bounds[0]; idx_hi < bounds[1]; ++idx_hi) {
		const uint32_t row = values_hi[idx_hi];
		if (full_lo && !ref->row_split[row]) {
			assert(iter_cb_func(
				       vline_get_ptr(&ref->uniform, row),
				       cb_func_data
			       ) >= 0);
			continue;
		}
		for (uint32_t idx_lo = bounds[2]; idx_lo < bounds[3];
		     ++idx_lo) {
			assert(iter_cb_func(
				       value_table_get_ptr(
					       &ref->comb,
					       row,
					       values_lo[idx_lo]
				       ),
				       cb_func_data
			       ) >= 0);
		}
	}
}

// Collects a region value into the registry range being filled.
static int
test_reference_collect(uint32_t *value, void *data) {
	struct test_regions_reference *ref = data;
	assert(value_registry_collect(&ref->registry, *value) == 0);
	if (*value + 1 > ref->region_count) {
		ref->region_count = *value + 1;
	}
	return 0;
}

// Builds the reference over the partitions of a compile.
static void
test_regions_reference_build(
	struct memory_context *mctx,
	struct classify_compile_net6 *compile,
	struct test_regions_reference *ref
) {
	memset(ref, 0, sizeof(*ref));
	ref->row_split = calloc(compile->row_count, 1);
	assert(ref->row_split != NULL);
	assert(value_table_init(
		       &ref->comb,
		       mctx,
		       "ref_comb",
		       compile->row_count,
		       compile->ri_lo.max_value + 1
	       ) == 0);
	assert(vline_init(
		       &ref->uniform, mctx, "ref_rows", compile->row_count
	       ) == 0);

	const uint32_t *values_hi = ADDR_OF(&compile->ri_hi.values);
	for (uint32_t net_idx = 0; net_idx < compile->net_count; ++net_idx) {
		const struct net6 *net = compile->nets + net_idx;
		if (net6_is_any(net)) {
			continue;
		}
		uint32_t bounds[4];
		filter_net6_net_bounds(compile, net, bounds);
		if (bounds[2] == 0 && bounds[3] == compile->ri_lo.count) {
			continue;
		}
		for (uint32_t idx_hi = bounds[0]; idx_hi < bounds[1];
		     ++idx_hi) {
			ref->row_split[values_hi[idx_hi]] = 1;
		}
	}

	struct remap_table remap_table;
	assert(remap_table_init(
		       &remap_table,
		       mctx,
		       compile->row_count * (compile->ri_lo.max_value + 1) +
			       compile->row_count
	       ) == 0);
	for (uint32_t net_idx = 0; net_idx < compile->net_count; ++net_idx) {
		const struct net6 *net = compile->nets + net_idx;
		if (net6_is_any(net)) {
			continue;
		}
		remap_table_new_gen(&remap_table);
		test_reference_iter(
			compile, ref, net, classify_attr_touch, &remap_table
		);
	}
	remap_table_compact(&remap_table);
	value_table_compact(&ref->comb, &remap_table);
	for (uint32_t row = 0; row < compile->row_count; ++row) {
		uint32_t *value = vline_get_ptr(&ref->uniform, row);
		*value = remap_table_compacted(&remap_table, *value);
	}
	remap_table_free(&remap_table);

	assert(value_registry_init(&ref->registry, mctx, "ref_nets") == 0);
	ref->region_count = 1;
	for (uint32_t net_idx = 0; net_idx < compile->net_count; ++net_idx) {
		assert(value_registry_start(&ref->registry) == 0);
		const struct net6 *net = compile->nets + net_idx;
		if (net6_is_any(net)) {
			continue;
		}
		test_reference_iter(
			compile, ref, net, test_reference_collect, ref
		);
	}
}

static void
test_regions_reference_free(struct test_regions_reference *ref) {
	value_registry_fini(&ref->registry);
	vline_free(&ref->uniform);
	value_table_free(&ref->comb);
	free(ref->row_split);
}

/*
 * Asserts that the compile of a ruleset of one destination network per
 * rule holds the reference regions: the row split map, the join table
 * cells, the uniform line, the region count and the registry range of
 * every masked network, value by value.
 */
static void
assert_regions_match_reference(
	struct memory_context *mctx, struct net6 *nets, uint32_t count
) {
	struct test_rule *rules = calloc(count, sizeof(*rules));
	const struct classifier_rule **rule_ptrs =
		calloc(count, sizeof(*rule_ptrs));
	assert(rules != NULL && rule_ptrs != NULL);
	for (uint32_t idx = 0; idx < count; ++idx) {
		rules[idx] = rule_dst(nets + idx, 1);
		rule_ptrs[idx] = &rules[idx].rule;
	}

	struct classify_compile_net6 *compile = classify_compile_net6_create(
		mctx, rule_ptrs, count, test_rule_get_dsts, "test_net6_dst"
	);
	assert(compile != NULL);
	struct test_regions_reference ref;
	test_regions_reference_build(mctx, compile, &ref);

	assert(memcmp(ref.row_split, compile->row_split, compile->row_count) ==
	       0);
	// The compile packs the split rows in the order of their first high
	// ordinal; the reference keeps every row.
	uint32_t *packed = malloc(compile->row_count * sizeof(uint32_t));
	assert(packed != NULL);
	memset(packed, 0xff, compile->row_count * sizeof(uint32_t));
	uint32_t packed_count = 0;
	const uint32_t *values_hi = ADDR_OF(&compile->ri_hi.values);
	for (uint32_t idx = 0; idx < compile->ri_hi.count; ++idx) {
		uint32_t row = values_hi[idx];
		if (compile->row_split[row] && packed[row] == UINT32_MAX) {
			packed[row] = packed_count++;
		}
	}
	assert(packed_count == compile->comb.v_dim);
	for (uint32_t row = 0; row < compile->row_count; ++row) {
		if (compile->row_split[row]) {
			assert(memcmp(value_table_get_ptr(&ref.comb, row, 0),
				      value_table_get_ptr(
					      &compile->comb, packed[row], 0
				      ),
				      ref.comb.h_dim * sizeof(uint32_t)) == 0);
		}
		assert(vline_get(&ref.uniform, row) ==
		       vline_get(&compile->uniform, row));
	}
	free(packed);
	assert(ref.region_count == compile->region_count);

	const struct value_range *ref_ranges = ADDR_OF(&ref.registry.ranges);
	const struct value_range *ranges =
		ADDR_OF(&compile->net_registry.ranges);
	for (uint32_t net_idx = 0; net_idx < compile->net_count; ++net_idx) {
		if (net6_is_any(compile->nets + net_idx)) {
			continue;
		}
		assert(ref_ranges[net_idx].count == ranges[net_idx].count);
		assert(memcmp(ADDR_OF(&ref_ranges[net_idx].values),
			      ADDR_OF(&ranges[net_idx].values),
			      ranges[net_idx].count * sizeof(uint32_t)) == 0);
	}

	test_regions_reference_free(&ref);
	classify_compile_net6_free(mctx, compile);
	free(rule_ptrs);
	free(rules);
}

// Asserts that every probe resolves to the first rule network covering
// it, one destination network per rule.
static void
assert_first_match_probes(
	struct memory_context *mctx,
	struct net6 *nets,
	uint32_t count,
	const uint8_t (*probes)[NET6_LEN],
	uint32_t probe_count
) {
	struct test_rule rules[8];
	const struct classifier_rule *rule_ptrs[8];
	assert(count <= 8);
	for (uint32_t idx = 0; idx < count; ++idx) {
		rules[idx] = rule_dst(nets + idx, 1);
		rule_ptrs[idx] = &rules[idx].rule;
	}

	struct test_filter_net6_dst flt;
	test_filter_net6_dst_compile(mctx, rule_ptrs, count, &flt);
	uint8_t src[NET6_LEN] = {0};
	for (uint32_t idx = 0; idx < probe_count; ++idx) {
		assert(classify_dst(&flt, src, probes[idx]) ==
		       test_first_match(nets, count, probes[idx]));
	}
	test_filter_net6_dst_free(mctx, &flt);
}

// Builds an address of the given leading bytes of each half.
static void
test_addr(
	uint8_t addr[NET6_LEN],
	uint16_t hi_head,
	uint8_t hi_next,
	uint8_t lo_head,
	uint8_t lo_next
) {
	memset(addr, 0, NET6_LEN);
	addr[0] = hi_head >> 8;
	addr[1] = hi_head & 0xff;
	addr[6] = hi_next;
	addr[8] = lo_head;
	addr[9] = lo_next;
}

// Verifies that a network nested in the low interval of a network with
// the same high interval keeps the enclosing network in its region.
static void
run_nested_low_intervals_test(struct memory_context *mctx, void *memory) {
	(void)memory;

	uint8_t outer[NET6_LEN], inner[NET6_LEN];
	test_addr(outer, 0x2001, 0, 0x0a, 0);
	test_addr(inner, 0x2001, 0, 0x0a, 0x0b);
	struct net6 nets[2] = {
		net6_bits(outer, 32, 8),
		net6_bits(inner, 32, 16),
	};

	uint8_t probes[3][NET6_LEN];
	memcpy(probes[0], inner, NET6_LEN);
	test_addr(probes[1], 0x2001, 0, 0x0a, 0x0c);
	test_addr(probes[2], 0x2001, 0, 0x0b, 0);

	assert_first_match_probes(mctx, nets, 2, probes, 3);
	assert_regions_match_reference(mctx, nets, 2);
}

// Verifies that rows under a wildcard low half network keep it once a
// nested high interval splits them over the low half.
static void
run_wildcard_low_over_split_rows_test(
	struct memory_context *mctx, void *memory
) {
	(void)memory;

	uint8_t child[NET6_LEN], parent[NET6_LEN];
	test_addr(child, 0x2001, 0x80, 0x0a, 0);
	test_addr(parent, 0x2001, 0, 0, 0);
	struct net6 nets[2] = {
		net6_bits(child, 56, 8),
		net6_bits(parent, 32, 0),
	};

	uint8_t probes[3][NET6_LEN];
	memcpy(probes[0], child, NET6_LEN);
	test_addr(probes[1], 0x2001, 0x80, 0x0b, 0);
	test_addr(probes[2], 0x2001, 0x40, 0x0a, 0);

	assert_first_match_probes(mctx, nets, 2, probes, 3);
	assert_regions_match_reference(mctx, nets, 2);
}

// Verifies that the low half networks of a high interval do not reach a
// sibling high interval swept after it under a parent split over the
// low half.
static void
run_sibling_high_intervals_test(struct memory_context *mctx, void *memory) {
	(void)memory;

	uint8_t first[NET6_LEN], second[NET6_LEN], parent[NET6_LEN];
	test_addr(first, 0x2001, 0x10, 0x0a, 0);
	test_addr(second, 0x2001, 0x20, 0x0b, 0);
	test_addr(parent, 0x2001, 0, 0x0c, 0);
	struct net6 nets[3] = {
		net6_bits(first, 56, 8),
		net6_bits(second, 56, 8),
		net6_bits(parent, 32, 8),
	};

	uint8_t probes[4][NET6_LEN];
	test_addr(probes[0], 0x2001, 0x20, 0x0a, 0);
	memcpy(probes[1], second, NET6_LEN);
	memcpy(probes[2], first, NET6_LEN);
	test_addr(probes[3], 0x2001, 0x20, 0x0c, 0);

	assert_first_match_probes(mctx, nets, 3, probes, 4);
	assert_regions_match_reference(mctx, nets, 3);
}

// Verifies that a row changed only at its first low regions ranks the
// uniform class of the regions after them in the enumeration order.
static void
run_sparse_row_trailing_uniform_test(
	struct memory_context *mctx, void *memory
) {
	(void)memory;

	struct net6 nets[18];
	uint8_t addr[NET6_LEN];
	test_addr(addr, 0x2001, 0, 0, 0);
	nets[0] = net6_bits(addr, 16, 0);
	nets[1] = net6_bits(addr, 32, 8);
	nets[2] = net6_bits(addr, 56, 8);
	for (uint8_t idx = 1; idx < 16; ++idx) {
		test_addr(addr, 0x3001, 0, idx << 4, 0);
		nets[2 + idx] = net6_bits(addr, 16, 8);
	}

	assert_regions_match_reference(mctx, nets, 18);
}

#define TEST_REGIONS_SEEDS 1000
#define TEST_REGIONS_MAX_NETS 48

/*
 * Verifies that the builder assigns the regions of the reference
 * enumeration, value for value, on seeded rulesets nesting densely in
 * both halves.
 *
 * The prefixes are 0 to 6 bits long, plus a few long ones, so the high
 * intervals nest in deep trees with several low intervals per node;
 * wildcard and repeated networks are mixed in.
 */
static void
run_regions_match_enumeration_test(struct memory_context *mctx, void *memory) {
	(void)memory;

	static const uint8_t lens[] = {0, 1, 2, 3, 3, 4, 5, 6, 40, 64};
	struct net6 nets[TEST_REGIONS_MAX_NETS];
	for (uint64_t seed = 1; seed <= TEST_REGIONS_SEEDS; ++seed) {
		uint64_t state = seed;
		uint32_t count = 1 + seed % TEST_REGIONS_MAX_NETS;
		for (uint32_t idx = 0; idx < count; ++idx) {
			uint8_t addr[NET6_LEN];
			for (uint32_t byte = 0; byte < NET6_LEN; ++byte) {
				addr[byte] = test_rand(&state);
			}
			uint8_t hi_bits =
				lens[test_rand(&state) % sizeof(lens)];
			uint8_t lo_bits =
				lens[test_rand(&state) % sizeof(lens)];
			if (test_rand(&state) % 16 == 0) {
				hi_bits = 0;
				lo_bits = 0;
			}
			nets[idx] = net6_bits(addr, hi_bits, lo_bits);
			if (idx && test_rand(&state) % 8 == 0) {
				nets[idx] = nets[test_rand(&state) % idx];
			}
		}
		assert_regions_match_reference(mctx, nets, count);
	}
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
	run_scenario(run_nested_low_intervals_test, memory);
	run_scenario(run_wildcard_low_over_split_rows_test, memory);
	run_scenario(run_sibling_high_intervals_test, memory);
	run_scenario(run_sparse_row_trailing_uniform_test, memory);
	run_scenario(run_regions_match_enumeration_test, memory);

	free(memory);
	return 0;
}
