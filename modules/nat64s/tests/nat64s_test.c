/*
 * End-to-end test of the stateful NAT64 module: a nat64s stage fed by a
 * plain device pipeline, backed by real nat64state map objects.
 *
 * The outbound round creates the session (the packet leaves as IPv4 with
 * the pool's source address), the return round finds it through the wan
 * table (leaves as IPv6 towards the client), an unknown tuple is dropped,
 * and the session expires with the configured TTL.
 */

#include "api/agent.h"
#include "common/memory_address.h"
#include "common/test_assert.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/dataplane_ut/mempool.h"
#include "lib/logging/log.h"

#include <errno.h>
#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "common/strutils.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "modules/forward/api/controlplane.h"
#include "modules/nat64s/api/controlplane.h"
#include "modules/nat64s/dataplane/config.h"
#include "objects/fwstate/api/fwstate_map_nat64_lan_object.h"
#include "objects/fwstate/api/fwstate_map_nat64_wan_object.h"

// 64:ff9b::/96, the well-known NAT64 prefix.
static const uint8_t nat64_prefix[12] = {
	0x00, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0
};
// Pool: 198.51.100.1 (TEST-NET-2).
static const uint32_t pool_addrs[1] = {RTE_BE32(RTE_IPV4(198, 51, 100, 1))};
// Client 2001:db8::1 and remote 192.0.2.34 (TEST-NET-1).
static const uint8_t client_addr[16] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01
};
static const uint32_t remote_addr = RTE_BE32(RTE_IPV4(192, 0, 2, 34));
#define CLIENT_PORT 50000
#define REMOTE_PORT 443

// A whole harness round wired to the nat64s module: one agent with the
// lan and wan map objects, a nat64s config linked to them, and a plain
// device pipeline nat64s -> forward out of the device.
struct nat64s_env {
	struct dataplane_ut *ut;
	struct agent *agent;
	struct fwstate_map_nat64_lan_object *lan;
	struct fwstate_map_nat64_wan_object *wan;
	struct nat64s_module_config *config;
};

