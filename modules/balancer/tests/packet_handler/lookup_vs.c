#include "common/container_of.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/network.h"
#include "config.h"
#include "controlplane/config/cp_module.h"
#include "logging/log.h"
#include "rte_tcp.h"
#include "session.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdlib.h>
#include <string.h>

#include "controlplane.h"

#include "../utils/balancer.h"
#include "../utils/packet.h"

#include "helpers.h"
#include "state.h"
#include "vs.h"
#include "vs_def.h"

////////////////////////////////////////////////////////////////////////////////

#define ARENA_SIZE (1 << 25)

////////////////////////////////////////////////////////////////////////////////

struct lookup_config {
	uint8_t network_proto;
	uint8_t src_ip[NET6_LEN];
	uint8_t dst_ip[NET6_LEN];
	uint16_t src_port;
	uint16_t dst_port;
	uint8_t transport_proto;
	uint8_t tcp_flags;
	uint8_t *expected_addr;
};

int
make_lookups(
	struct lookup_config *lookups,
	size_t count,
	struct balancer_module_config *balancer
) {
	int res;
	for (size_t i = 0; i < count; ++i) {
		struct lookup_config *lookup = &lookups[i];
		struct packet packet;
		if (lookup->network_proto == IPPROTO_IP) {
			res = make_packet4(
				&packet,
				lookup->src_ip,
				lookup->dst_ip,
				lookup->src_port,
				lookup->dst_port,
				lookup->transport_proto,
				lookup->tcp_flags
			);
		} else {
			res = make_packet6(
				&packet,
				lookup->src_ip,
				lookup->dst_ip,
				lookup->src_port,
				lookup->dst_port,
				lookup->transport_proto,
				lookup->tcp_flags
			);
		}
		TEST_ASSERT(res == 0, "can not make packet %zu", i);
		struct balancer_vs *vs = balancer_lookup_vs(balancer, &packet);
		LOG(INFO, "Trying packet %zu...", i);
		if (lookup->expected_addr == NULL) {
			TEST_ASSERT_NULL(vs, "expected no vs, but got some");
		} else {
			TEST_ASSERT_NOT_NULL(
				vs, "expected vs, but not found some"
			);
			TEST_ASSERT_EQUAL(
				(vs->flags & VS_TYPE_V6) != 0,
				lookup->network_proto == IPPROTO_IPV6,
				"got vs with bad address type"
			);
			int cmp_result;
			if (lookup->network_proto == IPPROTO_IPV6) {
				cmp_result = memcmp(
					lookup->expected_addr, vs->address, 16
				);
			} else {
				cmp_result = memcmp(
					lookup->expected_addr, vs->address, 4
				);
			}
			TEST_ASSERT_EQUAL(cmp_result, 0, "got bad vs");
		}
		LOG(INFO, "Packet %zu passed", i);
		free_packet(&packet);
	}
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

int
basic(void *arena) {
	struct block_allocator alloc;
	int res = block_allocator_init(&alloc);
	TEST_ASSERT_EQUAL(res, 0, "can not init block allocator");

	block_allocator_put_arena(&alloc, arena, ARENA_SIZE);
	struct memory_context mctx;
	res = memory_context_init(&mctx, "test", &alloc);
	TEST_ASSERT_EQUAL(res, 0, "can not init memory context");

	struct balancer_state *state = make_balancer_state(&mctx, 1, 100);
	struct balancer_session_timeouts timeouts = {1, 2, 3, 4, 5, 6};
	struct cp_module *balancer_module =
		make_balancer(&mctx, &timeouts, state);
	struct balancer_module_config *balancer = container_of(
		balancer_module, struct balancer_module_config, cp_module
	);

	// configure first service (1.1.1.1)

	uint8_t first_service_addr[4] = {1, 1, 1, 1};
	uint16_t first_service_port = 80;
	uint8_t first_service_proto = IPPROTO_TCP;
	struct balancer_service_config *first_service_config =
		balancer_service_config_create(
			0,
			first_service_addr,
			first_service_port,
			first_service_proto,
			0,
			2
		);
	TEST_ASSERT_NOT_NULL(
		first_service_config,
		"cannot create config for the first service"
	);

	uint8_t first_service_allowed_from1[4] = {10, 1, 0, 1};
	uint8_t first_service_allowed_to1[4] = {10, 10, 255, 255};
	balancer_service_config_set_src_prefix(
		first_service_config,
		0,
		first_service_allowed_from1,
		first_service_allowed_to1
	);

	uint8_t first_service_allowed_from2[4] = {10, 2, 0, 1};
	uint8_t first_service_allowed_to2[4] = {10, 12, 0, 1};
	balancer_service_config_set_src_prefix(
		first_service_config,
		1,
		first_service_allowed_from2,
		first_service_allowed_to2
	);

	res = balancer_module_config_add_service(
		balancer_module, first_service_config
	);
	TEST_ASSERT_EQUAL(
		res, 0, "can not add first service to the balancer module"
	);

	// configure second service (2.2.2.2)

	uint8_t second_service_addr[16];
	memset(second_service_addr, 2, 16);
	uint16_t second_service_port = 1010;
	uint8_t second_service_proto = IPPROTO_UDP;
	struct balancer_service_config *second_service_config =
		balancer_service_config_create(
			VS_TYPE_V6,
			second_service_addr,
			second_service_port,
			second_service_proto,
			0,
			2
		);
	TEST_ASSERT_NOT_NULL(
		first_service_config,
		"cannot create config for the first service"
	);

	uint8_t second_service_allowed_from1[16] = {10, 1, 0, 1};
	memset(second_service_allowed_from1 + 4, 0, 12);
	uint8_t second_service_allowed_to1[16] = {10, 10, 255, 255};
	memset(second_service_allowed_to1 + 4, 1, 12);
	balancer_service_config_set_src_prefix(
		second_service_config,
		0,
		second_service_allowed_from1,
		second_service_allowed_to1
	);

	uint8_t second_service_allowed_from2[16] = {10, 2, 0, 1};
	memset(second_service_allowed_from2 + 4, 1, 12);
	uint8_t second_service_allowed_to2[16] = {10, 12, 0, 1};
	memset(second_service_allowed_to2 + 4, 0, 12);
	balancer_service_config_set_src_prefix(
		second_service_config,
		1,
		second_service_allowed_from2,
		second_service_allowed_to2
	);

	res = balancer_module_config_add_service(
		balancer_module, second_service_config
	);
	TEST_ASSERT_EQUAL(
		res, 0, "can not add first service to the balancer module"
	);

	// all three services are configured

	// first service (1.1.1.1:80 tcp)
	//  allowed src range 1: [10, 1, 0, 1] - [10, 10, 255, 255]
	//  allowed src range 2: [10, 2, 0, 1] - [10, 12, 0, 1]
	//
	// second service (2.2.2.2.2....:1010 udp)
	//  allowed src range 1: [10, 1, 0, 1, 0, 0, ...] - [10, 10, 255, 255,
	//  1, 1, ...] allowed src range 2: [10, 2, 0, 1, 1, 1, ...] - [10, 12,
	//  0, 1, 0, 0, ...]

	// make lookups
	struct lookup_config lookups[] = {
		{// correct packet to the first service
		 IPPROTO_IP,
		 {10, 2, 123, 13},
		 {1, 1, 1, 1},
		 1000,
		 80,
		 IPPROTO_TCP,
		 RTE_TCP_SYN_FLAG,
		 first_service_addr
		},
		{// second correct packet to the first service
		 IPPROTO_IP,
		 {10, 5, 3, 10},
		 {1, 1, 1, 1},
		 2222,
		 80,
		 IPPROTO_TCP,
		 RTE_TCP_FIN_FLAG,
		 first_service_addr
		},
		{// third correct packet to the first service
		 IPPROTO_IP,
		 {10, 4, 3, 10},
		 {1, 1, 1, 1},
		 2222,
		 80,
		 IPPROTO_TCP,
		 RTE_TCP_FIN_FLAG,
		 first_service_addr
		},
		{// correct packet to the first service expect of the transport
		 // proto
		 IPPROTO_IP,
		 {10, 2, 123, 13},
		 {1, 1, 1, 1},
		 1000,
		 80,
		 IPPROTO_UDP,
		 0,
		 NULL
		},
		{// correct packet to the first service expect of the port
		 IPPROTO_IP,
		 {10, 2, 123, 13},
		 {1, 1, 1, 1},
		 1000,
		 81,
		 IPPROTO_TCP,
		 0,
		 NULL
		},
		{// correct packet to the first service expect of the src ip
		 IPPROTO_IP,
		 {10, 0, 123, 13},
		 {1, 1, 1, 1},
		 1000,
		 80,
		 IPPROTO_TCP,
		 0,
		 NULL
		},
		{// correct packet to the first service from the second src ip
		 // range
		 IPPROTO_IP,
		 {10, 11, 123, 13},
		 {1, 1, 1, 1},
		 1000,
		 80,
		 IPPROTO_TCP,
		 RTE_TCP_SYN_FLAG,
		 first_service_addr
		},
		{// correct packet to the first service expect of the src ip
		 IPPROTO_IP,
		 {5, 11, 123, 13},
		 {1, 1, 1, 1},
		 1000,
		 80,
		 IPPROTO_TCP,
		 RTE_TCP_SYN_FLAG,
		 NULL
		},
		{// correct packet to the first service expect dst ip
		 IPPROTO_IP,
		 {10, 11, 123, 13},
		 {1, 2, 1, 1},
		 1000,
		 80,
		 IPPROTO_TCP,
		 RTE_TCP_SYN_FLAG,
		 NULL
		},
		// second service (2.2.2.2.2....:1010 udp)
		//  allowed src range 1: [10, 1, 0, 1, 0, 0, ...] - [10, 10,
		//  255, 255, 1, 1, ...]
		//  allowed src range 2: [10, 2, 0, 1, 1, 1, ...] - [10, 12, 0,
		//  1, 0, 0, ...]
		// add packets for the second service
		{// correct packet to the second service
		 IPPROTO_IPV6,
		 {
			 10,
			 1,
			 1,
		 },
		 {2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2},
		 2025,
		 1010,
		 IPPROTO_UDP,
		 0,
		 second_service_addr
		},
		{// the second correct packet to the second service
		 IPPROTO_IPV6,
		 {
			 10,
			 12,
			 0,
			 0,
		 },
		 {2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2},
		 1,
		 1010,
		 IPPROTO_UDP,
		 0,
		 second_service_addr
		},
		{// correct packet to the second service expect of the proto
		 IPPROTO_IPV6,
		 {
			 10,
			 5,
			 245,
		 },
		 {2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2},
		 1,
		 1010,
		 IPPROTO_TCP,
		 0,
		 NULL
		},
		{// correct packet to the second service expect of the port
		 IPPROTO_IPV6,
		 {
			 10,
			 5,
			 245,
		 },
		 {2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2},
		 1,
		 1009,
		 IPPROTO_UDP,
		 0,
		 NULL
		},
		{// correct packet to the second service expect of src ip
		 IPPROTO_IPV6,
		 {
			 9,
			 5,
			 245,
		 },
		 {2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2},
		 1,
		 1010,
		 IPPROTO_UDP,
		 0,
		 NULL
		},
		{// correct packet to the second service expect of dst ip
		 IPPROTO_IPV6,
		 {
			 10,
			 5,
			 245,
		 },
		 {2, 2, 2, 2, 2, 2, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2},
		 1,
		 1010,
		 IPPROTO_UDP,
		 0,
		 NULL
		}
	};

	size_t lookups_cnt = sizeof(lookups) / sizeof(struct lookup_config);
	res = make_lookups(lookups, lookups_cnt, balancer);
	TEST_ASSERT_EQUAL(res, 0, "Failed to make first lookups");

	// Add third service with pure L3 balancing

	uint8_t third_service_ip[4] = {3, 3, 3, 3};

	// Add with specified port
	struct balancer_service_config *third_service =
		balancer_service_config_create(
			VS_PURE_L3, third_service_ip, 123, IPPROTO_UDP, 0, 1
		);
	uint8_t start_addr[16];
	memset(start_addr, 0, 16);
	uint8_t end_addr[16] = {255, 255, 255, 0};
	memset(end_addr + 4, 0, 12);
	balancer_service_config_set_src_prefix(
		third_service, 0, start_addr, end_addr
	);
	res = balancer_module_config_add_service(
		balancer_module, third_service
	);
	TEST_ASSERT_EQUAL(res, 0, "can not add third service");

	// Add fourth IPv6 service with pure L3 balancing
	uint8_t fourth_service_ip[16];
	memset(fourth_service_ip, 4, 16);
	struct balancer_service_config *fourth_service =
		balancer_service_config_create(
			IPPROTO_IPV6, fourth_service_ip, 0, IPPROTO_TCP, 0, 1
		);
	balancer_service_config_set_src_prefix(
		fourth_service, 0, start_addr, end_addr
	);
	res = balancer_module_config_add_service(
		balancer_module, fourth_service
	);
	TEST_ASSERT_EQUAL(res, 0, "can not add fourth service");

	// Repeat lookups

	LOG(INFO, "Added 2 new services, repeat lookups");

	res = make_lookups(lookups, lookups_cnt, balancer);
	TEST_ASSERT_EQUAL(
		res, 0, "Failed to repeat lookups after add two new services"
	);

	LOG(INFO, "Make lookups for pure L3 services...");

	// third: upd, pure l3, and ip is 3.3.3.3
	// fourth: tcp, pure l3, and ip is 4.4.4.4.4.4.......
	struct lookup_config lookups1[] = {
		{// packet for the third service
		 IPPROTO_IP,
		 {10, 1, 2, 3},
		 {3, 3, 3, 3},
		 100,
		 200,
		 IPPROTO_UDP,
		 0,
		 third_service_ip
		},
		{// packet for the third service
		 IPPROTO_IP,
		 {10, 1, 2, 3},
		 {3, 3, 3, 3},
		 1010,
		 50000,
		 IPPROTO_UDP,
		 0,
		 third_service_ip
		},
		{// packet for the third service expect proto
		 IPPROTO_IP,
		 {10, 1, 2, 3},
		 {3, 3, 3, 3},
		 1010,
		 50000,
		 IPPROTO_TCP,
		 0,
		 NULL
		},
		{// packet for the third service except src
		 IPPROTO_IP,
		 {255, 255, 255, 255},
		 {3, 3, 3, 3},
		 1010,
		 50000,
		 IPPROTO_UDP,
		 0,
		 NULL
		},
		{// packet for the third service except dst
		 IPPROTO_IP,
		 {2, 2, 2, 2},
		 {3, 4, 3, 3},
		 1010,
		 50000,
		 IPPROTO_UDP,
		 0,
		 NULL
		},
		{// packet for the fourth service
		 IPPROTO_IPV6,
		 {1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		 {4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4},
		 1010,
		 123,
		 IPPROTO_TCP,
		 RTE_TCP_SYN_FLAG,
		 fourth_service_ip
		},
		{// packet for the fourth service
		 IPPROTO_IPV6,
		 {1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		 {4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4},
		 1010,
		 5566,
		 IPPROTO_TCP,
		 RTE_TCP_FIN_FLAG,
		 fourth_service_ip
		},
		{// packet for the fourth service except proto
		 IPPROTO_IPV6,
		 {1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		 {4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4},
		 1010,
		 5566,
		 IPPROTO_UDP,
		 RTE_TCP_FIN_FLAG,
		 NULL
		},
		{// packet for the fourth service except proto
		 IPPROTO_IPV6,
		 {1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		 {4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4},
		 1010,
		 5566,
		 IPPROTO_UDP,
		 0,
		 NULL
		},
		{// packet for the fourth service except src_ip
		 IPPROTO_IPV6,
		 {255, 255, 255, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		 {4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4},
		 1010,
		 5566,
		 IPPROTO_TCP,
		 RTE_TCP_FIN_FLAG,
		 NULL
		},
		{// packet for the fourth service except dst_ip
		 IPPROTO_IPV6,
		 {1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		 {4, 4, 4, 4, 8, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4},
		 1010,
		 5566,
		 IPPROTO_TCP,
		 0,
		 NULL
		}
	};

	lookups_cnt = sizeof(lookups1) / sizeof(struct lookup_config);
	return make_lookups(lookups1, lookups_cnt, balancer);
}

////////////////////////////////////////////////////////////////////////////////

int
many_services(void *arena) {
	(void)arena;
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");

	void *arena = malloc(ARENA_SIZE);
	if (arena == NULL) {
		return 1;
	}

	LOG(INFO, "Running 'basic' test...");
	if (basic(arena) == TEST_FAILED) {
		LOG(ERROR, "Test 'basic' failed");
		return 1;
	}

	LOG(INFO, "Running 'many_services' test...");
	if (many_services(arena) == TEST_FAILED) {
		LOG(ERROR, "Test 'many_services' failed");
		return 1;
	}

	free(arena);

	LOG(INFO, "All tests have completed successfully");

	return 0;
}