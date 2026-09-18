// Runtime test of the shared classification core: the network core is
// compiled once and the ip6 filter is derived from it with the mapping
// alone, while the ip6 port filter derives from the same core joined
// with a ports classifier.
//
// Pins the sharing (the derived filters read the same network
// classifiers) and the projection semantics: a rule of one projection
// never resolves in the other, and each filter picks the first rule of
// its own projection.

#include "lib/filter2/compiler.h"
#include "lib/filter2/filter.h"
#include "lib/filter2/query.h"

#include "lib/utils/packet.h"

#include "common/memory.h"

#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

FILTER_COMPILER_DECLARE(
	sign_core, device, vlan, net6_src, net6_dst, proto_range
);
FILTER_QUERY_DECLARE(sign_ip6_q, device, vlan, net6_src, net6_dst, proto_range);
FILTER_QUERY_DECLARE(
	sign_ip6_port_q,
	device,
	vlan,
	net6_src,
	net6_dst,
	proto_range,
	port_src,
	port_dst
);

static void
put16(uint8_t *p, uint32_t g, uint16_t v) {
	p[g * 2] = v >> 8;
	p[g * 2 + 1] = v;
}

// Fills a v6 rule matching the given networks and port windows; a
// window of NULL means the full range, which keeps the rule out of the
// port scoped projection.
static struct filter_rule
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

	struct filter_rule rule = {0};
	rule.net6.src_count = 1;
	rule.net6.srcs = srcs;
	rule.net6.dst_count = 1;
	rule.net6.dsts = dsts;

	ranges[0] = sport ? *sport : (struct filter_port_range){0, 65535};
	ranges[1] = dport ? *dport : (struct filter_port_range){0, 65535};
	rule.transport.src_count = 1;
	rule.transport.srcs = ranges;
	rule.transport.dst_count = 1;
	rule.transport.dsts = ranges + 1;

	return rule;
}

#define classify(filter, sign, src, dst, sport, dport)                         \
	({                                                                     \
		struct packet classify_packet = {0};                           \
		assert(fill_packet_net6(                                       \
			       &classify_packet,                               \
			       src,                                            \
			       dst,                                            \
			       sport,                                          \
			       dport,                                          \
			       IPPROTO_UDP,                                    \
			       0                                               \
		       ) == 0);                                                \
		struct packet *classify_ptr = &classify_packet;                \
		uint32_t classify_result;                                      \
		filter_query(                                                  \
			filter, sign, &classify_ptr, &classify_result, 1       \
		);                                                             \
		free_packet(&classify_packet);                                 \
		classify_result;                                               \
	})

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
	struct filter_rule rule0 = make_rule(
		net_a, 32, net_b, 32, NULL, NULL, &srcs[0], &dsts[0], ranges[0]
	);
	struct filter_rule rule1 = make_rule(
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
	struct filter_rule rule2 = make_rule(
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

	const struct filter_rule *union_rules[3] = {&rule0, &rule1, &rule2};
	const struct filter_rule *ip6_rules[3] = {&rule0, NULL, NULL};
	const struct filter_rule *port_rules[3] = {NULL, &rule1, &rule2};

	void *memory = malloc(1 << 24);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);
	struct memory_context mctx;
	assert(memory_context_init(&mctx, "test", &allocator) == 0);

	struct filter_core core;
	assert(filter_core_init(&core, sign_core, union_rules, 3, &mctx) == 0);

	struct filter filter_ip6;
	assert(filter_derive_init(&filter_ip6, &core, ip6_rules, sign_core) ==
	       0);

	static const struct filter_compile_attr_handlers *sign_port[] = {
		FILTER_ATTR_COMPILE(device),
		FILTER_ATTR_COMPILE(vlan),
		FILTER_ATTR_COMPILE(net6_src),
		FILTER_ATTR_COMPILE(net6_dst),
		FILTER_ATTR_COMPILE(proto_range),
		FILTER_ATTR_COMPILE(port_src),
		FILTER_ATTR_COMPILE(port_dst),
	};

	struct filter filter_port;
	assert(filter_derive_init(&filter_port, &core, port_rules, sign_port) ==
	       0);

	// Verifies that both filters classify through the same network
	// classifiers: the net6 source attribute and the first join table
	// are the core objects.
	{
		struct filter_query_attr **core_attrs = ADDR_OF(&core.attrs);
		struct filter_query_attr **ip6_attrs =
			ADDR_OF(&filter_ip6.attrs);
		struct filter_query_attr **port_attrs =
			ADDR_OF(&filter_port.attrs);
		assert(ADDR_OF(ip6_attrs + 2) == ADDR_OF(core_attrs + 2));
		assert(ADDR_OF(port_attrs + 2) == ADDR_OF(core_attrs + 2));
		assert(ADDR_OF(ip6_attrs + 3) == ADDR_OF(port_attrs + 3));

		struct value_table *core_joints = ADDR_OF(&core.joints);
		struct value_table *ip6_joints = ADDR_OF(&filter_ip6.joints);
		struct value_table *port_joints = ADDR_OF(&filter_port.joints);
		assert(ADDR_OF(&ip6_joints->values) ==
		       ADDR_OF(&core_joints->values));
		assert(ADDR_OF(&port_joints->values) ==
		       ADDR_OF(&core_joints->values));
	}

	// The ip6 filter resolves through the core classes alone and sees
	// the full port rule only.
	assert(classify(&filter_ip6, sign_ip6_q, net_a, net_b, 150, 1000) == 0);
	assert(classify(&filter_ip6, sign_ip6_q, net_a, net_b, 500, 1000) == 0);
	assert(classify(&filter_ip6, sign_ip6_q, net_b, net_a, 150, 150) ==
	       FILTER_RULE_INVALID);

	// The port scoped filter resolves through the core and the ports
	// classifier together and never sees the full port rule.
	assert(classify(
		       &filter_port, sign_ip6_port_q, net_a, net_b, 150, 1000
	       ) == 1);
	assert(classify(
		       &filter_port, sign_ip6_port_q, net_a, net_b, 500, 1000
	       ) == FILTER_RULE_INVALID);
	assert(classify(
		       &filter_port, sign_ip6_port_q, net_b, net_a, 150, 150
	       ) == 2);
	assert(classify(
		       &filter_port, sign_ip6_port_q, net_b, net_a, 150, 300
	       ) == FILTER_RULE_INVALID);

	// A plain compile of the same projection agrees with the
	// derivation on every case above.
	{
		struct filter plain;
		assert(filter_init(&plain, sign_port, port_rules, 3, &mctx) ==
		       0);
		assert(classify(
			       &plain, sign_ip6_port_q, net_a, net_b, 150, 1000
		       ) == 1);
		assert(classify(
			       &plain, sign_ip6_port_q, net_a, net_b, 500, 1000
		       ) == FILTER_RULE_INVALID);
		assert(classify(
			       &plain, sign_ip6_port_q, net_b, net_a, 150, 150
		       ) == 2);
		filter_free(&plain, sign_port);
	}

	// Derived filters are destroyed before the core they borrow from.
	filter_free(&filter_ip6, sign_core);
	filter_free(&filter_port, sign_port);
	filter_core_free(&core, sign_core);

	memory_context_fini(&mctx);
	free(memory);
	return 0;
}