static int
nat64s_env_init(struct nat64s_env *env) {
	memset(env, 0, sizeof(*env));

	const char *port_names[] = {"dev0"};
	const char *module_names[] = {"nat64s", "forward"};
	const char *devs_to_load[] = {"plain"};
	const char *objs_to_load[] = {
		FWSTATE_MAP_NAT64_LAN_OBJECT_TYPE,
		FWSTATE_MAP_NAT64_WAN_OBJECT_TYPE,
	};
	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = module_names,
		.module_count = 2,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
		.objects_to_load = objs_to_load,
		.objects_to_load_count = 2,
	};
	env->ut = dataplane_ut_new(&cfg);
	TEST_ASSERT_NOT_NULL(env->ut, "dataplane_ut_new returned NULL");

	struct yanet_shm *shm = dataplane_ut_shm(env->ut);
	yanet_error *err = NULL;
	env->agent = agent_attach(shm, 0, "nat64s-test", 16u << 20, &err);
	TEST_ASSERT_NOT_NULL(env->agent, "agent_attach failed");

	// Session tables: small but real.
	env->lan = (struct fwstate_map_nat64_lan_object *)
		fwstate_map_nat64_lan_object_config_new(
			env->agent, "lan", &err
		);
	TEST_ASSERT_NOT_NULL(env->lan, "lan map create failed");
	struct fwstate_map_create_config create = {
		.index_size = 1024,
		.extra_bucket_count = 64,
		.worker_count = 1,
	};
	TEST_ASSERT_SUCCESS(
		fwstate_map_nat64_lan_object_create(env->lan, &create),
		"lan map init failed"
	);

	env->wan = (struct fwstate_map_nat64_wan_object *)
		fwstate_map_nat64_wan_object_config_new(
			env->agent, "wan", &err
		);
	TEST_ASSERT_NOT_NULL(env->wan, "wan map create failed");
	TEST_ASSERT_SUCCESS(
		fwstate_map_nat64_wan_object_create(env->wan, &create),
		"wan map init failed"
	);

	struct cp_object *objects[] = {
		&env->lan->cp_object, &env->wan->cp_object
	};
	TEST_ASSERT_SUCCESS(
		agent_update_objects(env->agent, 2, objects, &err),
		"agent_update_objects failed"
	);

	// The module: prefix, pool, links and a 30s syn timeout.
	struct cp_module *module =
		nat64s_module_config_create(env->agent, "t0", &err);
	TEST_ASSERT_NOT_NULL(module, "nat64s_module_config_create failed");
	env->config = (struct nat64s_module_config *)container_of(
		module, struct nat64s_module_config, cp_module
	);

	struct nat64s_module_update update = {
		.prefix = nat64_prefix,
		.pool = pool_addrs,
		.pool_count = 1,
		.timeouts =
			{.tcp_syn = 30,
			 .tcp_ack = 300,
			 .tcp_fin = 30,
			 .udp = 60,
			 .icmp = 30,
			 .other = 60},
		.lan_map_name = "lan",
		.wan_map_name = "wan",
	};
	TEST_ASSERT_SUCCESS(
		nat64s_module_config_update(module, &update, &err),
		"nat64s_module_config_update failed"
	);

	struct cp_module *forward =
		forward_module_config_init(env->agent, "loop", &err);
	TEST_ASSERT_NOT_NULL(forward, "forward_module_config_init failed");
	struct forward_rule forward_rule;
	memset(&forward_rule, 0, sizeof(forward_rule));
	strtcpy(forward_rule.target, "dev0", sizeof(forward_rule.target));
	strtcpy(forward_rule.counter, "loop", sizeof(forward_rule.counter));
	forward_rule.mode = FORWARD_MODE_OUT;
	TEST_ASSERT_SUCCESS(
		forward_module_config_update(forward, &forward_rule, 1, &err),
		"forward rule setup failed"
	);

	struct cp_module *modules[] = {module, forward};
	TEST_ASSERT_SUCCESS(
		agent_update_modules(env->agent, 2, modules, &err),
		"module update failed"
	);

	const char *chain_types[] = {"nat64s", "forward"};
	const char *chain_names[] = {"t0", "loop"};
	struct cp_chain_config *chain =
		cp_chain_config_create("chain", 2, chain_types, chain_names);
	TEST_ASSERT_NOT_NULL(chain, "cp_chain_config_create failed");
	struct cp_function_config *function =
		cp_function_config_create("function", 1);
	TEST_ASSERT_NOT_NULL(function, "cp_function_config_create failed");
	TEST_ASSERT_SUCCESS(
		cp_function_config_set_chain(function, 0, chain, 1),
		"cp_function_config_set_chain failed"
	);
	struct cp_function_config *functions[] = {function};
	TEST_ASSERT_SUCCESS(
		agent_update_functions(env->agent, 1, functions, &err),
		"function update failed"
	);
	cp_function_config_free(function);

	struct cp_pipeline_config *input_pipeline =
		cp_pipeline_config_create("input", 1);
	TEST_ASSERT_NOT_NULL(input_pipeline, "input pipeline alloc failed");
	TEST_ASSERT_SUCCESS(
		cp_pipeline_config_set_function(input_pipeline, 0, "function"),
		"input pipeline setup failed"
	);
	struct cp_pipeline_config *output_pipeline =
		cp_pipeline_config_create("output", 0);
	TEST_ASSERT_NOT_NULL(output_pipeline, "output pipeline alloc failed");
	struct cp_pipeline_config *pipelines[] = {
		input_pipeline, output_pipeline
	};
	TEST_ASSERT_SUCCESS(
		agent_update_pipelines(env->agent, 2, pipelines, &err),
		"pipeline update failed"
	);
	cp_pipeline_config_free(input_pipeline);
	cp_pipeline_config_free(output_pipeline);

	struct cp_device_plain_config *device_config =
		cp_device_plain_config_new("dev0", 1, 1, &err);
	TEST_ASSERT_NOT_NULL(
		device_config, "cp_device_plain_config_new failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_plain_config_set_input_pipeline(
			device_config, 0, "input", 1
		),
		"input device pipeline setup failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_plain_config_set_output_pipeline(
			device_config, 0, "output", 1
		),
		"output device pipeline setup failed"
	);
	struct cp_device *device =
		cp_device_plain_new(env->agent, device_config, &err);
	cp_device_plain_config_free(device_config);
	TEST_ASSERT_NOT_NULL(device, "cp_device_plain_new failed");
	struct cp_device *devices[] = {device};
	TEST_ASSERT_SUCCESS(
		agent_update_devices(env->agent, 1, devices, &err),
		"device update failed"
	);
	yanet_error *device_err = NULL;
	cp_device_plain_free(device, &device_err);
	yanet_error_free(device_err);

	return TEST_SUCCESS;
}

static void
nat64s_env_fini(struct nat64s_env *env) {
	agent_detach(env->agent);
	dataplane_ut_free(env->ut);
}

