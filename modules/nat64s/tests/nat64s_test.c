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
#include "lib/dataplane/packet/icmp.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/dataplane_ut/mempool.h"
#include "lib/logging/log.h"

#include <errno.h>
#include <string.h>

#include <netinet/ip_icmp.h>
#include <rte_ether.h>
#include <rte_icmp.h>
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
#include "objects/fwstate/api/fwstate_map_nat64_object.h"

// The NAT64 prefixes the config carries: the well-known 64:ff9b::/96
// and a documentation prefix — a session uses the one its destination
// embeds under.
static const uint8_t nat64_prefix[12] = {
	0x00, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0
};
static const uint8_t prefixes[2][12] = {
	{0x00, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0},
	{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0x64},
};
// Pool: 198.51.100.1 (TEST-NET-2).
static const uint32_t pool_addrs[1] = {RTE_BE32(RTE_IPV4(198, 51, 100, 1))};
// Client 2001:db8::1 and remote 192.0.2.34 (TEST-NET-1).
static const uint8_t client_addr[16] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01
};
static const uint32_t remote_addr = RTE_BE32(RTE_IPV4(192, 0, 2, 34));
// The same remote as bytes, for the parameterized SYN builder.
static const uint8_t remote4[4] = {192, 0, 2, 34};
#define CLIENT_PORT 50000
#define REMOTE_PORT 443

// A whole harness round wired to the nat64s module: one agent with the
// lan and wan map objects, a nat64s config linked to them, and a plain
// device pipeline nat64s -> forward out of the device.
struct nat64s_env {
	// The port the outbound session was translated to; written by the
	// outbound test, read by the return tests.
	uint16_t allocated_port;
	struct dataplane_ut *ut;
	struct agent *agent;
	struct fwstate_map_nat64_object *map;
	struct nat64s_module_config *config;
};

