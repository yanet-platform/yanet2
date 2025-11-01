#include <assert.h>
#include <netinet/in.h>
#include <pcap/pcap.h>
#include <stddef.h>
#include <stdlib.h>

#include "../utils/packet.h"
#include "../utils/rng.h"

#include "api/module.h"
#include "api/session.h"
#include "api/session_table.h"
#include "api/vs.h"

#include "dataplane/session.h"
#include "dataplane/session_table.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/packet/decap.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/logging/log.h"

#include "dataplane/real.h"
#include "dataplane/select.h"
#include "dataplane/tunnel.h"
#include "dataplane/vs.h"

#include "modules/pdump/tests/helpers.h"
#include "rte_byteorder.h"
#include "rte_ip.h"
#include "rte_tcp.h"
#include "tests/utils/mock.h"

////////////////////////////////////////////////////////////////////////////////

#define ARENA_SIZE (1 << 28) + 100000
#define AGENT_MEMORY (1 << 28)

static uint8_t null_addr[NET6_LEN];
static uint8_t full_addr[NET6_LEN];

////////////////////////////////////////////////////////////////////////////////

struct balancer_instance {
	struct agent *agent;
	struct balancer_session_table *session_table;
	struct balancer_sessions_timeouts *timeouts;
};

////////////////////////////////////////////////////////////////////////////////

