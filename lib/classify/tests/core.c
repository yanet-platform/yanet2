// Runtime test of the two stage compilation over explicit classifier
// trees, in the shape of the acl module: an ip6 classifier over the
// network attributes and a ports classifier over the port attributes
// are built once over the union ruleset, and the ip6 ports classifier
// joins the two; every filter freezes its own decoder over its own
// projection of the ruleset.
//
// Pins the tree composition (the joined classifier reuses the subtrees
// without rebuilding them), the projection isolation of the decoders,
// and the agreement of the composed ip6 ports filter with a plain
// build of the same signature.

#include "lib/classify/classify.h"
#include "lib/classify/compiler.h"
#include "lib/classify/query.h"

#include "modules/acl/dataplane/filter_lookup.h"

#include "lib/utils/packet.h"

#include "common/memory.h"

#include <assert.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static const struct classify_attr_handlers *sign_ip6[] = {
	CLASSIFY_ATTR(device),
	CLASSIFY_ATTR(vlan),
	CLASSIFY_ATTR(net6_src),
	CLASSIFY_ATTR(net6_dst),
	CLASSIFY_ATTR(proto_range),
};

static const struct classify_attr_handlers *sign_ports[] = {
	CLASSIFY_ATTR(port_src),
	CLASSIFY_ATTR(port_dst),
};

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
		classify_query(                                                \
			filter, sign, &classify_ptr, &classify_result, 1       \
		);                                                             \
		free_packet(&classify_packet);                                 \
		classify_result;                                               \
	})

// The v6 signatures come from the module lookup header; the rest of
// its arrays are referenced so the header stays warning clean.
#define classify_use(q) (void)(q)