static int
nat64s_env_init(struct nat64s_env *env) {
	memset(env, 0, sizeof(*env));

	const char *port_names[] = {"dev0"};
	const char *module_names[] = {"nat64s", "forward"};
	const char *devs_to_load[] = {"plain"};
	const char *objs_to_load[] = {FWSTATE_MAP_NAT64_OBJECT_TYPE};
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
		.objects_to_load_count = 1,
	};
	env->ut = dataplane_ut_new(&cfg);
	TEST_ASSERT_NOT_NULL(env->ut, "dataplane_ut_new returned NULL");

	struct yanet_shm *shm = dataplane_ut_shm(env->ut);
	yanet_error *err = NULL;
	env->agent = agent_attach(shm, 0, "nat64s-test", 8u << 20, &err);
	TEST_ASSERT_NOT_NULL(
		env->agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	// Session tables: small but real, both sides of one object.
	env->map = (struct fwstate_map_nat64_object *)
		fwstate_map_nat64_object_config_new(
			env->agent, "sessions", &err
		);
	TEST_ASSERT_NOT_NULL(env->map, "nat64state map create failed");
	struct fwstate_map_create_config create = {
		.index_size = 1024,
		.extra_bucket_count = 64,
		.worker_count = 1,
	};
	TEST_ASSERT_SUCCESS(
		fwstate_map_nat64_object_create(env->map, &create),
		"nat64state map init failed"
	);

	struct cp_object *objects[] = {&env->map->cp_object};
	TEST_ASSERT_SUCCESS(
		agent_update_objects(env->agent, 1, objects, &err),
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
		.prefixes = prefixes[0],
		.prefix_count = 2,
		.pool = pool_addrs,
		.pool_count = 1,
		// The client's port (50000) falls outside: its session must be
		// rewritten into this window.
		.port_lo = 1024,
		.port_hi = 4096,
		.timeouts = {.timeout = 300, .tcp_established = 300},
		.map_name = "sessions",
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
nat64s_make_v6_syn_to(
	struct nat64s_env *env,
	uint16_t client_port,
	const uint8_t remote[4],
	struct packet **out
) {
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
	memcpy(&ipv6->dst_addr[12], remote, 4);
	ipv6->proto = IPPROTO_TCP;
	ipv6->payload_len = rte_cpu_to_be_16(sizeof(struct rte_tcp_hdr));

	struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)(ipv6 + 1);
	tcp->src_port = rte_cpu_to_be_16(client_port);
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

static int
nat64s_make_v6_syn(
	struct nat64s_env *env, uint16_t client_port, struct packet **out
) {
	return nat64s_make_v6_syn_to(env, client_port, remote4, out);
}

// A SYN to the test remote embedded under the config's second prefix.
static int
nat64s_make_v6_syn_to2(
	struct nat64s_env *env, uint16_t client_port, struct packet **out
) {
	struct packet *packet = NULL;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_to(env, client_port, remote4, &packet),
		"build failed"
	);
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	memcpy(ipv6->dst_addr, prefixes[1], 12);
	*out = packet;
	return TEST_SUCCESS;
}

// Build a parsed IPv4 TCP packet from the remote towards the pool.
static int
nat64s_make_v4_reply(
	struct nat64s_env *env, uint16_t client_port, struct packet **out
) {
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
	tcp->dst_port = rte_cpu_to_be_16(client_port);
	tcp->tcp_flags = RTE_TCP_SYN_FLAG | RTE_TCP_ACK_FLAG;
	tcp->data_off = (sizeof(struct rte_tcp_hdr) / 4) << 4;

	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");
	*out = packet;
	return TEST_SUCCESS;
}

// Build a parsed ICMPv6 echo request from the client.
static int
nat64s_make_v6_echo(
	struct nat64s_env *env, uint16_t ident, struct packet **out
) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(env->ut);
	TEST_ASSERT_NOT_NULL(mbuf, "dataplane_ut_alloc_mbuf returned NULL");
	uint8_t *data = (uint8_t *)rte_pktmbuf_append(
		mbuf,
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv6_hdr) + 8
	);
	TEST_ASSERT_NOT_NULL(data, "packet payload allocation failed");
	memset(data, 0, rte_pktmbuf_data_len(mbuf));
	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)data;
	ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
	struct rte_ipv6_hdr *ipv6 = (struct rte_ipv6_hdr *)(ether + 1);
	memcpy(ipv6->src_addr, client_addr, 16);
	memcpy(ipv6->dst_addr, nat64_prefix, 12);
	memcpy(&ipv6->dst_addr[12], remote4, 4);
	ipv6->proto = IPPROTO_ICMPV6;
	ipv6->payload_len = rte_cpu_to_be_16(8);
	uint8_t *icmp = (uint8_t *)(ipv6 + 1);
	icmp[0] = 128; // echo request
	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");
	// Identifier past parse so the checksum need not be valid.
	rte_be16_t *idp = (rte_be16_t *)(icmp + 4);
	*idp = rte_cpu_to_be_16(ident);
	*out = packet;
	return TEST_SUCCESS;
}

// Build a parsed ICMPv4 echo reply from the remote towards the pool.
static int
nat64s_make_v4_echo_reply(
	struct nat64s_env *env, uint16_t ident, struct packet **out
) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(env->ut);
	TEST_ASSERT_NOT_NULL(mbuf, "dataplane_ut_alloc_mbuf returned NULL");
	uint8_t *data = (uint8_t *)rte_pktmbuf_append(
		mbuf,
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr) + 8
	);
	TEST_ASSERT_NOT_NULL(data, "packet payload allocation failed");
	memset(data, 0, rte_pktmbuf_data_len(mbuf));
	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)data;
	ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	struct rte_ipv4_hdr *ipv4 = (struct rte_ipv4_hdr *)(ether + 1);
	ipv4->version_ihl = RTE_IPV4_VHL_DEF;
	ipv4->total_length = rte_cpu_to_be_16(sizeof(struct rte_ipv4_hdr) + 8);
	ipv4->next_proto_id = IPPROTO_ICMP;
	ipv4->src_addr = remote_addr;
	ipv4->dst_addr = pool_addrs[0];
	uint8_t *icmp = (uint8_t *)(ipv4 + 1);
	icmp[0] = ICMP_ECHOREPLY;
	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");
	rte_be16_t *idp = (rte_be16_t *)(icmp + 4);
	*idp = rte_cpu_to_be_16(ident);
	*out = packet;
	return TEST_SUCCESS;
}

