// Equivalence and join tests for the hashed address classifiers.
//
// The hashed net4/net6 attributes match exact addresses - an address
// resolves iff the ruleset names it - so the equivalence reference is
// the LPM variant over a ruleset of host routes: for fully specified
// addresses the two must resolve the same first-match rules, and the
// hash address classes must join with a port attribute into the same
// rule rows the LPM classes join into.

#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net4_hash.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/net6_hash.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/classify.h"
#include "lib/classify/compiler/helper.h"
#include "lib/classify/compiler/net4.h"
#include "lib/classify/compiler/net4_hash.h"
#include "lib/classify/compiler/net6.h"
#include "lib/classify/compiler/net6_hash.h"
#include "lib/classify/compiler/u16_ranges.h"
#include "lib/classify/query.h"
#include "lib/classify/rule.h"

#include "common/container_of.h"
#include "common/memory.h"

#include <arpa/inet.h>
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// The consumer rule: the classifier base first, then the attribute
// views, like a module rule - networks for the LPM reference, plain
// addresses for the hashed attributes under test, ports shared.
struct test_rule {
	struct classifier_rule rule;
	struct filter_net4s net4s;
	struct filter_net6s net6s;
	struct classify_net4_addrs addrs4;
	struct classify_net6_addrs addrs6;
	struct filter_u16_ranges port_ranges;
};

static inline void
test_rule_get_net4s(
	const struct classifier_rule *rule, struct filter_net4s *nets
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	*nets = test_rule->net4s;
}

static inline void
test_rule_get_net6s(
	const struct classifier_rule *rule, struct filter_net6s *nets
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	*nets = test_rule->net6s;
}

static inline void
test_rule_get_addrs4(
	const struct classifier_rule *rule, struct classify_net4_addrs *addrs
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	*addrs = test_rule->addrs4;
}

static inline void
test_rule_get_addrs6(
	const struct classifier_rule *rule, struct classify_net6_addrs *addrs
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	*addrs = test_rule->addrs6;
}

static inline void
test_rule_get_port_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	*ranges = test_rule->port_ranges;
}

CLASSIFY_NET4_COMPILE(test_net4, test_rule_get_net4s)
CLASSIFY_NET4_HASH_COMPILE(test_net4_hash, test_rule_get_addrs4)
CLASSIFY_NET6_COMPILE(test_net6, test_rule_get_net6s)
CLASSIFY_NET6_HASH_COMPILE(test_net6_hash, test_rule_get_addrs6)
CLASSIFY_U16_RANGES_COMPILE(
	test_port, struct classify_attr_port, test_rule_get_port_ranges
)

// One host route of the LPM reference: the address with the full mask.
static void
host4_of(struct net4 *net4, uint8_t a, uint8_t b, uint8_t c, uint8_t d) {
	net4->addr[0] = a;
	net4->addr[1] = b;
	net4->addr[2] = c;
	net4->addr[3] = d;
	net4->mask[0] = 0xff;
	net4->mask[1] = 0xff;
	net4->mask[2] = 0xff;
	net4->mask[3] = 0xff;
}

static void
host6_of(struct net6 *net6, const uint8_t *addr) {
	memcpy(net6->addr, addr, NET6_LEN);
	memset(net6->mask, 0xff, NET6_LEN);
}