// Build a parsed IPv6 TCP SYN from the client to the remote under the
// NAT64 prefix.
static int
nat64s_make_v6_syn(struct nat64s_env *env, struct packet **out) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(env->ut);
	TEST_ASSERT_NOT_NULL(mbuf, "dataplane_ut_alloc_mbuf returned NULL");

	uint8_t *data = (uint8_t *)rte_pktmbuf_append(
		mbuf,
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv6_hdr) +
			sizeof(struct rte_tcp_hdr)
	);
	TEST_ASSERT_NOT_NULL(data, "packet payload allocation failed");
	memset(data, 0, rte_pktmbuf_data_len(mbuf));

	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)data;
	ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	struct rte_ipv6_hdr *ipv6 = (struct rte_ipv6_hdr *)(ether + 1);
	memcpy(ipv6->src_addr, client_addr, 16);
	memcpy(ipv6->dst_addr, nat64_prefix, 12);
	memcpy(&ipv6->dst_addr[12], &remote_addr, sizeof(remote_addr));
	ipv6->proto = IPPROTO_TCP;
	ipv6->payload_len = rte_cpu_to_be_16(sizeof(struct rte_tcp_hdr));

	struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)(ipv6 + 1);
	tcp->src_port = rte_cpu_to_be_16(CLIENT_PORT);
	tcp->dst_port = rte_cpu_to_be_16(REMOTE_PORT);
	tcp->tcp_flags = RTE_TCP_SYN_FLAG;
	tcp->data_off = (sizeof(struct rte_tcp_hdr) / 4) << 4;

	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");
	*out = packet;
	return TEST_SUCCESS;
}

// Build a parsed IPv4 TCP packet from the remote towards the pool.
static int
nat64s_make_v4_reply(struct nat64s_env *env, struct packet **out) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(env->ut);
	TEST_ASSERT_NOT_NULL(mbuf, "dataplane_ut_alloc_mbuf returned NULL");

	uint8_t *data = (uint8_t *)rte_pktmbuf_append(
		mbuf,
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr) +
			sizeof(struct rte_tcp_hdr)
	);
	TEST_ASSERT_NOT_NULL(data, "packet payload allocation failed");
	memset(data, 0, rte_pktmbuf_data_len(mbuf));

	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)data;
	ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ipv4 = (struct rte_ipv4_hdr *)(ether + 1);
	ipv4->version_ihl = RTE_IPV4_VHL_DEF;
	ipv4->total_length = rte_cpu_to_be_16(
		sizeof(struct rte_ipv4_hdr) + sizeof(struct rte_tcp_hdr)
	);
	ipv4->next_proto_id = IPPROTO_TCP;
	ipv4->src_addr = remote_addr;
	ipv4->dst_addr = pool_addrs[0];

	struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)(ipv4 + 1);
	tcp->src_port = rte_cpu_to_be_16(REMOTE_PORT);
	tcp->dst_port = rte_cpu_to_be_16(CLIENT_PORT);
	tcp->tcp_flags = RTE_TCP_SYN_FLAG | RTE_TCP_ACK_FLAG;
	tcp->data_off = (sizeof(struct rte_tcp_hdr) / 4) << 4;

	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");
	*out = packet;
	return TEST_SUCCESS;
}

// Run one packet through the pipeline. On return *out holds the emitted
// packet and *dropped tells whether it was dropped instead; a dropped
// packet is freed here.
static int
nat64s_run(
	struct nat64s_env *env,
	struct packet *packet,
	struct packet **out,
	bool *dropped
) {
	struct packet_list input;
	packet_list_init(&input);
	packet_list_add(&input, packet);
	uint16_t input_devices[1] = {0};
	struct dataplane_ut_round_result result;
	dataplane_ut_run(env->ut, 0, input_devices, &input, &result);

	*out = packet_list_pop(&result.output);
	struct packet *drop = packet_list_pop(&result.drop);
	TEST_ASSERT_NULL(
		packet_list_pop(&result.output),
		"the round must emit at most one packet"
	);
	TEST_ASSERT(
		*out == NULL || drop == NULL,
		"a packet must not be both output and dropped"
	);
	*dropped = drop != NULL;
	if (drop != NULL) {
		rte_pktmbuf_free(packet_to_mbuf(drop));
	}
	return TEST_SUCCESS;
}

