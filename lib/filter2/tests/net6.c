// Runtime classification test for the net6 attribute of the region-based
// filter library.
//
// Pins the regression of combining the high and the low address halves
// through the shared classification: unique networks grouped across rules,
// masks spanning both halves, networks shared and nested across rules, a
// wildcard network, and the source with destination joint. Every scenario
// compiles a ruleset, classifies synthetic packets and asserts the resolved
// rule index.

#include "lib/filter2/compiler.h"
#include "lib/filter2/filter.h"
#include "lib/filter2/query.h"

#include "lib/utils/packet.h"

#include "common/memory.h"

#include <assert.h>
#include <netinet/in.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

FILTER_COMPILER_DECLARE(sign_compile_dst, net6_dst);
FILTER_QUERY_DECLARE(sign_query_dst, net6_dst);

FILTER_COMPILER_DECLARE(sign_compile_both, net6_src, net6_dst);
FILTER_QUERY_DECLARE(sign_query_both, net6_src, net6_dst);

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
static struct filter_rule
rule_dst(struct net6 *nets, uint32_t count) {
	struct filter_rule rule = {0};
	rule.net6.dst_count = count;
	rule.net6.dsts = nets;
	return rule;
}

// Fills a rule matching packets of the given source and destination
// networks.
static struct filter_rule
rule_src_dst(
	struct net6 *srcs,
	uint32_t src_count,
	struct net6 *dsts,
	uint32_t dst_count
) {
	struct filter_rule rule = {0};
	rule.net6.src_count = src_count;
	rule.net6.srcs = srcs;
	rule.net6.dst_count = dst_count;
	rule.net6.dsts = dsts;
	return rule;
}

// Classifies one synthetic packet of the given address pair against the
// destination-only signature and asserts the resolved rule index.
static uint32_t
classify_dst(
	struct filter *filter,
	const uint8_t src[NET6_LEN],
	const uint8_t dst[NET6_LEN]
) {
	struct packet packet = {0};
	assert(fill_packet_net6(&packet, src, dst, 100, 200, IPPROTO_UDP, 0) ==
	       0);

	struct packet *packet_ptr = &packet;
	uint32_t rule;
	filter_query(filter, sign_query_dst, &packet_ptr, &rule, 1);

	free_packet(&packet);
	return rule;
}

// Classifies one synthetic packet of the given address pair against the
// source and destination signature and asserts the resolved rule index.
static uint32_t
classify_both(
	struct filter *filter,
	const uint8_t src[NET6_LEN],
	const uint8_t dst[NET6_LEN]
) {
	struct packet packet = {0};
	assert(fill_packet_net6(&packet, src, dst, 100, 200, IPPROTO_UDP, 0) ==
	       0);

	struct packet *packet_ptr = &packet;
	uint32_t rule;
	filter_query(filter, sign_query_both, &packet_ptr, &rule, 1);

	free_packet(&packet);
	return rule;
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
	struct filter_rule rule = rule_dst(nets, 1);
	const struct filter_rule *rules[1] = {&rule};

	struct filter filter;
	assert(filter_init(&filter, sign_compile_dst, rules, 1, mctx) == 0);

	uint8_t src[NET6_LEN] = {0};

	// the same address
	assert(classify_dst(&filter, src, base) == 0);
	// the skipped high half bytes are free
	uint8_t inside_hi[NET6_LEN];
	memcpy(inside_hi, base, NET6_LEN);
	inside_hi[5] = 0xff;
	inside_hi[7] = 0x99;
	assert(classify_dst(&filter, src, inside_hi) == 0);
	// the skipped low half bytes are free
	uint8_t inside_lo[NET6_LEN];
	memcpy(inside_lo, base, NET6_LEN);
	inside_lo[11] = 0xff;
	inside_lo[15] = 0x77;
	assert(classify_dst(&filter, src, inside_lo) == 0);
	// an address outside the masked high interval
	uint8_t outside_hi[NET6_LEN];
	memcpy(outside_hi, base, NET6_LEN);
	outside_hi[4] = 0x12;
	assert(classify_dst(&filter, src, outside_hi) == FILTER_RULE_INVALID);
	// an address outside the masked low interval
	uint8_t outside_lo[NET6_LEN];
	memcpy(outside_lo, base, NET6_LEN);
	outside_lo[9] = 0x56;
	assert(classify_dst(&filter, src, outside_lo) == FILTER_RULE_INVALID);

	filter_free(&filter, sign_compile_dst);
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
	struct filter_rule rule0 = rule_dst(rule0_nets, 1);

	struct net6 rule1_nets[2] = {n1, n2};
	struct filter_rule rule1 = rule_dst(rule1_nets, 2);

	struct net6 rule2_nets[1] = {n2};
	struct filter_rule rule2 = rule_dst(rule2_nets, 1);

	// holds the exact value of the first rule
	struct net6 rule3_nets[1] = {n1};
	struct filter_rule rule3 = rule_dst(rule3_nets, 1);

	const struct filter_rule *rules[4] = {&rule0, &rule1, &rule2, &rule3};

	struct filter filter;
	assert(filter_init(&filter, sign_compile_dst, rules, 4, mctx) == 0);

	uint8_t src[NET6_LEN] = {0};

	assert(classify_dst(&filter, src, addr_n1) == 0);
	assert(classify_dst(&filter, src, addr_n2) == 1);

	uint8_t other[NET6_LEN] = {0};
	other[0] = 0xdd;
	assert(classify_dst(&filter, src, other) == FILTER_RULE_INVALID);

	filter_free(&filter, sign_compile_dst);
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
	struct filter_rule rule0 = rule_dst(rule0_nets, 1);
	struct net6 rule1_nets[1] = {broad};
	struct filter_rule rule1 = rule_dst(rule1_nets, 1);
	struct net6 rule2_nets[1] = {any};
	struct filter_rule rule2 = rule_dst(rule2_nets, 1);

	const struct filter_rule *rules[3] = {&rule0, &rule1, &rule2};

	struct filter filter;
	assert(filter_init(&filter, sign_compile_dst, rules, 3, mctx) == 0);

	uint8_t src[NET6_LEN] = {0};

	// inside both the specific and the broad network
	assert(classify_dst(&filter, src, addr_specific) == 0);
	// inside the broad network only
	uint8_t broad_only[NET6_LEN];
	memcpy(broad_only, addr_broad, NET6_LEN);
	broad_only[5] = 0x99;
	assert(classify_dst(&filter, src, broad_only) == 1);
	// outside every masked network, inside the wildcard one
	uint8_t other[NET6_LEN] = {0};
	other[0] = 0xdd;
	assert(classify_dst(&filter, src, other) == 2);

	filter_free(&filter, sign_compile_dst);
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

	struct filter_rule rule0 = rule_src_dst(&s1, 1, &d1, 1);
	struct filter_rule rule1 = rule_src_dst(&s1, 1, &d2, 1);
	struct filter_rule rule2 = rule_src_dst(&s2, 1, &d1, 1);

	const struct filter_rule *rules[3] = {&rule0, &rule1, &rule2};

	struct filter filter;
	assert(filter_init(&filter, sign_compile_both, rules, 3, mctx) == 0);

	assert(classify_both(&filter, addr_s1, addr_d1) == 0);
	assert(classify_both(&filter, addr_s1, addr_d2) == 1);
	assert(classify_both(&filter, addr_s2, addr_d1) == 2);
	assert(classify_both(&filter, addr_s2, addr_d2) == FILTER_RULE_INVALID);

	filter_free(&filter, sign_compile_both);
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
