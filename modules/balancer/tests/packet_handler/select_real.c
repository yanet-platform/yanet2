#include "../utils/packet.h"
#include "../utils/rng.h"
#include "api/module.h"
#include "api/session.h"
#include "api/session_table.h"
#include "api/vs.h"
#include "common/network.h"

#include "dataplane/real.h"
#include "dataplane/select.h"
#include "dataplane/vs.h"
#include "logging/log.h"
#include "modules/pdump/tests/helpers.h"
#include "rte_common.h"

#include "rte_hash_crc.h"
#include "rte_tcp.h"
#include <assert.h>
#include <math.h>
#include <netinet/in.h>

#include <stdatomic.h>

#include "dataplane/module.h"
#include "tests/utils/mock.h"

////////////////////////////////////////////////////////////////////////////////

#define ARENA_SIZE (1 << 27) + 1000000
#define AGENT_MEMORY (1 << 27)

////////////////////////////////////////////////////////////////////////////////

uint32_t current_time;

struct real *
lookup_rs(
	struct balancer_module_config *balancer,
	uint8_t *src_ip,
	uint8_t *dst_ip,
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t transport_proto,
	uint8_t network_proto,
	uint16_t tcp_flags
) {
	struct packet packet;
	int res = make_packet_generic(
		&packet,
		src_ip,
		dst_ip,
		src_port,
		dst_port,
		transport_proto,
		network_proto,
		tcp_flags
	);
	if (res != 0) {
		LOG(ERROR, "failed to make packet, error=%d", res);
		return NULL;
	}
	struct virtual_service *vs = vs_lookup(balancer, &packet);
	if (vs == NULL) {
		LOG(ERROR, "failed to lookup vs");
		exit(TEST_FAILED);
	}
	struct packet_metadata meta;
	res = fill_packet_metadata(&packet, &meta);
	if (res != 0) {
		LOG(ERROR, "failed to fill packet metadata");
		return NULL;
	}
	struct tuple {
		uint8_t src_ip[NET6_LEN];
		uint8_t dst_ip[NET6_LEN];
		uint16_t src_port;
		uint16_t dst_port;
		uint8_t proto;
	} tuple;
	memset(tuple.src_ip, 0, NET6_LEN);
	memset(tuple.dst_ip, 0, NET6_LEN);
	if (network_proto == IPPROTO_IP) {
		memcpy(tuple.src_ip, src_ip, NET4_LEN);
		memcpy(tuple.dst_ip, dst_ip, NET4_LEN);
	} else {
		memcpy(tuple.src_ip, src_ip, NET6_LEN);
		memcpy(tuple.dst_ip, dst_ip, NET6_LEN);
	}
	tuple.src_port = src_port;
	tuple.dst_port = dst_port;
	tuple.proto = transport_proto;
	meta.hash = rte_hash_crc(&tuple, sizeof(struct tuple), 0);
	free_packet(&packet);
	/// @todo
	///	calculate hash during packet parsing
	return select_real(balancer, current_time, 0, vs, &meta);
}

////////////////////////////////////////////////////////////////////////////////

