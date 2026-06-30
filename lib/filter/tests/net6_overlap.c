#include "lib/filter/compiler.h"
#include "lib/filter/filter.h"
#include "lib/filter/query.h"

#include "lib/filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>
#include <string.h>

FILTER_COMPILER_DECLARE(sign_net6_compile, net6_src, net6_dst);
FILTER_QUERY_DECLARE(sign_net6, net6_src, net6_dst);

// The IPv6 classifier could match a packet to the wrong rule
// when a wide prefix and a single address inside it appear across several
// rules. The bug needs a source match too, so every rule has a dst and a src.
//
// Rules in priority order (lower number wins):
//   rule 0 - dst: fe80::/64 and fe80::f15;  src: 2a02:6b8::/32
//   rule 1 - dst: fe80::f15;                src: any
//   rule 2 - dst: fe80::/64;                src: any
//
// The probe's source is not in rule 0, and its destination is in fe80::/64
// but not fe80::f15. Only rule 2 matches it, so the filter must return 2;
// the bug returns 1.

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

static uint32_t
query_one(
	struct filter *filter,
	const uint8_t src[NET6_LEN],
	const uint8_t dst[NET6_LEN]
) {
	struct packet packet = {0};
	int res = fill_packet_net6(&packet, src, dst, 100, 200, IPPROTO_UDP, 0);
	assert(res == 0);

	struct packet *packet_ptr = &packet;
	uint32_t action;
	filter_query(filter, sign_net6, &packet_ptr, &action, 1);
	free_packet(&packet);

	return action;
}

int
main() {
	log_enable_name("info");
	void *memory = malloc(1 << 24);

	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context mctx;
	int res = memory_context_init(&mctx, "test", &allocator);
	assert(res == 0);

	const uint8_t any[NET6_LEN] = {0};
	const uint8_t wide_prefix[NET6_LEN] = {0xfe, 0x80};
	uint8_t single_address[NET6_LEN] = {0xfe, 0x80};
	single_address[14] = 0x0f;
	single_address[15] = 0x15;
	const uint8_t src_prefix[NET6_LEN] = {0x2a, 0x02, 0x06, 0xb8};

	// rule 0: wide prefix + the address, fixed source
	struct filter_rule_builder b0;
	builder_init(&b0);
	builder_add_net6_dst(&b0, net6_cidr(wide_prefix, 64));
	builder_add_net6_dst(&b0, net6_cidr(single_address, 128));
	builder_add_net6_src(&b0, net6_cidr(src_prefix, 32));
	struct filter_rule rule0 = build_rule(&b0);

	// rule 1: the address only, any source
	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_net6_dst(&b1, net6_cidr(single_address, 128));
	builder_add_net6_src(&b1, net6_cidr(any, 0));
	struct filter_rule rule1 = build_rule(&b1);

	// rule 2: the wide prefix only, any source
	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_net6_dst(&b2, net6_cidr(wide_prefix, 64));
	builder_add_net6_src(&b2, net6_cidr(any, 0));
	struct filter_rule rule2 = build_rule(&b2);

	const struct filter_rule *rule_ptrs[3] = {&rule0, &rule1, &rule2};

	struct filter filter;
	res = filter_init(
		&filter, sign_net6_compile, rule_ptrs, 3, &mctx, NULL
	);
	assert(res == 0);

	// packet: src outside rule 0, dst in the wide prefix but not the
	// address — matches only rule 2.
	const uint8_t src[NET6_LEN] = {
		0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
	};
	uint8_t dst[NET6_LEN] = {0xfe, 0x80};
	dst[14] = 0x12;
	dst[15] = 0x34;

	uint32_t action = query_one(&filter, src, dst);
	LOG(INFO, "dst fe80::1234 -> rule %u (expected 2)", action);
	assert(action == 2);

	LOG(INFO, "net6 overlap test passed");

	filter_free(&filter, sign_net6_compile);
	memory_context_fini(&mctx);
	free(memory);

	return 0;
}