// Build a parsed ICMPv4 Fragmentation Needed error from the remote,
// quoting the translated outbound packet of the session.
static int
nat64s_make_v4_frag_needed(
	struct nat64s_env *env, uint16_t allocated_port, struct packet **out
) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(env->ut);
	TEST_ASSERT_NOT_NULL(mbuf, "dataplane_ut_alloc_mbuf returned NULL");

	size_t icmp_len =
		8 + sizeof(struct rte_ipv4_hdr) + sizeof(struct rte_tcp_hdr);
	uint8_t *data = (uint8_t *)rte_pktmbuf_append(
		mbuf,
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr) +
			icmp_len +
			// Tailroom for the embedded header growth of the
			// translation.
			sizeof(struct rte_ipv6_hdr)
	);
	TEST_ASSERT_NOT_NULL(data, "packet payload allocation failed");
	memset(data, 0, rte_pktmbuf_data_len(mbuf));

	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)data;
	ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ipv4 = (struct rte_ipv4_hdr *)(ether + 1);
	ipv4->version_ihl = RTE_IPV4_VHL_DEF;
	ipv4->total_length =
		rte_cpu_to_be_16(sizeof(struct rte_ipv4_hdr) + icmp_len);
	ipv4->next_proto_id = IPPROTO_ICMP;
	ipv4->src_addr = remote_addr;
	ipv4->dst_addr = pool_addrs[0];

	uint8_t *icmp = (uint8_t *)(ipv4 + 1);
	icmp[0] = ICMP_DEST_UNREACH; // type
	icmp[1] = ICMP_FRAG_NEEDED;  // code

	struct rte_ipv4_hdr *emb = (struct rte_ipv4_hdr *)(icmp + 8);
	emb->version_ihl = RTE_IPV4_VHL_DEF;
	emb->total_length = rte_cpu_to_be_16(
		sizeof(struct rte_ipv4_hdr) + sizeof(struct rte_tcp_hdr)
	);
	emb->next_proto_id = IPPROTO_TCP;
	emb->src_addr = pool_addrs[0];
	emb->dst_addr = remote_addr;

	struct rte_tcp_hdr *emb_tcp = (struct rte_tcp_hdr *)(emb + 1);
	emb_tcp->src_port = rte_cpu_to_be_16(allocated_port);
	emb_tcp->dst_port = rte_cpu_to_be_16(REMOTE_PORT);
	emb_tcp->data_off = (sizeof(struct rte_tcp_hdr) / 4) << 4;

	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");
	*out = packet;
	return TEST_SUCCESS;
}