static int
create_service(
	struct balancer_vs_config **vs_config,
	struct agent *agent,
	vs_flags_t vs_flags,
	uint8_t *vip,
	uint16_t vs_port,
	uint8_t vs_proto,
	real_flags_t rs_flags,
	uint8_t *real_dst,
	uint8_t *real_src,
	uint8_t *real_mask
) {
	*vs_config = balancer_vs_config_create(
		agent, vs_flags, vip, vs_port, vs_proto, 1, 1
	);
	TEST_ASSERT_NOT_NULL(*vs_config, "failed to create service config");
	balancer_vs_config_set_allowed_src_range(
		*vs_config, 0, null_addr, full_addr
	);
	balancer_vs_config_set_real(
		*vs_config, 0, rs_flags, 1, real_dst, real_src, real_mask
	);
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

int
check_packet_network_fields(
	struct packet *packet,
	uint8_t network_proto,
	uint8_t *src_ip,
	uint8_t *dst_ip
) {
	if (network_proto == IPPROTO_IP) {
		struct rte_ipv4_hdr *ip = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		TEST_ASSERT_EQUAL(
			memcmp(src_ip, &ip->src_addr, NET4_LEN),
			0,
			"unexpected src addr"
		);
		TEST_ASSERT_EQUAL(
			memcmp(dst_ip, &ip->dst_addr, NET4_LEN),
			0,
			"unexpected dst addr"
		);
	} else if (network_proto == IPPROTO_IPV6) {
		struct rte_ipv6_hdr *ip = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		TEST_ASSERT_EQUAL(
			memcmp(src_ip, &ip->src_addr, NET6_LEN),
			0,
			"unexpected src addr"
		);
		TEST_ASSERT_EQUAL(
			memcmp(dst_ip, &ip->dst_addr, NET6_LEN),
			0,
			"unexpected dst addr"
		);
	} else {
		LOG(ERROR, "unexpected network_proto: %u\n", network_proto);
		return TEST_FAILED;
	}
	return TEST_SUCCESS;
}

int
check_packet_fields(
	struct packet *packet,
	uint8_t network_proto,
	uint8_t transport_proto,
	uint8_t *src_ip,
	uint8_t *dst_ip,
	uint16_t src_port,
	uint16_t dst_port
) {
	struct packet_metadata meta;
	int res = fill_packet_metadata(packet, &meta);
	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet metadata");
	TEST_ASSERT_EQUAL(
		meta.network_proto, network_proto, "unexpected network proto"
	);
	TEST_ASSERT_EQUAL(
		meta.transport_proto,
		transport_proto,
		"unexpected transport proto"
	);
	TEST_ASSERT_EQUAL(
		memcmp(src_ip,
		       meta.src_addr,
		       network_proto == IPPROTO_IP ? NET4_LEN : NET6_LEN),
		0,
		"unexpected src addr"
	);
	TEST_ASSERT_EQUAL(
		memcmp(dst_ip,
		       meta.dst_addr,
		       network_proto == IPPROTO_IP ? NET4_LEN : NET6_LEN),
		0,
		"unexpected dst addr"
	);
	TEST_ASSERT_EQUAL(meta.src_port, src_port, "unexpected src port");
	TEST_ASSERT_EQUAL(meta.dst_port, dst_port, "unexpected dst port");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

static int
tun_packet(struct balancer_module_config *balancer, struct packet *packet) {
	struct virtual_service *vs = vs_lookup(balancer, packet);
	TEST_ASSERT_NOT_NULL(vs, "failed to lookup vs");
	struct packet_metadata meta;
	int res = fill_packet_metadata(packet, &meta);
	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet metadata");
	struct real *rs = select_real(balancer, 0, 0, vs, &meta);
	TEST_ASSERT_NOT_NULL(rs, "failed to select rs");
	res = tunnel_packet(vs->flags, rs, packet);
	TEST_ASSERT_EQUAL(res, 0, "failed to tunnel packet");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

void
tunnelled_packet_src(
	uint8_t network_proto_hop1,
	uint8_t network_proto_hop2,
	uint8_t *res,
	uint8_t *u_src,
	uint8_t *real_mask
) {
	size_t bytes = (network_proto_hop1 == IPPROTO_IPV6 &&
			network_proto_hop2 == IPPROTO_IPV6)
			       ? NET6_LEN
			       : NET4_LEN;
	for (size_t i = 0; i < bytes; ++i) {
		res[i] |= u_src[i] & (~real_mask[i]);
	}
}

////////////////////////////////////////////////////////////////////////////////

static int
tunnel(struct balancer_instance *instance,
       uint8_t *u_src,
       uint16_t u_port,
       vs_flags_t vs_flags,
       uint8_t *vs_dst,
       uint16_t vs_port,
       uint8_t vs_proto,
       real_flags_t rs_flags,
       uint8_t *rs_dst,
       uint8_t *rs_src,
       uint8_t *rs_mask) {
	struct balancer_vs_config *vs_config;
	int res = create_service(
		&vs_config,
		instance->agent,
		vs_flags,
		vs_dst,
		vs_port,
		vs_proto,
		rs_flags,
		rs_dst,
		rs_src,
		rs_mask
	);
	TEST_ASSERT_EQUAL(res, TEST_SUCCESS, "failed to create vs config");

	struct cp_module *cp_module = balancer_module_config_create(
		instance->agent,
		"balancer",
		instance->session_table,
		1,
		&vs_config,
		instance->timeouts
	);
	TEST_ASSERT_NOT_NULL(
		cp_module, "failed to create balancer module config"
	);
	struct balancer_module_config *balancer = container_of(
		cp_module, struct balancer_module_config, cp_module
	);

	struct packet packet;
	uint8_t user_to_vs_network_proto =
		(vs_flags & BALANCER_VS_IPV6_FLAG) ? IPPROTO_IPV6 : IPPROTO_IP;
	res = make_packet_generic(
		&packet,
		u_src,
		vs_dst,
		u_port,
		vs_port,
		vs_proto,
		user_to_vs_network_proto,
		RTE_TCP_SYN_FLAG
	);
	TEST_ASSERT_EQUAL(res, 0, "failed to make packet");

	res = tun_packet(balancer, &packet);
	TEST_ASSERT_EQUAL(res, TEST_SUCCESS, "failed to tunnel packet");

	res = parse_packet(&packet);
	TEST_ASSERT_EQUAL(res, 0, "parse packet failed");

	uint8_t vs_to_rs_network_proto = (rs_flags & BALANCER_REAL_IPV6_FLAG)
						 ? IPPROTO_IPV6
						 : IPPROTO_IP;

	uint8_t expected_src[NET6_LEN];
	memcpy(expected_src,
	       rs_src,
	       (vs_to_rs_network_proto == IPPROTO_IPV6) ? NET6_LEN : NET4_LEN);
	tunnelled_packet_src(
		user_to_vs_network_proto,
		vs_to_rs_network_proto,
		expected_src,
		u_src,
		rs_mask
	);

	res = check_packet_network_fields(
		&packet, vs_to_rs_network_proto, expected_src, rs_dst
	);
	TEST_ASSERT_EQUAL(
		res, TEST_SUCCESS, "encap packet network fields inconsistent"
	);

	res = packet_decap(&packet);
	TEST_ASSERT_EQUAL(res, 0, "faield to decap packet");

	res = parse_packet(&packet);
	TEST_ASSERT_EQUAL(res, 0, "faield to parse packet after decap");

	res = check_packet_fields(
		&packet,
		user_to_vs_network_proto,
		vs_proto,
		u_src,
		vs_dst,
		rte_cpu_to_be_16(u_port),
		rte_cpu_to_be_16(vs_port)
	);
	TEST_ASSERT_EQUAL(
		res, TEST_SUCCESS, "decap packet fields inconsistent"
	);

	free_packet(&packet);
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

static int
tunnel_packets(
	struct balancer_instance *instance,
	uint8_t hop1_network_proto,
	uint8_t hop2_network_proto,
	size_t from,
	size_t to,
	uint64_t *rng
) {
	for (size_t i = from; i < to; ++i) {
		uint8_t vs_dst[NET6_LEN];
		memset(vs_dst, 0x01, NET6_LEN);
		vs_dst[0] = i & 0xFF;

		const uint16_t vs_port = 10010;
		const uint8_t vs_proto =
			rng_next(rng) % 2 == 0 ? IPPROTO_TCP : IPPROTO_UDP;
		vs_flags_t vs_flags =
			(hop1_network_proto == IPPROTO_IPV6
				 ? BALANCER_VS_IPV6_FLAG
				 : 0);
		if (rng_next(rng) % 2 == 0) {
			vs_flags |= BALANCER_VS_PURE_L3_FLAG;
		}

		uint8_t rs_dst[NET6_LEN];
		memset(rs_dst, 0x02, NET6_LEN);
		rs_dst[0] = i & 0xFF;

		uint8_t rs_src[NET6_LEN];
		memset(rs_src, 0x14, NET6_LEN);
		rs_src[0] = (3 * i) & 0xFF;

		uint8_t rs_mask[NET6_LEN];
		memset(rs_mask, 0x07, NET6_LEN);

		for (size_t i = 0; i < NET6_LEN; ++i) {
			rs_src[i] &= rs_mask[i];
		}

		const real_flags_t rs_flags =
			(hop2_network_proto == IPPROTO_IPV6
				 ? BALANCER_REAL_IPV6_FLAG
				 : 0);

		uint8_t u_src[NET6_LEN];
		memset(u_src, 0xFF, NET6_LEN);
		const uint16_t u_port = 10024;
		u_src[0] = rng_next(rng) & 0xFF;

		int res =
			tunnel(instance,
			       u_src,
			       u_port,
			       vs_flags,
			       vs_dst,
			       vs_port,
			       vs_proto,
			       rs_flags,
			       rs_dst,
			       rs_src,
			       rs_mask);

		if (res != TEST_SUCCESS) {
			LOG(ERROR,
			    "Tunneling %lu failed: "
			    "BALANCER_VS_PURE_L3_FLAG=%u, proto=%s",
			    i,
			    !((vs_flags & BALANCER_VS_PURE_L3_FLAG) == 0),
			    vs_proto == IPPROTO_TCP ? "TCP" : "UDP");
			return TEST_FAILED;
		}
	}
	return TEST_SUCCESS;
}

static int
tunnel_ipv6_ipv6(struct balancer_instance *balancer) {
	uint64_t rng = 1231;
	return tunnel_packets(
		balancer, IPPROTO_IPV6, IPPROTO_IPV6, 0, 25, &rng
	);
}

////////////////////////////////////////////////////////////////////////////////

static int
tunnel_ipv4_ipv6(struct balancer_instance *balancer) {
	uint64_t rng = 555;
	return tunnel_packets(balancer, IPPROTO_IP, IPPROTO_IPV6, 25, 50, &rng);
}

////////////////////////////////////////////////////////////////////////////////

static int
tunnel_ipv6_ipv4(struct balancer_instance *balancer) {
	uint64_t rng = 333;
	return tunnel_packets(balancer, IPPROTO_IPV6, IPPROTO_IP, 50, 75, &rng);
}

////////////////////////////////////////////////////////////////////////////////

static int
tunnel_ipv4_ipv4(struct balancer_instance *balancer) {
	uint64_t rng = 11;
	return tunnel_packets(balancer, IPPROTO_IP, IPPROTO_IP, 75, 100, &rng);
}

////////////////////////////////////////////////////////////////////////////////

static void
init_globals() {
	memset(null_addr, 0, NET6_LEN);
	memset(full_addr, 0xFF, NET6_LEN);
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");

	LOG(INFO, "Starting tunnel tests...");

	init_globals();

	void *arena = malloc(ARENA_SIZE);
	TEST_ASSERT_NOT_NULL(arena, "failed to allocate arena");

	struct mock *mock = mock_init(arena, ARENA_SIZE);
	TEST_ASSERT_NOT_NULL(mock, "failed to init mock");

	struct agent *agent = mock_create_agent(mock, AGENT_MEMORY);
	TEST_ASSERT_NOT_NULL(agent, "failed to create agent");

	struct balancer_session_table *session_table =
		balancer_session_table_create(agent, 1000);
	TEST_ASSERT_NOT_NULL(session_table, "failed to create session table");

	struct balancer_sessions_timeouts *timeouts =
		balancer_sessions_timeouts_create(agent, 1, 2, 3, 4, 5, 6);
	TEST_ASSERT_NOT_NULL(timeouts, "failed to create sessions timeouts");

	struct balancer_instance balancer = {agent, session_table, timeouts};

	typedef int (*test_func)(struct balancer_instance *);

	struct test_case {
		test_func func;
		const char *name;
	} test_cases[] = {
		{tunnel_ipv6_ipv6, "IPv6 virtual and IPv6 real"},
		{tunnel_ipv6_ipv4, "IPv6 virtual and IPv4 real"},
		{tunnel_ipv4_ipv4, "IPv4 virtual and IPv4 real"},
		{tunnel_ipv4_ipv6, "IPv4 virtual and IPv6 real"},
	};

	size_t failed_tests = 0;
	size_t test_case_count = sizeof(test_cases) / sizeof(*test_cases);
	for (size_t i = 0; i < test_case_count; ++i) {
		struct test_case *test = &test_cases[i];
		LOG(INFO, "Test '%s'...", test->name);
		int res = test->func(&balancer);
		if (res == TEST_FAILED) {
			++failed_tests;
			LOG(ERROR, "Test '%s' failed", test->name);
		} else {
			assert(res == TEST_SUCCESS);
			LOG(INFO, "Test `%s` succeed", test->name);
		}
	}

	free(arena);

	if (failed_tests == 0) {
		LOG(INFO, "All tests have been passed");
		return 0;
	} else {
		LOG(ERROR,
		    "Tests failed: %lu/%lu",
		    failed_tests,
		    test_case_count);
		return 1;
	}
}