int
main(void) {
	classify_use(acl_query_vlan);
	classify_use(acl_query_ip4);
	classify_use(acl_query_ip4_port);
	classify_use(acl_query_core4);
	classify_use(acl_query_frag);
	classify_use(acl_query_ports);
	classify_use(acl_query_core6);
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

	// The acl composition: the ip6 and the ports classifiers are built
	// once over the union ruleset, the ip6 ports classifier joins the
	// two, and every filter freezes its own decoder over its own
	// projection.
	struct classifier *ip6 = classify_build(
		&mctx,
		sign_ip6,
		sizeof(sign_ip6) / sizeof(*sign_ip6),
		union_rules,
		3
	);
	assert(ip6 != NULL);
	struct classifier *ports = classify_build(
		&mctx,
		sign_ports,
		sizeof(sign_ports) / sizeof(*sign_ports),
		union_rules,
		3
	);
	assert(ports != NULL);
	struct classifier *ip6_ports = classify_join(&mctx, ip6, ports);
	assert(ip6_ports != NULL);

	// Verifies that the joined classifier reuses its subtrees: the
	// net6 source attribute of the joined tape is the classifier of
	// the ip6 subtree.
	assert(ip6_ports->attrs[2] == ip6->attrs[2]);

	struct classify_filter filter_ip6;
	struct vline *decoder_ip6 = classify_decode(ip6, &mctx, ip6_rules);
	assert(decoder_ip6 != NULL);
	assert(classify_filter_init(&filter_ip6, &mctx, ip6, decoder_ip6) == 0);

	struct classify_filter filter_ports;
	struct vline *decoder_ports =
		classify_decode(ip6_ports, &mctx, port_rules);
	assert(decoder_ports != NULL);
	assert(classify_filter_init(
		       &filter_ports, &mctx, ip6_ports, decoder_ports
	       ) == 0);

	// The ip6 filter resolves through the network classifier alone and
	// sees the full port rule only.
	assert(classify(&filter_ip6, acl_query_ip6, net_a, net_b, 150, 1000) ==
	       0);
	assert(classify(&filter_ip6, acl_query_ip6, net_a, net_b, 500, 1000) ==
	       0);
	assert(classify(&filter_ip6, acl_query_ip6, net_b, net_a, 150, 150) ==
	       CLASSIFY_RULE_INVALID);

	// The port scoped filter resolves through the joined classifier and
	// never sees the full port rule.
	assert(classify(
		       &filter_ports,
		       acl_query_ip6_port,
		       net_a,
		       net_b,
		       150,
		       1000
	       ) == 1);
	assert(classify(
		       &filter_ports,
		       acl_query_ip6_port,
		       net_a,
		       net_b,
		       500,
		       1000
	       ) == CLASSIFY_RULE_INVALID);
	assert(classify(
		       &filter_ports, acl_query_ip6_port, net_b, net_a, 150, 150
	       ) == 2);
	assert(classify(
		       &filter_ports, acl_query_ip6_port, net_b, net_a, 150, 300
	       ) == CLASSIFY_RULE_INVALID);

	// The shared classification path - the subtree classes evaluated
	// once and combined through the root joint of the joint filter -
	// agrees with the full tape query on the same filter.
	{
		struct classify_filter core_src;
		struct classify_filter ports_src;
		assert(classify_filter_init(&core_src, &mctx, ip6, NULL) == 0);
		assert(classify_filter_init(&ports_src, &mctx, ports, NULL) == 0
		);

		struct packet probe = {0};
		assert(fill_packet_net6(
			       &probe, net_a, net_b, 150, 1000, IPPROTO_UDP, 0
		       ) == 0);
		struct packet *probe_ptr = &probe;

		uint32_t core_classes[1];
		uint32_t port_classes[1];
		uint32_t shared_result[1];
		uint32_t full_result[1];

		classify_classify(
			&core_src,
			acl_query_core6,
			(const struct packet **)&probe_ptr,
			core_classes,
			1
		);
		classify_classify(
			&ports_src,
			acl_query_ports,
			(const struct packet **)&probe_ptr,
			port_classes,
			1
		);
		classify_combine(
			&filter_ports,
			core_classes,
			port_classes,
			shared_result,
			1
		);
		classify_query(
			&filter_ports,
			acl_query_ip6_port,
			&probe_ptr,
			full_result,
			1
		);
		assert(shared_result[0] == full_result[0]);

		free_packet(&probe);
		classify_filter_free(&core_src);
		classify_filter_free(&ports_src);
	}

	// A plain build of the joined signature agrees with the composed
	// classifier on every case above.
	{
		static const struct classify_attr_handlers *sign_flat[] = {
			CLASSIFY_ATTR(device),
			CLASSIFY_ATTR(vlan),
			CLASSIFY_ATTR(net6_src),
			CLASSIFY_ATTR(net6_dst),
			CLASSIFY_ATTR(proto_range),
			CLASSIFY_ATTR(port_src),
			CLASSIFY_ATTR(port_dst),
		};
		struct classifier *flat_cls =
			classify_build(&mctx, sign_flat, 7, union_rules, 3);
		assert(flat_cls != NULL);
		struct vline *flat_decoder =
			classify_decode(flat_cls, &mctx, port_rules);
		assert(flat_decoder != NULL);
		struct classify_filter flat_flt;
		assert(classify_filter_init(
			       &flat_flt, &mctx, flat_cls, flat_decoder
		       ) == 0);

		assert(classify(
			       &flat_flt,
			       acl_query_ip6_port,
			       net_a,
			       net_b,
			       150,
			       1000
		       ) == 1);
		assert(classify(
			       &flat_flt,
			       acl_query_ip6_port,
			       net_a,
			       net_b,
			       500,
			       1000
		       ) == CLASSIFY_RULE_INVALID);
		assert(classify(
			       &flat_flt,
			       acl_query_ip6_port,
			       net_b,
			       net_a,
			       150,
			       150
		       ) == 2);

		classify_filter_free(&flat_flt);
		classify_free(flat_cls);
	}

	// Filters are freed before the classifiers they borrow from; the
	// joined classifier consumes its subtrees and frees them with it.
	classify_filter_free(&filter_ip6);
	classify_filter_free(&filter_ports);
	classify_free(ip6_ports);

	memory_context_fini(&mctx);
	free(memory);
	return 0;
}