// Build a parsed ICMPv6 Packet Too Big error from the client, quoting the
// inbound packet of the session (remote -> client): the reversed
// orientation of the stored session.
static int
nat64s_make_v6_packet_too_big(
	struct nat64s_env *env, uint16_t client_port, struct packet **out
) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(env->ut);
	TEST_ASSERT_NOT_NULL(mbuf, "dataplane_ut_alloc_mbuf returned NULL");

	size_t icmp_len =
		8 + sizeof(struct rte_ipv6_hdr) + sizeof(struct rte_tcp_hdr);
	uint8_t *data = (uint8_t *)rte_pktmbuf_append(
		mbuf,
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv6_hdr) +
			icmp_len
	);
	TEST_ASSERT_NOT_NULL(data, "packet payload allocation failed");
	memset(data, 0, rte_pktmbuf_data_len(mbuf));

	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)data;
	ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	struct rte_ipv6_hdr *ipv6 = (struct rte_ipv6_hdr *)(ether + 1);
	memcpy(ipv6->src_addr, client_addr, 16);
	memcpy(ipv6->dst_addr, nat64_prefix, 12);
	memcpy(&ipv6->dst_addr[12], &remote_addr, sizeof(remote_addr));
	ipv6->proto = IPPROTO_ICMPV6;
	ipv6->payload_len = rte_cpu_to_be_16(icmp_len);

	uint8_t *icmp = (uint8_t *)(ipv6 + 1);
	icmp[0] = 2; // ICMP6_PACKET_TOO_BIG
	icmp[1] = 0;

	struct rte_ipv6_hdr *emb = (struct rte_ipv6_hdr *)(icmp + 8);
	memcpy(emb->src_addr, nat64_prefix, 12);
	memcpy(&emb->src_addr[12], &remote_addr, sizeof(remote_addr));
	memcpy(emb->dst_addr, client_addr, 16);
	emb->proto = IPPROTO_TCP;

	struct rte_tcp_hdr *emb_tcp = (struct rte_tcp_hdr *)(emb + 1);
	emb_tcp->src_port = rte_cpu_to_be_16(REMOTE_PORT);
	emb_tcp->dst_port = rte_cpu_to_be_16(client_port);
	emb_tcp->data_off = (sizeof(struct rte_tcp_hdr) / 4) << 4;

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
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn(env, CLIENT_PORT, &in), "build failed"
	);
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
	uint16_t translated_port = rte_be_to_cpu_16(tcp->src_port);
	env->allocated_port = translated_port;
	TEST_ASSERT(
		translated_port >= 1024 && translated_port < 4096,
		"an out-of-window source port must be translated into the "
		"window"
	);
	TEST_ASSERT(
		translated_port != CLIENT_PORT,
		"the client's out-of-window port must not be kept"
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
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, env->allocated_port, &in),
		"build failed"
	);
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
	struct rte_tcp_hdr *tcp6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_tcp_hdr *, out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(tcp6->dst_port),
		CLIENT_PORT,
		"the client's original port must be restored"
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
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, env->allocated_port, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "a refreshed session must keep translating");
	rte_pktmbuf_free(packet_to_mbuf(out));
	return TEST_SUCCESS;
}

// A tuple nobody registered is dropped.
static int
test_unknown_tuple_dropped(struct nat64s_env *env) {
	struct packet *reply = NULL;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, env->allocated_port, &reply),
		"build failed"
	);
	// Move the reply to a port no session holds.
	struct rte_mbuf *mbuf = packet_to_mbuf(reply);
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_tcp_hdr *, reply->transport_header.offset
	);
	tcp->dst_port = rte_cpu_to_be_16(env->allocated_port + 1);

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

// A flow whose source port already lies in the window keeps it, and its
// return traffic restores it unchanged.
static int
test_in_window_port_kept(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 2000000000000ull);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2000, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "an in-window flow must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "an in-window flow must be translated");
	struct rte_mbuf *mbuf = packet_to_mbuf(out);
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_tcp_hdr *, out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(tcp->src_port),
		2000,
		"an in-window source port must be kept"
	);
	rte_pktmbuf_free(mbuf);

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, 2000, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the in-window reply must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));
	return TEST_SUCCESS;
}