int
main(void) {
	// The ruleset, each network carrying exactly the addresses the
	// hashed rule names:
	//   rule 0: 10.0.0.1, 2a02::1; any port.
	//   rule 1: 10.0.0.2; ports 100..200.
	//   rule 2: 192.168.0.1, 2a0d::1; ports 300..400.
	struct net4 hosts4[3];
	host4_of(&hosts4[0], 10, 0, 0, 1);
	host4_of(&hosts4[1], 10, 0, 0, 2);
	host4_of(&hosts4[2], 192, 168, 0, 1);

	uint8_t host6_a[NET6_LEN] = {0};
	host6_a[0] = 0x2a;
	host6_a[1] = 0x02;
	host6_a[15] = 0x01;
	uint8_t host6_b[NET6_LEN] = {0};
	host6_b[0] = 0x2a;
	host6_b[1] = 0x0d;
	host6_b[15] = 0x01;
	struct net6 hosts6[2];
	host6_of(&hosts6[0], host6_a);
	host6_of(&hosts6[1], host6_b);

	uint8_t addrs4[3][NET4_LEN] = {
		{10, 0, 0, 1},
		{10, 0, 0, 2},
		{192, 168, 0, 1},
	};
	uint8_t addrs6[2][NET6_LEN];
	memcpy(addrs6[0], host6_a, NET6_LEN);
	memcpy(addrs6[1], host6_b, NET6_LEN);

	struct filter_u16_span port_spans[3] = {
		{100, 200},
		{300, 400},
		{0, 0},
	};

	struct test_rule rules[3];
	memset(rules, 0, sizeof(rules));
	rules[0].net4s.items = &hosts4[0];
	rules[0].net4s.count = 1;
	rules[0].net6s.items = &hosts6[0];
	rules[0].net6s.count = 1;
	rules[0].addrs4.items = &addrs4[0];
	rules[0].addrs4.count = 1;
	rules[0].addrs6.items = &addrs6[0];
	rules[0].addrs6.count = 1;
	rules[1].net4s.items = &hosts4[1];
	rules[1].net4s.count = 1;
	rules[1].addrs4.items = &addrs4[1];
	rules[1].addrs4.count = 1;
	rules[1].port_ranges.items = &port_spans[0];
	rules[1].port_ranges.count = 1;
	rules[2].net4s.items = &hosts4[2];
	rules[2].net4s.count = 1;
	rules[2].net6s.items = &hosts6[1];
	rules[2].net6s.count = 1;
	rules[2].addrs4.items = &addrs4[2];
	rules[2].addrs4.count = 1;
	rules[2].addrs6.items = &addrs6[1];
	rules[2].addrs6.count = 1;
	rules[2].port_ranges.items = &port_spans[1];
	rules[2].port_ranges.count = 1;

	const struct classifier_rule *rule_ptrs[3] = {
		&rules[0].rule, &rules[1].rule, &rules[2].rule
	};

	void *memory = malloc(1 << 24);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);
	struct memory_context mctx;
	assert(memory_context_init(&mctx, "test", &allocator) == 0);

	// The shared port stage over the full ruleset, and one rule table
	// per address variant: the address classes join the port classes
	// into the first-match rule rows.
	struct classify_attr_port port_attr;
	struct classifier port_cls;
	assert(classify_test_port_compile(
		       &mctx, "port", rule_ptrs, 3, &port_attr, &port_cls
	       ) == 0);

	struct classify_attr_net4 lpm4_attr;
	struct classifier lpm4_cls;
	assert(classify_test_net4_compile(
		       &mctx, "lpm4", rule_ptrs, 3, &lpm4_attr, &lpm4_cls
	       ) == 0);
	struct rule_table lpm4_rules;
	assert(classify_join_rules(
		       &mctx,
		       "lpm4",
		       &lpm4_cls,
		       &port_cls,
		       rule_ptrs,
		       3,
		       &lpm4_rules
	       ) == 0);

	struct classify_attr_net4_hash hash4_attr;
	struct classifier hash4_cls;
	assert(classify_test_net4_hash_compile(
		       &mctx, "hash4", rule_ptrs, 3, &hash4_attr, &hash4_cls
	       ) == 0);
	struct rule_table hash4_rules;
	assert(classify_join_rules(
		       &mctx,
		       "hash4",
		       &hash4_cls,
		       &port_cls,
		       rule_ptrs,
		       3,
		       &hash4_rules
	       ) == 0);

	const uint32_t addrs[] = {
		(10u << 24) | 1u,		  // rule 0
		(10u << 24) | 2u,		  // rule 1
		(192u << 24) | (168u << 16) | 1u, // rule 2
		(10u << 24) | 3u,		  // named by no rule
		(172u << 24) | (16u << 16) | 1u,  // named by no rule
	};
	const uint16_t ports[] = {0, 99, 100, 200, 201, 299, 300, 400, 401};
	const uint32_t want[] = {
		// 10.0.0.1: rule 0, whatever the port.
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		// 10.0.0.2: rule 1 on 100..200 only.
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		1,
		1,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		// 192.168.0.1: rule 2 on 300..400 only.
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		2,
		2,
		FILTER_RULE_INVALID,
		// 10.0.0.3 and 172.16.0.1: named by no rule, whatever the port.
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
	};

	for (uint32_t addr_idx = 0; addr_idx < sizeof(addrs) / sizeof(addrs[0]);
	     ++addr_idx) {
		for (uint32_t port_idx = 0;
		     port_idx < sizeof(ports) / sizeof(ports[0]);
		     ++port_idx) {
			const uint32_t addr = htonl(addrs[addr_idx]);
			uint32_t port_class = vline_get(
				(struct vline *)&port_attr.line, ports[port_idx]
			);

			uint32_t lpm_net;
			classify_net4_lookup(&lpm4_attr, &addr, &lpm_net, 1);
			uint32_t lpm_rule;
			classify_rule_table_lookup(
				&lpm4_rules, &lpm_net, &port_class, &lpm_rule, 1
			);

			uint32_t hash_net;
			classify_net4_hash_lookup(
				&hash4_attr, &addr, &hash_net, 1
			);
			uint32_t hash_rule;
			classify_rule_table_lookup(
				&hash4_rules,
				&hash_net,
				&port_class,
				&hash_rule,
				1
			);

			const uint32_t want_rule =
				want[addr_idx * 9 + port_idx];
			assert(lpm_rule == want_rule);
			assert(hash_rule == want_rule);
		}
	}

	printf("  hashed net4 matches the LPM host routes: PASSED\n");

	// The net6 equivalence over the same ruleset shape.
	struct classify_attr_net6 lpm6_attr;
	struct classifier lpm6_cls;
	assert(classify_test_net6_compile(
		       &mctx, "lpm6", rule_ptrs, 3, &lpm6_attr, &lpm6_cls
	       ) == 0);
	struct rule_table lpm6_rules;
	assert(classify_join_rules(
		       &mctx,
		       "lpm6",
		       &lpm6_cls,
		       &port_cls,
		       rule_ptrs,
		       3,
		       &lpm6_rules
	       ) == 0);

	struct classify_attr_net6_hash hash6_attr;
	struct classifier hash6_cls;
	assert(classify_test_net6_hash_compile(
		       &mctx, "hash6", rule_ptrs, 3, &hash6_attr, &hash6_cls
	       ) == 0);
	struct rule_table hash6_rules;
	assert(classify_join_rules(
		       &mctx,
		       "hash6",
		       &hash6_cls,
		       &port_cls,
		       rule_ptrs,
		       3,
		       &hash6_rules
	       ) == 0);

	const uint8_t addrs6_probe[][NET6_LEN] = {
		{0x2a, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
		}, // rule 0
		{0x2a, 0x0d, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
		}, // rule 2
		{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
		},							// miss
		{0x2a, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}, // miss
		{0x2a, 0x0d, 0x11, 0x22, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
		}, // miss
	};
	for (uint32_t addr_idx = 0;
	     addr_idx < sizeof(addrs6_probe) / sizeof(addrs6_probe[0]);
	     ++addr_idx) {
		for (uint32_t port_idx = 0;
		     port_idx < sizeof(ports) / sizeof(ports[0]);
		     ++port_idx) {
			uint32_t port_class = vline_get(
				(struct vline *)&port_attr.line, ports[port_idx]
			);

			uint32_t lpm_net;
			classify_net6_lookup(
				&lpm6_attr, addrs6_probe[addr_idx], &lpm_net, 1
			);
			uint32_t lpm_rule;
			classify_rule_table_lookup(
				&lpm6_rules, &lpm_net, &port_class, &lpm_rule, 1
			);

			uint32_t hash_net;
			classify_net6_hash_lookup(
				&hash6_attr,
				addrs6_probe[addr_idx],
				&hash_net,
				1
			);
			uint32_t hash_rule;
			classify_rule_table_lookup(
				&hash6_rules,
				&hash_net,
				&port_class,
				&hash_rule,
				1
			);

			assert(lpm_rule == hash_rule);
		}
	}

	printf("  hashed net6 matches the LPM host routes: PASSED\n");

	rule_table_free(&hash6_rules);
	classify_attr_net6_hash_free(&mctx, &hash6_attr);
	classifier_fini(&hash6_cls, &mctx, 3);

	rule_table_free(&lpm6_rules);
	classify_attr_net6_free(&mctx, &lpm6_attr);
	classifier_fini(&lpm6_cls, &mctx, 3);

	rule_table_free(&hash4_rules);
	classify_attr_net4_hash_free(&mctx, &hash4_attr);
	classifier_fini(&hash4_cls, &mctx, 3);

	rule_table_free(&lpm4_rules);
	classify_attr_net4_free(&mctx, &lpm4_attr);
	classifier_fini(&lpm4_cls, &mctx, 3);

	classify_attr_port_free(&mctx, &port_attr);
	classifier_fini(&port_cls, &mctx, 3);

	memory_context_fini(&mctx);
	block_allocator_fini(&allocator);
	free(memory);

	printf("=== hashed address classifier tests PASSED ===\n");
	return EXIT_SUCCESS;
}