// Outbound translation: the SYN leaves as IPv4 with the pool address as
// its source and the remote as its destination, ports untouched.
static int
test_outbound_creates_session(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 1000000000000ull);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the outbound SYN must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the outbound SYN must be translated");

	TEST_ASSERT(
		out->network_header.type ==
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
		"the translated packet must be IPv4"
	);
	struct rte_mbuf *mbuf = packet_to_mbuf(out);
	struct rte_ipv4_hdr *ipv4 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, out->network_header.offset
	);
	TEST_ASSERT_EQUAL(
		ipv4->src_addr,
		pool_addrs[0],
		"the source must be the pool address"
	);
	TEST_ASSERT_EQUAL(
		ipv4->dst_addr,
		remote_addr,
		"the destination must be the remote"
	);
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_tcp_hdr *, out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(tcp->src_port),
		CLIENT_PORT,
		"the source port must be kept"
	);
	rte_pktmbuf_free(mbuf);

	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_TRANSLATED_V6_TO_V4],
		1,
		"the translation counter must count the SYN"
	);
	return TEST_SUCCESS;
}

// Return traffic: the reply finds the session through the wan table and
// leaves as IPv6 towards the client.
static int
test_return_uses_session(struct nat64s_env *env) {
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v4_reply(env, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the return packet must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the return packet must be translated");

	TEST_ASSERT(
		out->network_header.type ==
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
		"the translated packet must be IPv6"
	);
	struct rte_mbuf *mbuf = packet_to_mbuf(out);
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, out->network_header.offset
	);
	TEST_ASSERT(
		memcmp(ipv6->dst_addr, client_addr, 16) == 0,
		"the translated destination must be the client"
	);
	TEST_ASSERT(
		memcmp(ipv6->src_addr, nat64_prefix, 12) == 0,
		"the translated source must carry the NAT64 prefix"
	);
	rte_pktmbuf_free(mbuf);

	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_TRANSLATED_V4_TO_V6],
		1,
		"the translation counter must count the reply"
	);
	return TEST_SUCCESS;
}

// The next reply finds the session again: it was refreshed, not consumed.
static int
test_session_refreshes(struct nat64s_env *env) {
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v4_reply(env, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "a refreshed session must keep translating");
	rte_pktmbuf_free(packet_to_mbuf(out));
	return TEST_SUCCESS;
}

// A tuple nobody registered is dropped.
static int
test_unknown_tuple_dropped(struct nat64s_env *env) {
	struct packet *reply = NULL;
	TEST_ASSERT_SUCCESS(nat64s_make_v4_reply(env, &reply), "build failed");
	// Move the reply to a port no session holds.
	struct rte_mbuf *mbuf = packet_to_mbuf(reply);
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_tcp_hdr *, reply->transport_header.offset
	);
	tcp->dst_port = rte_cpu_to_be_16(CLIENT_PORT + 1);

	bool dropped = false;
	struct packet *out = NULL;
	TEST_ASSERT_SUCCESS(
		nat64s_run(env, reply, &out, &dropped), "run failed"
	);
	TEST_ASSERT_NULL(out, "an unknown tuple must not be translated");
	TEST_ASSERT(dropped, "an unknown tuple must be dropped");
	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_WAN_STATE_MISS],
		1,
		"the miss counter must count the unknown tuple"
	);
	return TEST_SUCCESS;
}

// An active flow survives past the SYN timeout: every reply refreshed the
// wan entry with the established (ACK) timeout.
static int
test_session_survives_syn_timeout(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(
		env->ut, 1000000000000ull + 60ull * 1000000000ull
	);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v4_reply(env, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "a refreshed session must outlive its syn ttl");
	TEST_ASSERT_NOT_NULL(out, "a refreshed session must keep translating");
	rte_pktmbuf_free(packet_to_mbuf(out));
	return TEST_SUCCESS;
}

// Once the refreshed TTL has passed with no traffic, the wan table no
// longer answers and the reply drops.
static int
test_session_expires(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(
		env->ut,
		1000000000000ull + 60ull * 1000000000ull +
			301ull * 1000000000ull
	);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v4_reply(env, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(out, "an expired session must not translate");
	TEST_ASSERT(dropped, "an expired session must drop its reply");
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	struct nat64s_env env;
	if (nat64s_env_init(&env) != TEST_SUCCESS) {
		return 1;
	}

	int res = test_outbound_creates_session(&env);
	if (res == TEST_SUCCESS) {
		res = test_return_uses_session(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_session_refreshes(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_unknown_tuple_dropped(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_session_survives_syn_timeout(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_session_expires(&env);
	}

	nat64s_env_fini(&env);
	return (res == TEST_SUCCESS) ? 0 : 1;
}