// An ICMPv6 echo session is answered by its translated ICMPv4 reply:
// both ICMP families share one session protocol.
static int
test_icmp_echo_roundtrip(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 6000000000000ull);

	// Outbound echo request with an in-window id: the id passes through
	// untouched and the reply finds the session across the ICMP family
	// boundary.
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_echo(env, 2000, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the echo request must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the echo request must translate");
	struct rte_mbuf *m = packet_to_mbuf(out);
	struct rte_icmp_hdr *icmp4 = rte_pktmbuf_mtod_offset(
		m, struct rte_icmp_hdr *, out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		icmp4->icmp_type, ICMP_ECHO, "the request must become ICMP echo"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(icmp4->icmp_ident), 2000, "the id must survive"
	);
	rte_pktmbuf_free(m);

	// The reply arrives as ICMPv4 with the translated id: the session
	// was created with the ICMPv6 family number, so finding it proves
	// the shared session protocol.
	struct packet *reply = NULL;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_echo_reply(env, 2000, &reply), "build failed"
	);
	TEST_ASSERT_SUCCESS(
		nat64s_run(env, reply, &out, &dropped), "run failed"
	);
	TEST_ASSERT(!dropped, "the echo reply must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the echo reply must translate back");
	m = packet_to_mbuf(out);
	struct yanet_icmp6_hdr *icmp6 = rte_pktmbuf_mtod_offset(
		m, struct yanet_icmp6_hdr *, out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		icmp6->icmp6_type,
		129,
		"the reply must become ICMPv6 echo reply"
	);
	rte_pktmbuf_free(m);
	return TEST_SUCCESS;
}

// Path-MTU discovery through the translator: an ICMPv4 Fragmentation
// Needed error from the remote reaches the client as an ICMPv6 Packet Too
// Big quoting the session's IPv6 direction.
static int
test_pmtud_error_translated(struct nat64s_env *env) {
	// Self-contained: refresh the session at this test's own timestamp,
	// later tests may have advanced the clock past its timeout.
	dataplane_ut_set_time_ns(env->ut, 7000000000000ull);
	struct packet *warm = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn(env, CLIENT_PORT, &warm), "build failed"
	);
	TEST_ASSERT_SUCCESS(
		nat64s_run(env, warm, &out, &dropped), "run failed"
	);
	TEST_ASSERT(!dropped, "the session refresh SYN must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));

	struct packet *in = NULL;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_frag_needed(env, env->allocated_port, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped, "a PMTUD error of a live session must not be dropped"
	);
	TEST_ASSERT_NOT_NULL(out, "a PMTUD error must be translated");

	TEST_ASSERT(
		out->network_header.type ==
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
		"the translated error must be IPv6"
	);
	struct rte_mbuf *mbuf = packet_to_mbuf(out);
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, out->network_header.offset
	);
	TEST_ASSERT(
		memcmp(ipv6->dst_addr, client_addr, 16) == 0,
		"the error must be delivered to the client"
	);
	TEST_ASSERT(
		memcmp(ipv6->src_addr, nat64_prefix, 12) == 0,
		"the error must come from under the NAT64 prefix"
	);
	struct yanet_icmp6_hdr *icmp = rte_pktmbuf_mtod_offset(
		mbuf, struct yanet_icmp6_hdr *, out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		icmp->icmp6_type,
		2,
		"Fragmentation Needed must become Packet Too Big"
	);
	// The quoted packet keeps its direction, client -> remote, in IPv6:
	// the client side becomes the session's client address and the remote
	// side embeds under the prefix.
	struct rte_ipv6_hdr *emb = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, out->transport_header.offset + 8
	);
	TEST_ASSERT(
		memcmp(emb->src_addr, client_addr, 16) == 0,
		"the quoted client address must be the session's client"
	);
	TEST_ASSERT(
		memcmp(emb->dst_addr, nat64_prefix, 12) == 0 &&
			memcmp(&emb->dst_addr[12], &remote_addr, 4) == 0,
		"the quoted remote address must be prefix + remote"
	);
	rte_pktmbuf_free(mbuf);
	return TEST_SUCCESS;
}