int
ops_distribution(
	struct balancer_module_config *balancer,
	uint8_t *src_ip,
	uint8_t *dst_ip,
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t transport_proto,
	size_t *rng,
	size_t probes,
	size_t result[2]
) {
	(void)src_port;
	memset(result, 0, 2 * 8);
	struct real *first = NULL;
	struct real *second = NULL;
	for (size_t i = 0; i < probes; ++i) {
		uint16_t tcp_flags = 0;
		if (transport_proto == IPPROTO_TCP) {
			if (rng_next(rng) % 2 == 0) {
				tcp_flags |= RTE_TCP_SYN_FLAG;
			}
			if (rng_next(rng) % 2 == 0) {
				tcp_flags |= RTE_TCP_RST_FLAG;
			}
		}
		struct real *rs = lookup_rs(
			balancer,
			src_ip,
			dst_ip,
			i & 0xFFFF,
			dst_port,
			transport_proto,
			IPPROTO_IP,
			tcp_flags
		);
		TEST_ASSERT_NOT_NULL(rs, "failed to find rs");
		if (first == NULL || first == rs) {
			first = rs;
			++result[0];
		} else {
			assert(second == rs || second == NULL);
			second = rs;
			++result[1];
		}
	}
	if (first->weight < second->weight) {
		size_t tmp = result[0];
		result[0] = result[1];
		result[1] = tmp;
	}
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

int
pure_l3_and_ops_and_weigth_matters(void *arena) {
	current_time = 1;

	struct mock *mock = mock_init(arena, ARENA_SIZE);
	TEST_ASSERT_NOT_NULL(mock, "failed to create mock");

	struct agent *agent = mock_create_agent(mock, AGENT_MEMORY);
	TEST_ASSERT_NOT_NULL(agent, "failed to create agent");

	struct balancer_session_table *session_table =
		balancer_session_table_create(agent, 10);
	TEST_ASSERT_NOT_NULL(session_table, "failed to create session table");

	struct balancer_sessions_timeouts *timeouts =
		balancer_sessions_timeouts_create(agent, 1, 1, 1, 1, 1, 1);
	TEST_ASSERT_NOT_NULL(timeouts, "failed to create sessions timeouts");

	uint8_t null_addr[16];
	memset(null_addr, 0, 16);

	uint8_t full_addr[16];
	memset(full_addr, 0xFF, 16);

	// Add the first virtual service on tcp 1.1.1.1. ... .1:80

	uint8_t vip1[16];
	memset(vip1, 1, 16);
	const uint16_t vs1_port = 80;
	const uint8_t vs1_proto = IPPROTO_TCP;
	struct balancer_vs_config *vs1_config = balancer_vs_config_create(
		agent, BALANCER_VS_IPV6_FLAG, vip1, vs1_port, vs1_proto, 2, 1
	);
	balancer_vs_config_set_allowed_src_range(
		vs1_config, 0, null_addr, full_addr
	);

	// Add first real on the first virtual service on 11.11. ... .11
	uint8_t real1_dst[16];
	memset(real1_dst, 0x11, 16);

	// Add first real on the first virtual service on 22.22.22.22
	uint8_t real2_dst[4];
	memset(real1_dst, 0x22, 4);

	balancer_vs_config_set_real(
		vs1_config,
		0,
		BALANCER_REAL_IPV6_FLAG,
		1,
		real1_dst,
		null_addr,
		full_addr
	);
	balancer_vs_config_set_real(
		vs1_config, 1, 0, 1, real2_dst, null_addr, full_addr
	);

	// Add the second virtual service on udp 2.2.2.2:0 (pure l3 balancing)

	uint8_t vip2[4];
	memset(vip2, 2, 4);
	const uint16_t vs2_port = 0;
	const uint8_t vs2_proto = IPPROTO_UDP;
	struct balancer_vs_config *vs2_config = balancer_vs_config_create(
		agent, BALANCER_VS_PURE_L3_FLAG, vip2, vs2_port, vs2_proto, 2, 1
	);

	balancer_vs_config_set_allowed_src_range(
		vs2_config, 0, null_addr, full_addr
	);

	// Add first real on 33.33. ... .33
	uint8_t real3_dst[16];
	memset(real3_dst, 0x33, 16);

	// Add second real on 44.44. ... .44
	uint8_t real4_dst[4];
	memset(real4_dst, 0x44, 4);

	balancer_vs_config_set_real(
		vs2_config,
		0,
		BALANCER_REAL_IPV6_FLAG,
		1,
		real3_dst,
		null_addr,
		full_addr
	);
	balancer_vs_config_set_real(
		vs2_config, 1, 0, 1, real4_dst, null_addr, full_addr
	);

	// Add the third virtual service on tcp 3.3.3.3:80 with OPS flag
	uint8_t vip3[4] = {3, 3, 3, 3};
	const uint16_t vs3_port = 80;
	const uint8_t vs3_proto = IPPROTO_UDP;
	uint8_t real5_dst[4] = {5, 5, 5, 5};
	uint8_t real6_dst[4] = {6, 6, 6, 6};
	struct balancer_vs_config *vs3_config = balancer_vs_config_create(
		agent, BALANCER_VS_OPS_FLAG, vip3, vs3_port, vs3_proto, 2, 1
	);
	TEST_ASSERT_NOT_NULL(
		vs3_config, "can not create third virtual service"
	);
	balancer_vs_config_set_allowed_src_range(
		vs3_config, 0, null_addr, full_addr
	);
	balancer_vs_config_set_real(
		vs3_config, 0, 0, 1, real5_dst, null_addr, full_addr
	);
	balancer_vs_config_set_real(
		vs3_config, 1, 0, 2, real6_dst, null_addr, full_addr
	);

	// Add the fourth virtual service on udp 3.3.3.3:0 (pure l3) with OPS
	// flag

	uint8_t vip4[4] = {3, 3, 3, 3};
	const uint16_t vs4_port = 443;
	const uint8_t vs4_proto = IPPROTO_TCP;
	uint8_t real7_dst[4] = {7, 7, 7, 7};
	uint8_t real8_dst[4] = {8, 8, 8, 8};
	struct balancer_vs_config *vs4_config = balancer_vs_config_create(
		agent,
		BALANCER_VS_OPS_FLAG | BALANCER_VS_PURE_L3_FLAG,
		vip4,
		vs4_port,
		vs4_proto,
		2,
		1
	);
	TEST_ASSERT_NOT_NULL(
		vs4_config, "can not create fourth virtual service"
	);
	balancer_vs_config_set_allowed_src_range(
		vs4_config, 0, null_addr, full_addr
	);
	balancer_vs_config_set_real(
		vs4_config, 0, 0, 1, real7_dst, null_addr, full_addr
	);
	balancer_vs_config_set_real(
		vs4_config, 1, 0, 2, real8_dst, null_addr, full_addr
	);

	struct balancer_vs_config *vs_configs[4] = {
		vs1_config, vs2_config, vs3_config, vs4_config
	};
	struct cp_module *cp_module = balancer_module_config_create(
		agent, "balancer", session_table, 4, vs_configs, timeouts
	);
	TEST_ASSERT_NOT_NULL(
		cp_module, "failed to create balancer module config"
	);
	struct balancer_module_config *balancer = container_of(
		cp_module, struct balancer_module_config, cp_module
	);

	// vs1 is ipv6 tcp
	// vs2 is ipv4 udp l3_only
	// vs3 ip ipv4 udp OPS
	// vs3 ip ipv4 tcp OPS+l3_only

	uint8_t u1_src[16];
	memset(u1_src, 10, 16);

	uint8_t u2_src[4];
	memset(u2_src, 11, 4);

	uint8_t u3_src[16];
	memset(u3_src, 12, 16);

	uint8_t u4_src[4];
	memset(u4_src, 13, 4);

	// u1, u3 is ipv6
	// u2, u4 is ipv4

	// trying to create session, but tcp packet is not syn
	struct real *rs = lookup_rs(
		balancer,
		u1_src,
		vip1,
		50000,
		vs1_port,
		IPPROTO_TCP,
		IPPROTO_IPV6,
		0
	);
	TEST_ASSERT_NULL(rs, "created session for not syn packet");

	// trying to create session, but tcp packet is SYN+RST
	rs = lookup_rs(
		balancer,
		u1_src,
		vip1,
		50000,
		vs1_port,
		IPPROTO_TCP,
		IPPROTO_IPV6,
		RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG
	);
	TEST_ASSERT_NULL(rs, "created session for syn+rst packet");

	// trying to create session with SYN packet
	rs = lookup_rs(
		balancer,
		u1_src,
		vip1,
		50000,
		vs1_port,
		IPPROTO_TCP,
		IPPROTO_IPV6,
		RTE_TCP_SYN_FLAG
	);
	TEST_ASSERT_NOT_NULL(rs, "did not created session for tcp syn packet");

	struct real *prev_rs = rs;
	for (size_t i = 0; i < 100; ++i) {
		rs = lookup_rs(
			balancer,
			u1_src,
			vip1,
			50000,
			vs1_port,
			IPPROTO_TCP,
			IPPROTO_IPV6,
			0
		);
		TEST_ASSERT_EQUAL(
			rs, prev_rs, "rescheduled packet for the fixed sesion"
		);
	}

	// check syn packet not rescheduled
	for (size_t i = 0; i < 100; ++i) {
		rs = lookup_rs(
			balancer,
			u1_src,
			vip1,
			50000,
			vs1_port,
			IPPROTO_TCP,
			IPPROTO_IPV6,
			RTE_TCP_SYN_FLAG
		);
		TEST_ASSERT_EQUAL(
			rs, prev_rs, "rescheduled packet for the fixed session"
		);
	}

	// check syn+rst packet not rescheduled
	for (size_t i = 0; i < 100; ++i) {
		rs = lookup_rs(
			balancer,
			u1_src,
			vip1,
			50000,
			vs1_port,
			IPPROTO_TCP,
			IPPROTO_IPV6,
			RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG
		);
		TEST_ASSERT_EQUAL(
			rs, prev_rs, "rescheduled packet for the fixed session"
		);
	}

	// send first udp packet to the second virtual service, session must be
	// created
	struct real *first_udp_rs = lookup_rs(
		balancer, u2_src, vip2, 1231, 1231, IPPROTO_UDP, IPPROTO_IP, 0
	);
	TEST_ASSERT_NOT_NULL(
		first_udp_rs, "can not create session for udp packet"
	);
	for (size_t i = 0; i < 100; ++i) {
		rs = lookup_rs(
			balancer,
			u2_src,
			vip2,
			i,
			i + 1,
			IPPROTO_UDP,
			IPPROTO_IP,
			0
		);
		TEST_ASSERT_EQUAL(rs, first_udp_rs, "rescheduled udp packet");
	}

	// Update current time
	current_time = 10000;

	// Check sessions removed

	for (size_t i = 0; i < 100; ++i) {
		rs = lookup_rs(
			balancer,
			u1_src,
			vip1,
			50000,
			vs1_port,
			IPPROTO_TCP,
			IPPROTO_IPV6,
			0
		);
		TEST_ASSERT_NULL(rs, "created session for not syn packet");

		rs = lookup_rs(
			balancer,
			u1_src,
			vip1,
			50000,
			vs1_port,
			IPPROTO_TCP,
			IPPROTO_IPV6,
			RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG
		);
		TEST_ASSERT_NULL(rs, "created session for syn+rst packet");
	}

	LOG(INFO, "Make probes for the third service [OPS]");
	{
		uint64_t rng = 123123;
		size_t result[2];
		int res = ops_distribution(
			balancer,
			u2_src,
			vip3,
			123,
			vs3_port,
			vs3_proto,
			&rng,
			2000,
			result
		);
		TEST_ASSERT_EQUAL(
			res,
			TEST_SUCCESS,
			"failed to make ops for the third service"
		);
		double frac = (double)result[0] / result[1];
		LOG(INFO,
		    "Third service session/real distribution: [%lu, %lu] "
		    "(d[0]/d[1]=%.3lf)",
		    result[0],
		    result[1],
		    frac);
		TEST_ASSERT(fabs(frac - 2.0) / 2.0 <= 0.25, "bad distribution");
	}

	LOG(INFO, "Make probes for the fourth service [OPS + PURE_L3]");
	{
		uint64_t rng = 12123;
		size_t result[2];
		int res = ops_distribution(
			balancer,
			u2_src,
			vip4,
			123,
			1231,
			vs4_proto,
			&rng,
			2000,
			result
		);
		TEST_ASSERT_EQUAL(
			res,
			TEST_SUCCESS,
			"failed to make ops for the fourth service"
		);
		double frac = (double)result[0] / result[1];
		LOG(INFO,
		    "Fourth service session/real distribution: [%lu, %lu] "
		    "(d[0]/d[1]=%.3lf)",
		    result[0],
		    result[1],
		    frac);
		TEST_ASSERT(fabs(frac - 2.0) / 2.0 <= 0.25, "bad distribution");
	}

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");

	void *arena = malloc(ARENA_SIZE);
	if (arena == NULL) {
		LOG(ERROR, "failed to allocate arena");
		return 1;
	}

	LOG(INFO, "Running test `pure_l3_and_ops_and_weigth_matters`...");
	int res = pure_l3_and_ops_and_weigth_matters(arena);
	TEST_ASSERT_EQUAL(
		res,
		TEST_SUCCESS,
		"Test `pure_l3_and_ops_and_weigth_matters` failed"
	);

	free(arena);

	LOG(INFO, "All tests have been passed");

	return 0;
}