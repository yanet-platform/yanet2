// Equivalence tests for the hashed network classifiers.
//
// The hashed net4/net6 attributes must resolve the same first-match
// rules as the LPM variants over the same ruleset - including the
// containing-prefix case, where an address inside a shorter prefix of
// one rule and a longer prefix of another must see both rules - once
// each network stage joins with the port stage through the rule rows.

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
// views, like a module rule.
struct test_rule {
	struct classifier_rule rule;
	struct filter_net4s net4s;
	struct filter_net6s net6s;
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
test_rule_get_port_ranges(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
) {
	const struct test_rule *test_rule =
		container_of(rule, struct test_rule, rule);
	*ranges = test_rule->port_ranges;
}

CLASSIFY_NET4_COMPILE(test_net4, test_rule_get_net4s)
CLASSIFY_NET4_HASH_COMPILE(test_net4_hash, test_rule_get_net4s)
CLASSIFY_NET6_COMPILE(test_net6, test_rule_get_net6s)
CLASSIFY_NET6_HASH_COMPILE(test_net6_hash, test_rule_get_net6s)
CLASSIFY_U16_RANGES_COMPILE(
	test_port, struct classify_attr_port, test_rule_get_port_ranges
)

// One IPv4 network of the fixture: the address and the prefix mask of
// the given length.
static void
net4_of(struct net4 *net4,
	uint8_t a,
	uint8_t b,
	uint8_t c,
	uint8_t d,
	uint8_t bits) {
	memset(net4, 0, sizeof(*net4));
	net4->addr[0] = a;
	net4->addr[1] = b;
	net4->addr[2] = c;
	net4->addr[3] = d;
	for (uint32_t idx = 0; idx < NET4_LEN; ++idx) {
		const uint32_t rest = bits > idx * 8 ? bits - idx * 8 : 0;
		net4->mask[idx] =
			rest >= 8 ? 0xff : (uint8_t)(0xffu << (8 - rest));
	}
}

static void
net6_of(struct net6 *net6, const uint8_t *addr, uint8_t bits) {
	memset(net6, 0, sizeof(*net6));
	memcpy(net6->addr, addr, NET6_LEN);
	for (uint32_t idx = 0; idx < NET6_LEN; ++idx) {
		const uint32_t rest = bits > idx * 8 ? bits - idx * 8 : 0;
		net6->mask[idx] =
			rest >= 8 ? 0xff : (uint8_t)(0xffu << (8 - rest));
	}
}

int
main(void) {
	// The ruleset:
	//   rule 0: 10.0.0.0/8, 2a02::/32; any port.
	//   rule 1: 10.1.0.0/24; ports 100..200.
	//   rule 2: 192.168.0.0/16, 2a0d::/32; ports 300..400.
	//
	// The containing-prefix case is 10.1.0.42: rule 0 covers it through
	// the /8 and rule 1 through the /24, so rule 0 matches whatever the
	// port is; 10.2.0.1 sees rule 0 alone and 192.168.1.1 rule 2 alone.
	struct net4 net4s[4];
	net4_of(&net4s[0], 10, 0, 0, 0, 8);
	net4_of(&net4s[1], 10, 1, 0, 0, 24);
	net4_of(&net4s[2], 192, 168, 0, 0, 16);

	uint8_t net6_a[NET6_LEN] = {0};
	net6_a[0] = 0x2a;
	net6_a[1] = 0x02;
	uint8_t net6_b[NET6_LEN] = {0};
	net6_b[0] = 0x2a;
	net6_b[1] = 0x0d;
	struct net6 net6s[4];
	net6_of(&net6s[0], net6_a, 32);
	net6_of(&net6s[1], net6_b, 32);

	struct filter_u16_span port_spans[3] = {
		{100, 200},
		{300, 400},
		{0, 0},
	};

	struct test_rule rules[3];
	memset(rules, 0, sizeof(rules));
	rules[0].net4s.items = &net4s[0];
	rules[0].net4s.count = 1;
	rules[0].net6s.items = &net6s[0];
	rules[0].net6s.count = 1;
	rules[1].net4s.items = &net4s[1];
	rules[1].net4s.count = 1;
	rules[1].port_ranges.items = &port_spans[0];
	rules[1].port_ranges.count = 1;
	rules[2].net4s.items = &net4s[2];
	rules[2].net4s.count = 1;
	rules[2].net6s.items = &net6s[1];
	rules[2].net6s.count = 1;
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
	// per network variant: the network classes join the port classes
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
		(10u << 24) | 1u,
		(10u << 24) | (1u << 16),
		(10u << 24) | (1u << 16) | 42u, // inside /24 and /8
		(10u << 24) | (2u << 16) | 1u,	// inside /8 only
		(192u << 24) | (168u << 16) | (1u << 8) | 1u, // rule 2
		(172u << 24) | (16u << 16) | 1u,	      // no rule
		(192u << 24) | (169u << 16) | 1u,	      // outside rule 2
	};
	const uint16_t ports[] = {0, 99, 100, 200, 201, 299, 300, 400, 401};
	const uint32_t want[] = {
		// 10.0.0.1: rule 0 through the /8, whatever the port.
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		// 10.1.0.0: rules 0 and 1; rule 0 is first everywhere.
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		// 10.1.0.42: the containing-prefix case; rule 0 everywhere.
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		// 10.2.0.1: rule 0 alone.
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		// 192.168.1.1: rule 2 on 300..400 only.
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		2,
		2,
		FILTER_RULE_INVALID,
		// 172.16.0.1: no rule.
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		FILTER_RULE_INVALID,
		// 192.169.1.1: outside rule 2.
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

	printf("  hashed net4 matches the LPM variant: PASSED\n");

	// The net6 equivalence over the same ruleset.
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

	const uint8_t addrs6[][NET6_LEN] = {
		{0x2a, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
		}, // rule 0
		{0x2a, 0x0d, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
		}, // rule 2
		{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
		}, // miss
		{0x2a, 0x02, 0x11, 0x22, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
		}, // rule 0
		{0x2a, 0x0e, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, // miss
	};
	for (uint32_t addr_idx = 0;
	     addr_idx < sizeof(addrs6) / sizeof(addrs6[0]);
	     ++addr_idx) {
		for (uint32_t port_idx = 0;
		     port_idx < sizeof(ports) / sizeof(ports[0]);
		     ++port_idx) {
			uint32_t port_class = vline_get(
				(struct vline *)&port_attr.line, ports[port_idx]
			);

			uint32_t lpm_net;
			classify_net6_lookup(
				&lpm6_attr, addrs6[addr_idx], &lpm_net, 1
			);
			uint32_t lpm_rule;
			classify_rule_table_lookup(
				&lpm6_rules, &lpm_net, &port_class, &lpm_rule, 1
			);

			uint32_t hash_net;
			classify_net6_hash_lookup(
				&hash6_attr, addrs6[addr_idx], &hash_net, 1
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

	printf("  hashed net6 matches the LPM variant: PASSED\n");

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

	printf("=== hashed network classifier tests PASSED ===\n");
	return EXIT_SUCCESS;
}