// A client-side error quotes the inbound direction of the session; it
// must still find the outbound-stored session and translate to IPv4.
static int
test_client_error_translated(struct nat64s_env *env) {
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_packet_too_big(env, CLIENT_PORT, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped, "a client error of a live session must not be dropped"
	);
	TEST_ASSERT_NOT_NULL(out, "a client error must be translated");
	TEST_ASSERT(
		out->network_header.type ==
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
		"the translated error must be IPv4"
	);
	struct rte_mbuf *mbuf = packet_to_mbuf(out);
	struct rte_ipv4_hdr *ipv4 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, out->network_header.offset
	);
	TEST_ASSERT_EQUAL(
		ipv4->dst_addr,
		remote_addr,
		"the error must be delivered to the remote"
	);
	TEST_ASSERT_EQUAL(
		ipv4->src_addr,
		pool_addrs[0],
		"the error must come from the pool address"
	);
	rte_pktmbuf_free(mbuf);
	return TEST_SUCCESS;
}

// An error quoting a tuple nobody registered is dropped.
static int
test_error_unknown_tuple_dropped(struct nat64s_env *env) {
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_frag_needed(env, env->allocated_port + 7, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(out, "an unknown quoted tuple must not be translated");
	TEST_ASSERT(dropped, "an unknown quoted tuple must be dropped");
	return TEST_SUCCESS;
}

// RFC 6146 5.2 endpoint-independent mapping: the same client endpoint
// reaching a second destination reuses the external identity the first
// session allocated.
static int
test_endpoint_independent_mapping(struct nat64s_env *env) {
	// Two fresh destinations, so the main session is left alone.
	const uint8_t remote2[4] = {192, 0, 2, 99};
	const uint8_t remote3[4] = {192, 0, 2, 100};

	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_to(env, CLIENT_PORT, remote2, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the first SYN must not be dropped");
	struct rte_mbuf *first_mbuf = packet_to_mbuf(out);
	struct rte_tcp_hdr *first_tcp = rte_pktmbuf_mtod_offset(
		first_mbuf, struct rte_tcp_hdr *, out->transport_header.offset
	);
	uint16_t first_port = rte_be_to_cpu_16(first_tcp->src_port);
	struct rte_ipv4_hdr *first_ip4 = rte_pktmbuf_mtod_offset(
		first_mbuf, struct rte_ipv4_hdr *, out->network_header.offset
	);
	uint32_t first_src = first_ip4->src_addr;
	rte_pktmbuf_free(first_mbuf);

	// Same client endpoint, another destination.
	struct packet *second = NULL;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_to(env, CLIENT_PORT, remote3, &second),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(
		nat64s_run(env, second, &out, &dropped), "run failed"
	);
	TEST_ASSERT(!dropped, "the second SYN must not be dropped");
	struct rte_mbuf *second_mbuf = packet_to_mbuf(out);
	struct rte_tcp_hdr *second_tcp = rte_pktmbuf_mtod_offset(
		second_mbuf, struct rte_tcp_hdr *, out->transport_header.offset
	);
	struct rte_ipv4_hdr *second_ip4 = rte_pktmbuf_mtod_offset(
		second_mbuf, struct rte_ipv4_hdr *, out->network_header.offset
	);
	TEST_ASSERT_EQUAL(
		second_ip4->src_addr,
		first_src,
		"the same client endpoint must keep its external address"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(second_tcp->src_port),
		first_port,
		"the same client endpoint must keep its external port"
	);
	rte_pktmbuf_free(second_mbuf);
	return TEST_SUCCESS;
}

// Several prefixes on one config: a flow under the second prefix keeps
// it — the return packet's translated source embeds under that prefix,
// not the first one.
static int
test_session_keeps_own_prefix(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 5000000000000ull);

	// A SYN whose destination embeds under the second prefix.
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_to2(env, CLIENT_PORT, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped, "the SYN under the second prefix must not be dropped"
	);
	TEST_ASSERT_NOT_NULL(
		out, "the SYN under the second prefix must translate"
	);

	// The return packet for that session: its translated IPv6 source
	// must embed under the second prefix.
	struct packet *reply = NULL;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, env->allocated_port, &reply),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(
		nat64s_run(env, reply, &out, &dropped), "run failed"
	);
	TEST_ASSERT(!dropped, "the reply must not be dropped");
	struct rte_mbuf *mbuf = packet_to_mbuf(out);
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, out->network_header.offset
	);
	TEST_ASSERT(
		memcmp(ipv6->src_addr, prefixes[1], 12) == 0,
		"the return source must embed under the session's own prefix"
	);
	TEST_ASSERT(
		memcmp(ipv6->src_addr, prefixes[0], 12) != 0,
		"the return source must not fall back to the first prefix"
	);
	rte_pktmbuf_free(mbuf);
	return TEST_SUCCESS;
}

