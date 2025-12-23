#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdio.h>

FILTER_COMPILER_DECLARE(
	sign_net4_ports, port_src, port_dst, net4_src, net4_dst
);
FILTER_QUERY_DECLARE(sign_net4_ports, port_src, port_dst, net4_src, net4_dst);

static void
query_and_expect_action(
	struct filter *filter,
	uint8_t sip[NET4_LEN],
	uint8_t dip[NET4_LEN],
	uint16_t src_port,
	uint16_t dst_port,
	uint32_t expected
) {
	struct packet p = {0};
	int res = fill_packet_net4(
		&p, sip, dip, src_port, dst_port, IPPROTO_UDP, 0
	);
	assert(res == 0);
	uint32_t *actions;
	uint32_t actions_count;
	FILTER_QUERY(filter, sign_net4_ports, &p, &actions, &actions_count);
	assert(actions_count >= 1);
	assert(actions[0] == expected);
	free_packet(&p);
}

static void
test(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);

	block_allocator_put_arena(&allocator, memory, 1 << 26);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// actions

	// a1:
	//  src_port: 100-500
	//  dst_port: 200-250
	//  net4_src: 198.233.0.0/16
	//  net4_dst: 192.0.0.0/8
	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_port_src_range(&b1, 100, 500);
	builder_add_port_dst_range(&b1, 200, 250);
	builder_add_net4_src(&b1, ip(198, 233, 0, 0), ip(255, 255, 0, 0));
	builder_add_net4_dst(&b1, ip(192, 0, 0, 0), ip(255, 0, 0, 0));
	struct filter_rule a1 = build_rule(&b1, 1);

	// a2:
	//  src_port: 200-300
	//  dst_port: 100-300
	//  net4_src: 198.233.10.0/24
	//  net4_dst: 192.0.0.0/8
	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_port_src_range(&b2, 200, 300);
	builder_add_port_dst_range(&b2, 100, 300);
	builder_add_net4_src(&b2, ip(198, 233, 10, 0), ip(255, 255, 255, 0));
	builder_add_net4_dst(&b2, ip(192, 0, 0, 0), ip(255, 0, 0, 0));
	struct filter_rule a2 = build_rule(&b2, 2);

	struct filter_rule actions[2] = {a1, a2};

	// build filter
	struct filter filter;
	res = FILTER_INIT(
		&filter, sign_net4_ports, actions, 2, &memory_context
	);
	assert(res == 0);

	// make queries

	query_and_expect_action(
		&filter, ip(198, 233, 10, 15), ip(192, 1, 1, 1), 200, 230, 1
	);

	query_and_expect_action(
		&filter, ip(198, 233, 10, 15), ip(192, 1, 1, 1), 200, 150, 2
	);

	FILTER_FREE(&filter, sign_net4_ports);
}

////////////////////////////////////////////////////////////////////////////////

static int
next_permutation(uint32_t *a, size_t n) {
	if (n <= 1) {
		return 0;
	}
	for (ssize_t i = n - 2; i >= 0; --i) {
		if (a[i] < a[i + 1]) {
			// find the least a[j] s.t a[j] > a[i]
			for (ssize_t j = n - 1; j > i; --j) {
				if (a[j] > a[i]) {
					// swap (a[i], a[j])
					uint32_t tmp = a[i];
					a[i] = a[j];
					a[j] = tmp;
					break;
				}
			}
			// reverse prefix
			size_t len = n - i - 1;
			for (size_t j = 0; j < len / 2; ++j) {
				// swap (a[i + j + 1, a[n - j - 1]])
				uint32_t tmp = a[i + j + 1];
				a[i + j + 1] = a[n - j - 1];
				a[n - j - 1] = tmp;
			}
			return 1;
		}
	}
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");
	void *memory = malloc(1 << 26); // 64MB

	uint32_t perm[4] = {0, 1, 2, 3};

	uint32_t check_counter = 0;
	do {
		test(memory);
		++check_counter;
	} while (next_permutation(perm, 4));

	assert(check_counter == 24);

	LOG(INFO, "OK");
	LOG(INFO, "checked %u attribute permutations", check_counter);

	free(memory);

	return 0;
}