// The simultaneous open of RFC 6146 3.5.2.2: a return SYN without an
// ACK establishes the session too.
static int
test_reverse_syn_establishes(struct nat64s_env *env) {
	env->config->timeouts.timeout = 30;
	env->config->timeouts.tcp_established = 300;

	dataplane_ut_set_time_ns(env->ut, 4000000000000ull);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn(env, CLIENT_PORT, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));

	// A return SYN with no ACK: the simultaneous open.
	struct packet *reply = NULL;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, env->allocated_port, &reply),
		"build failed"
	);
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(reply),
		struct rte_tcp_hdr *,
		reply->transport_header.offset
	);
	tcp->tcp_flags = RTE_TCP_SYN_FLAG;
	TEST_ASSERT_SUCCESS(
		nat64s_run(env, reply, &out, &dropped), "run failed"
	);
	TEST_ASSERT(!dropped, "the reverse SYN must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Past the general timeout the session must still answer: the
	// reverse SYN latched it.
	dataplane_ut_set_time_ns(
		env->ut, 4000000000000ull + 60ull * 1000000000ull
	);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, env->allocated_port, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"a simultaneous-open session must outlive the general timeout"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	env->config->timeouts.timeout = 300;
	env->config->timeouts.tcp_established = 300;
	return TEST_SUCCESS;
}

// The established latch: a return SYN-ACK moves the session onto the
// established timeout, so it outlives the general one.
static int
test_established_latch(struct nat64s_env *env) {
	// Short general timeout, long established one.
	env->config->timeouts.timeout = 30;
	env->config->timeouts.tcp_established = 300;

	dataplane_ut_set_time_ns(env->ut, 3000000000000ull);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn(env, CLIENT_PORT, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));

	// The SYN-ACK return carries an ACK: the session latches.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, env->allocated_port, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN-ACK must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Past the general timeout the session must still answer: the ACK
	// latched it onto the established one.
	dataplane_ut_set_time_ns(
		env->ut, 3000000000000ull + 60ull * 1000000000ull
	);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, env->allocated_port, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"an established session must outlive the general timeout"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Restore the suite's timeouts.
	env->config->timeouts.timeout = 300;
	env->config->timeouts.tcp_established = 300;
	return TEST_SUCCESS;
}

// An active flow survives well into its timeout window: every reply
// refreshed the wan entry.
static int
test_session_survives_refresh_window(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(
		env->ut, 1000000000000ull + 60ull * 1000000000ull
	);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, env->allocated_port, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	for (int i = 0; i < NAT64S_STAT_COUNT; ++i) {
	}
	TEST_ASSERT(
		!dropped, "a refreshed session must outlive half its timeout"
	);
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
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, env->allocated_port, &in),
		"build failed"
	);
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
		res = test_icmp_echo_roundtrip(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_pmtud_error_translated(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_client_error_translated(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_error_unknown_tuple_dropped(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_endpoint_independent_mapping(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_in_window_port_kept(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_established_latch(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_session_keeps_own_prefix(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_reverse_syn_establishes(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_session_survives_refresh_window(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_session_expires(&env);
	}

	nat64s_env_fini(&env);
	return (res == TEST_SUCCESS) ? 0 : 1;
}
