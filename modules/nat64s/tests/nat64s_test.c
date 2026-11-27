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
#include "lib/statemap/fwtable.h"

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

// The NAT64 prefixes the config carries: two network-specific
// documentation prefixes — a session uses the one its destination embeds
// under.
//
// The suite's remotes are documentation addresses, which only a
// network-specific prefix may stand for: the Well-Known Prefix is
// reserved for global ones (RFC 6052 3.1) and has its own scenario.
static const uint8_t nat64_prefix[12] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0x01
};
static const uint8_t prefixes[2][12] = {
	{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0x01},
	{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0x64},
};
// The Well-Known Prefix, 64:ff9b::/96, in place of the suite's first one.
static const uint8_t well_known_prefixes[2][12] = {
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
// Another address of the pool, one no session is ever translated to.
static const uint32_t other_pool_addr = RTE_BE32(RTE_IPV4(198, 51, 100, 9));
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

// The session knobs one harness round is configured with.
struct nat64s_env_settings {
	uint32_t port_lo;
	uint32_t port_hi;
	struct nat64state_timeouts timeouts;
	// The IPv6 MTU limit of the round; zero leaves the default.
	uint32_t mtu_ipv6;
	// Whether the config carries the Well-Known Prefix in place of the
	// suite's first one.
	bool well_known;
};

// The suite's own settings. The client's port (50000) falls outside the
// window, so its session is rewritten into it.
static const struct nat64s_env_settings nat64s_suite_settings = {
	.port_lo = 1024,
	.port_hi = 4096,
	.timeouts = {.timeout = 300, .tcp_established = 300},
};

// The module update carrying one settings block over the suite's fixed
// prefixes, pool and map link.
static struct nat64s_module_update
nat64s_env_update(const struct nat64s_env_settings *settings) {
	return (struct nat64s_module_update){
		.prefixes = settings->well_known ? well_known_prefixes[0]
						 : prefixes[0],
		.prefix_count = 2,
		.pool = pool_addrs,
		.pool_count = 1,
		.port_lo = settings->port_lo,
		.port_hi = settings->port_hi,
		.timeouts = settings->timeouts,
		.mtu_ipv6 = (uint16_t)settings->mtu_ipv6,
		.map_name = "sessions",
	};
}

static int
nat64s_env_init(
	struct nat64s_env *env, const struct nat64s_env_settings *settings
) {
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

	// The module: prefixes, pool, the map link, and the settings' window
	// and timeouts.
	struct cp_module *module =
		nat64s_module_config_create(env->agent, "t0", &err);
	TEST_ASSERT_NOT_NULL(module, "nat64s_module_config_create failed");
	env->config = (struct nat64s_module_config *)container_of(
		module, struct nat64s_module_config, cp_module
	);

	struct nat64s_module_update update = nat64s_env_update(settings);
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

// Reconfigure the suite's live module in place through the module's API
// with the settings' window and timeouts.
//
// Only the single-threaded harness allows a published config to change
// underneath the dataplane: an operator's update publishes a fresh one.
static int
nat64s_env_reconfigure(
	struct nat64s_env *env, const struct nat64s_env_settings *settings
) {
	struct nat64s_module_update update = nat64s_env_update(settings);
	yanet_error *err = NULL;
	TEST_ASSERT_SUCCESS(
		nat64s_module_config_update(
			&env->config->cp_module, &update, &err
		),
		"nat64s_module_config_update failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

// Build a parsed IPv6 TCP SYN from the given client to the remote under
// the NAT64 prefix.
static int
nat64s_make_v6_syn_from(
	struct nat64s_env *env,
	const uint8_t client6[16],
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
	memcpy(ipv6->src_addr, client6, 16);
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
nat64s_make_v6_syn_to(
	struct nat64s_env *env,
	uint16_t client_port,
	const uint8_t remote[4],
	struct packet **out
) {
	return nat64s_make_v6_syn_from(
		env, client_addr, client_port, remote, out
	);
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

// Build a parsed TCP SYN from the client to the remote under the NAT64
// prefix, with a Routing header of the given type ahead of the TCP
// header.
static int
nat64s_make_v6_syn_with_routing(
	struct nat64s_env *env,
	uint16_t client_port,
	uint8_t routing_type,
	struct packet **out
) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(env->ut);
	TEST_ASSERT_NOT_NULL(mbuf, "dataplane_ut_alloc_mbuf returned NULL");

	uint8_t *data = (uint8_t *)rte_pktmbuf_append(
		mbuf,
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv6_hdr) + 8 +
			sizeof(struct rte_tcp_hdr)
	);
	TEST_ASSERT_NOT_NULL(data, "packet payload allocation failed");
	memset(data, 0, rte_pktmbuf_data_len(mbuf));

	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)data;
	ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	struct rte_ipv6_hdr *ipv6 = (struct rte_ipv6_hdr *)(ether + 1);
	memcpy(ipv6->src_addr, client_addr, 16);
	memcpy(ipv6->dst_addr, nat64_prefix, 12);
	memcpy(&ipv6->dst_addr[12], remote4, 4);
	ipv6->proto = IPPROTO_ROUTING;
	ipv6->payload_len = rte_cpu_to_be_16(8 + sizeof(struct rte_tcp_hdr));

	uint8_t *routing = (uint8_t *)(ipv6 + 1);
	routing[0] = IPPROTO_TCP; // next header
	routing[1] = 0;		  // eight bytes, less one unit
	routing[2] = routing_type;
	routing[3] = 0; // segments left

	struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)(routing + 8);
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

// Build a parsed ICMPv6 echo request with the given identifier from the
// given client.
static int
nat64s_make_v6_echo_from(
	struct nat64s_env *env,
	const uint8_t client6[16],
	uint16_t ident,
	struct packet **out
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
	memcpy(ipv6->src_addr, client6, 16);
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

// Build a parsed ICMPv6 echo request from the client.
static int
nat64s_make_v6_echo(
	struct nat64s_env *env, uint16_t ident, struct packet **out
) {
	return nat64s_make_v6_echo_from(env, client_addr, ident, out);
}

// Build a parsed IPv4 TCP reply from the remote towards the pool, its
// IPv4 header extended by four option bytes: a source-route option of
// the given type, or nothing but the end of the options list when the
// type is zero.
static int
nat64s_make_v4_reply_with_option(
	struct nat64s_env *env,
	uint16_t allocated_port,
	uint8_t option_type,
	struct packet **out
) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(env->ut);
	TEST_ASSERT_NOT_NULL(mbuf, "dataplane_ut_alloc_mbuf returned NULL");

	uint8_t *data = (uint8_t *)rte_pktmbuf_append(
		mbuf,
		sizeof(struct rte_ether_hdr) + 24 + sizeof(struct rte_tcp_hdr)
	);
	TEST_ASSERT_NOT_NULL(data, "packet payload allocation failed");
	memset(data, 0, rte_pktmbuf_data_len(mbuf));

	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)data;
	ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ipv4 = (struct rte_ipv4_hdr *)(ether + 1);
	ipv4->version_ihl = RTE_IPV4_VHL_DEF + 1;
	ipv4->total_length = rte_cpu_to_be_16(24 + sizeof(struct rte_tcp_hdr));
	ipv4->next_proto_id = IPPROTO_TCP;
	ipv4->src_addr = remote_addr;
	ipv4->dst_addr = pool_addrs[0];

	uint8_t *options = (uint8_t *)(ipv4 + 1);
	options[0] = option_type;
	options[1] = option_type ? 4 : 0; // length
	options[2] = option_type ? 4 : 0; // pointer into the route data

	struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)(options + 4);
	tcp->src_port = rte_cpu_to_be_16(REMOTE_PORT);
	tcp->dst_port = rte_cpu_to_be_16(allocated_port);
	tcp->tcp_flags = RTE_TCP_SYN_FLAG | RTE_TCP_ACK_FLAG;
	tcp->data_off = (sizeof(struct rte_tcp_hdr) / 4) << 4;

	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");
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

// Build a parsed ICMPv4 Destination Unreachable error from the remote,
// quoting the translated outbound echo request of an echo session.
static int
nat64s_make_v4_error_quoting_echo(
	struct nat64s_env *env, uint16_t translated_ident, struct packet **out
) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(env->ut);
	TEST_ASSERT_NOT_NULL(mbuf, "dataplane_ut_alloc_mbuf returned NULL");

	size_t icmp_len = 8 + sizeof(struct rte_ipv4_hdr) + 8;
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
	icmp[0] = ICMP_DEST_UNREACH;

	struct rte_ipv4_hdr *emb = (struct rte_ipv4_hdr *)(icmp + 8);
	emb->version_ihl = RTE_IPV4_VHL_DEF;
	emb->total_length = rte_cpu_to_be_16(sizeof(struct rte_ipv4_hdr) + 8);
	emb->next_proto_id = IPPROTO_ICMP;
	emb->src_addr = pool_addrs[0];
	emb->dst_addr = remote_addr;

	uint8_t *emb_icmp = (uint8_t *)(emb + 1);
	emb_icmp[0] = ICMP_ECHO;
	*(rte_be16_t *)(emb_icmp + 4) = rte_cpu_to_be_16(translated_ident);

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

// Build the client-side error of the suite's session with a Hop-by-Hop
// header ahead of the quoted TCP header, as the remote may have sent it.
static int
nat64s_make_v6_packet_too_big_with_options(
	struct nat64s_env *env, uint16_t client_port, struct packet **out
) {
	struct packet *packet = NULL;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_packet_too_big(env, client_port, &packet),
		"build failed"
	);
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	TEST_ASSERT_NOT_NULL(
		rte_pktmbuf_append(mbuf, 8), "packet payload allocation failed"
	);
	uint8_t *icmp = rte_pktmbuf_mtod_offset(
		mbuf, uint8_t *, packet->transport_header.offset
	);
	struct rte_ipv6_hdr *emb = (struct rte_ipv6_hdr *)(icmp + 8);
	uint8_t *options = (uint8_t *)(emb + 1);
	memmove(options + 8, options, sizeof(struct rte_tcp_hdr));
	options[0] = IPPROTO_TCP; // the header that follows
	options[1] = 0;		  // its length in eight-octet units, less one
	memset(options + 2, 0, 6);
	emb->proto = IPPROTO_HOPOPTS;

	struct rte_ipv6_hdr *outer = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	outer->payload_len =
		rte_cpu_to_be_16(rte_be_to_cpu_16(outer->payload_len) + 8);
	TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");
	*out = packet;
	return TEST_SUCCESS;
}

// Build a parsed ICMPv6 Packet Too Big error from the client, quoting the
// inbound echo reply of an echo session as the client received it, under
// its own identifier.
static int
nat64s_make_v6_packet_too_big_quoting_echo(
	struct nat64s_env *env, uint16_t ident, struct packet **out
) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(env->ut);
	TEST_ASSERT_NOT_NULL(mbuf, "dataplane_ut_alloc_mbuf returned NULL");
	size_t icmp_len = 8 + sizeof(struct rte_ipv6_hdr) + 8;
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
	memcpy(&ipv6->dst_addr[12], remote4, 4);
	ipv6->proto = IPPROTO_ICMPV6;
	ipv6->payload_len = rte_cpu_to_be_16(icmp_len);

	uint8_t *icmp = (uint8_t *)(ipv6 + 1);
	icmp[0] = 2; // ICMP6_PACKET_TOO_BIG

	struct rte_ipv6_hdr *emb = (struct rte_ipv6_hdr *)(icmp + 8);
	memcpy(emb->src_addr, nat64_prefix, 12);
	memcpy(&emb->src_addr[12], remote4, 4);
	memcpy(emb->dst_addr, client_addr, 16);
	emb->proto = IPPROTO_ICMPV6;
	emb->payload_len = rte_cpu_to_be_16(8);

	uint8_t *emb_icmp = (uint8_t *)(emb + 1);
	emb_icmp[0] = 129; // echo reply
	*(rte_be16_t *)(emb_icmp + 4) = rte_cpu_to_be_16(ident);

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

// A return packet the translation refuses refreshes nothing.
//
// The translation refuses a packet carrying a source-route option
// itself. Such a reply of a live session is dropped and does not keep
// the session alive: past its timeout without a translated packet, the
// session is gone. The same reply with no such option is translated.
static int
test_refused_return_packet_refreshes_nothing(struct nat64s_env *env) {
	const uint64_t sec = 1000000000ull;
	const uint64_t now = 3900000000000ull;
	dataplane_ut_set_time_ns(env->ut, now);
	// A port inside the window is the session's on both sides.
	const uint16_t port = 2120;
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN must open the session");
	rte_pktmbuf_free(packet_to_mbuf(out));

	// The control: a reply whose header ends its options list at once is
	// translated.
	dataplane_ut_set_time_ns(env->ut, now + 100 * sec);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply_with_option(env, port, 0, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped, "a reply without a source route must be translated"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// The remote's reply with a source-route option is dropped, and is
	// no translation.
	dataplane_ut_set_time_ns(env->ut, now + 250 * sec);
	uint64_t translated = env->config->stats[NAT64S_TRANSLATED_V4_TO_V6];
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply_with_option(env, port, 0x83, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(
		out, "a reply with a source-route option must not be forwarded"
	);
	TEST_ASSERT(
		dropped, "a reply with a source-route option must be dropped"
	);
	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_TRANSLATED_V4_TO_V6],
		translated,
		"a refused packet is no translation"
	);

	// Past its timeout since the last translated packet, the session is
	// gone.
	dataplane_ut_set_time_ns(env->ut, now + 500 * sec);
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	struct rte_tcp_hdr *ack = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(in),
		struct rte_tcp_hdr *,
		in->transport_header.offset
	);
	ack->tcp_flags = RTE_TCP_ACK_FLAG;
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(
		out, "a refused return packet must not keep a session alive"
	);
	TEST_ASSERT(dropped, "the session must have aged out unrefreshed");
	return TEST_SUCCESS;
}

// Refresh the suite's own session at the current time, so that an error
// about it is not dropped as one about an expired flow.
static int
nat64s_test_warm_session(struct nat64s_env *env) {
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn(env, CLIENT_PORT, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the session refresh SYN must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));
	return TEST_SUCCESS;
}

// Outbound translation: the SYN leaves as IPv4 with the pool address as
// its source, the remote as its destination, and its out-of-window source
// port translated into the window.
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
	// The client must recognize the quoted packet as its own: its source
	// port is the client's, not the translated one the remote saw.
	struct rte_tcp_hdr *emb_tcp = (struct rte_tcp_hdr *)(emb + 1);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(emb_tcp->src_port),
		CLIENT_PORT,
		"the quoted source port must be the client's own"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(emb_tcp->dst_port),
		REMOTE_PORT,
		"the quoted destination port must stay the remote's"
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
	// The remote must recognize the quoted packet as the one it sent: it
	// came from the remote to the session's public address, its source
	// port is the remote's and its destination port the allocated one,
	// not the client's own. The quote left as IPv4, its header shrunk to
	// the IPv4 size.
	struct rte_ipv4_hdr *emb = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_ipv4_hdr *,
		out->transport_header.offset + sizeof(struct rte_icmp_hdr)
	);
	TEST_ASSERT_EQUAL(
		emb->src_addr,
		remote_addr,
		"the quoted source address must be the remote"
	);
	TEST_ASSERT_EQUAL(
		emb->dst_addr,
		pool_addrs[0],
		"the quoted destination address must be the session's public "
		"one"
	);
	struct rte_tcp_hdr *emb_tcp = (struct rte_tcp_hdr *)(emb + 1);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(emb_tcp->src_port),
		REMOTE_PORT,
		"the quoted source port must stay the remote's"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(emb_tcp->dst_port),
		env->allocated_port,
		"the quoted destination port must be the allocated one"
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

// The same client endpoint starts from one external identity towards
// every destination.
//
// A second destination's SYN leaves with the first session's address and
// port, as long as that identity is free towards it.
static int
test_external_identity_derives_from_client_endpoint(struct nat64s_env *env) {
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

	// The SYN-ACK return carries the remote's SYN: the session latches.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, env->allocated_port, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN-ACK must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Past the general timeout the session must still answer: the
	// remote's SYN latched it onto the established one.
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

// Set the TCP flags of a packet built by one of the TCP builders.
static void
nat64s_test_set_tcp_flags(struct packet *packet, uint8_t flags) {
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(packet),
		struct rte_tcp_hdr *,
		packet->transport_header.offset
	);
	tcp->tcp_flags = flags;
}

// Move the tail of a built TCP packet's header into a second segment,
// keeping the given number of its leading bytes in the first.
//
// The first segment's tailroom past them is dirtied: a header field the
// module or the translation read or wrote there instead of in the second
// segment shows.
static int
nat64s_test_split_tcp_header(
	struct nat64s_env *env, struct packet *packet, size_t kept
) {
	struct rte_mbuf *head = packet_to_mbuf(packet);
	const size_t moved = sizeof(struct rte_tcp_hdr) - kept;
	struct rte_mbuf *rest = dataplane_ut_alloc_mbuf(env->ut);
	TEST_ASSERT_NOT_NULL(rest, "dataplane_ut_alloc_mbuf returned NULL");

	uint8_t *rest_data = (uint8_t *)rte_pktmbuf_append(rest, moved);
	TEST_ASSERT_NOT_NULL(rest_data, "packet payload allocation failed");
	memcpy(rest_data,
	       rte_pktmbuf_mtod_offset(
		       head, uint8_t *, packet->transport_header.offset
	       ) + kept,
	       moved);
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_trim(head, (uint16_t)moved),
		0,
		"trimming the head segment failed"
	);

	TEST_ASSERT(
		rte_pktmbuf_tailroom(head) >= 32,
		"the head segment has no tailroom to dirty"
	);
	memset(rte_pktmbuf_mtod_offset(
		       head, uint8_t *, rte_pktmbuf_data_len(head)
	       ),
	       0xff,
	       32);
	TEST_ASSERT_EQUAL(
		rte_pktmbuf_chain(head, rest), 0, "chaining the segments failed"
	);
	TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");
	return TEST_SUCCESS;
}

// A client's own ACK does not establish its session (RFC 6146 3.5.2.2).
//
// Until the remote answers, the session ages on the general timeout
// whatever its client sends after the SYN.
static int
test_client_ack_keeps_init_lifetime(struct nat64s_env *env) {
	// Short general timeout, long established one.
	env->config->timeouts.timeout = 30;
	env->config->timeouts.tcp_established = 300;

	dataplane_ut_set_time_ns(env->ut, 3500000000000ull);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2016, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN must not be dropped");
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	uint16_t allocated_port = rte_be_to_cpu_16(tcp->src_port);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// The client's ACK, the remote having answered nothing: forwarded
	// like any other packet of the session.
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2016, &in), "build failed");
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the client's ACK must be translated");
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Past the general timeout the session is gone, as one the remote
	// never answered.
	dataplane_ut_set_time_ns(
		env->ut, 3500000000000ull + 60ull * 1000000000ull
	);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, allocated_port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(
		out,
		"a session the remote never answered must keep the general "
		"timeout"
	);
	TEST_ASSERT(dropped, "the reply to an expired session must be dropped");

	// Restore the suite's timeouts.
	env->config->timeouts.timeout = 300;
	env->config->timeouts.tcp_established = 300;
	return TEST_SUCCESS;
}

// Only the remote's SYN establishes a session (RFC 6146 3.5.2.2).
//
// The remote's refusal of the connection, a reset carrying an ACK, is
// forwarded but leaves the session on the general timeout.
static int
test_remote_reset_does_not_establish(struct nat64s_env *env) {
	// Short general timeout, long established one.
	env->config->timeouts.timeout = 30;
	env->config->timeouts.tcp_established = 300;

	dataplane_ut_set_time_ns(env->ut, 3600000000000ull);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2019, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN must not be dropped");
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	uint16_t allocated_port = rte_be_to_cpu_16(tcp->src_port);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// The remote's reset of the connection: forwarded like any other
	// packet of the session.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, allocated_port, &in), "build failed"
	);
	nat64s_test_set_tcp_flags(in, RTE_TCP_RST_FLAG | RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the remote's reset must be translated");
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Past the general timeout the session is gone, as one the remote
	// never answered with a SYN.
	dataplane_ut_set_time_ns(
		env->ut, 3600000000000ull + 60ull * 1000000000ull
	);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, allocated_port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(
		out,
		"a session the remote answered without a SYN must keep the "
		"general timeout"
	);
	TEST_ASSERT(dropped, "the reply to an expired session must be dropped");

	// Restore the suite's timeouts.
	env->config->timeouts.timeout = 300;
	env->config->timeouts.tcp_established = 300;
	return TEST_SUCCESS;
}

// Latch the entry a key names in one of the session tables as
// established, by hand.
//
// That is the half-state a packet of the session's other side leaves
// between another packet's lookup and its insert, which one worker
// cannot produce by interleaving.
static int
nat64s_test_latch(fwtable_t *table, uint64_t now, const void *key) {
	void *found = NULL;
	rwlock_t *lock = NULL;
	bool from_stale = false;
	TEST_ASSERT(
		fwtable_lookup(table, now, key, &found, &lock, &from_stale) >=
			0,
		"the entry to latch must exist"
	);
	((struct nat64state_value *)found)->tcp_established = true;
	if (lock != NULL) {
		rwlock_read_unlock(lock);
	}
	return TEST_SUCCESS;
}

// The lan key of the TCP session a client port opens towards the remote.
static struct nat64state_lan_key
nat64s_test_lan_key(uint16_t client_port) {
	struct nat64state_lan_key key = {
		.hdr = {.proto = IPPROTO_TCP,
			.src_port = client_port,
			.dst_port = REMOTE_PORT},
	};
	memcpy(key.src_addr, client_addr, sizeof(key.src_addr));
	memcpy(key.dst_addr, nat64_prefix, NAT64S_PREFIX_LEN);
	memcpy(key.dst_addr + NAT64S_PREFIX_LEN, remote4, sizeof(remote4));
	return key;
}

// The wan key of that session, from the port it was translated to.
static struct nat64state_wan_key
nat64s_test_wan_key(uint16_t allocated_port) {
	return (struct nat64state_wan_key){
		.hdr = {.proto = IPPROTO_TCP,
			.src_port = allocated_port,
			.dst_port = REMOTE_PORT},
		.local_addr = pool_addrs[0],
		.remote_addr = remote_addr,
	};
}

// A client packet that finds the wan entry latched, though it read the
// session as not established, refreshes on the established timeout.
//
// Otherwise the session ages out as one the remote never answered: past
// the general timeout neither side's packet would find it.
static int
test_latched_wan_entry_holds_client_refresh(struct nat64s_env *env) {
	// Short general timeout, long established one.
	env->config->timeouts.timeout = 30;
	env->config->timeouts.tcp_established = 300;

	const uint64_t base = 3700000000000ull;
	const uint64_t sec = 1000000000ull;
	dataplane_ut_set_time_ns(env->ut, base);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2023, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN must not be dropped");
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	uint16_t allocated_port = rte_be_to_cpu_16(tcp->src_port);
	rte_pktmbuf_free(packet_to_mbuf(out));

	struct nat64state_wan_key wan_key = nat64s_test_wan_key(allocated_port);
	TEST_ASSERT_SUCCESS(
		nat64s_test_latch(
			fwstate_map_nat64_object_wan_table(&env->map->cp_object
			),
			base,
			&wan_key
		),
		"latch failed"
	);

	// The client's ACK, which read the session as not established.
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2023, &in), "build failed");
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the client's ACK must be translated");
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Past the general timeout the client's entry still stands on its
	// own, then the remote's on its own.
	dataplane_ut_set_time_ns(env->ut, base + 60 * sec);
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2023, &in), "build failed");
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"the client's entry must outlive the general timeout once "
		"established"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, allocated_port, &in), "build failed"
	);
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"the session must outlive the general timeout once established"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Restore the suite's timeouts.
	env->config->timeouts.timeout = 300;
	env->config->timeouts.tcp_established = 300;
	return TEST_SUCCESS;
}

// A return packet that finds the lan entry latched, though it read the
// session as not established, refreshes it on the established timeout.
//
// Otherwise the client's entry dies with the general timeout while the
// session is established, and the client's next packet is dropped as a
// new flow.
static int
test_latched_lan_entry_holds_return_refresh(struct nat64s_env *env) {
	// Short general timeout, long established one.
	env->config->timeouts.timeout = 30;
	env->config->timeouts.tcp_established = 300;

	const uint64_t base = 3800000000000ull;
	const uint64_t sec = 1000000000ull;
	dataplane_ut_set_time_ns(env->ut, base);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2024, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN must not be dropped");
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	uint16_t allocated_port = rte_be_to_cpu_16(tcp->src_port);
	rte_pktmbuf_free(packet_to_mbuf(out));

	struct nat64state_lan_key lan_key = nat64s_test_lan_key(2024);
	TEST_ASSERT_SUCCESS(
		nat64s_test_latch(
			fwstate_map_nat64_object_lan_table(&env->map->cp_object
			),
			base,
			&lan_key
		),
		"latch failed"
	);

	// The remote's plain ACK, which read the session as not established
	// and establishes nothing by itself.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, allocated_port, &in), "build failed"
	);
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the remote's ACK must be translated");
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Past the general timeout the client's ACK still finds its entry.
	dataplane_ut_set_time_ns(env->ut, base + 60 * sec);
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2024, &in), "build failed");
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"the client's entry must outlive the general timeout once the "
		"session is established"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Restore the suite's timeouts.
	env->config->timeouts.timeout = 300;
	env->config->timeouts.tcp_established = 300;
	return TEST_SUCCESS;
}

// A session outlives the client's silence: return traffic alone keeps
// the client's entry alive.
//
// A client that stayed quiet past the general timeout still finds its
// session, instead of being taken for a new flow and dropped by the
// SYN-only rule.
static int
test_return_traffic_keeps_lan_entry(struct nat64s_env *env) {
	// A short general timeout, so the lan entry would expire inside
	// this test without the refresh; the established one is long, so
	// the session itself stays live.
	env->config->timeouts.timeout = 30;
	env->config->timeouts.tcp_established = 300;

	// A fresh client port, so this test does not disturb the suite's
	// own session.
	const uint16_t port = 2005;
	const uint64_t base = 11000000000000ull;
	const uint64_t sec = 1000000000ull;
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;

	// The client opens the session and the remote's SYN-ACK establishes
	// it.
	dataplane_ut_set_time_ns(env->ut, base);
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the SYN must translate");
	struct rte_tcp_hdr *out_tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	const uint16_t allocated = rte_be_to_cpu_16(out_tcp->src_port);
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, allocated, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN-ACK must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));

	// The client stays silent. Well past the general timeout the remote
	// is still sending, which is what keeps the session alive.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, allocated, &in), "build failed"
	);
	// A plain ACK from the remote, not the builder's SYN-ACK.
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	dataplane_ut_set_time_ns(env->ut, base + 60 * sec);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped, "return traffic on a live session must not be dropped"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// The client speaks again, with a plain ACK rather than a SYN. It
	// must be forwarded: the entry it needs was kept alive by the
	// remote's traffic, and carries the session's external identity.
	dataplane_ut_set_time_ns(env->ut, base + 61 * sec);
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"a client returning to a session the remote kept alive must be "
		"forwarded"
	);
	TEST_ASSERT_NOT_NULL(out, "the client's ACK must be translated");
	struct rte_ipv4_hdr *ipv4 = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_ipv4_hdr *,
		out->network_header.offset
	);
	TEST_ASSERT_EQUAL(
		ipv4->src_addr,
		pool_addrs[0],
		"the refreshed entry must carry the session's external address"
	);
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(tcp->src_port),
		allocated,
		"the refreshed entry must carry the session's external port"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	env->config->timeouts.timeout = 300;
	env->config->timeouts.tcp_established = 300;
	return TEST_SUCCESS;
}

// The client's address with another final byte: a second client of the
// lan.
static void
nat64s_test_other_client(uint8_t out[16], uint8_t last_byte) {
	memcpy(out, client_addr, 16);
	out[15] = last_byte;
}

// Check that a translated return packet carries the given client as its
// IPv6 destination, port included, and release it.
static int
nat64s_test_assert_reply_to(
	struct packet *out, const uint8_t client6[16], uint16_t client_port
) {
	TEST_ASSERT_NOT_NULL(out, "the reply must be translated");
	TEST_ASSERT(
		out->network_header.type ==
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
		"the reply must be IPv6"
	);
	struct rte_mbuf *mbuf = packet_to_mbuf(out);
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, out->network_header.offset
	);
	TEST_ASSERT(
		memcmp(ipv6->dst_addr, client6, 16) == 0,
		"the reply must reach its own client"
	);
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_tcp_hdr *, out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(tcp->dst_port),
		client_port,
		"the reply must restore the client's port"
	);
	rte_pktmbuf_free(mbuf);
	return TEST_SUCCESS;
}

// Send one client's echo request through the pipeline and return the
// identifier it left with.
static int
nat64s_test_echo_request(
	struct nat64s_env *env,
	const uint8_t client6[16],
	uint16_t ident,
	uint16_t *translated
) {
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_echo_from(env, client6, ident, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the echo request must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the echo request must be translated");
	struct rte_icmp_hdr *icmp4 = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_icmp_hdr *,
		out->transport_header.offset
	);
	*translated = rte_be_to_cpu_16(icmp4->icmp_ident);
	rte_pktmbuf_free(packet_to_mbuf(out));
	return TEST_SUCCESS;
}

// Check that a translated echo reply carries the given client as its
// IPv6 destination and the client's own identifier, and release it.
static int
nat64s_test_assert_echo_reply_to(
	struct packet *out, const uint8_t client6[16], uint16_t ident
) {
	TEST_ASSERT_NOT_NULL(out, "the echo reply must be translated");
	TEST_ASSERT(
		out->network_header.type ==
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
		"the echo reply must be IPv6"
	);
	struct rte_mbuf *mbuf = packet_to_mbuf(out);
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, out->network_header.offset
	);
	TEST_ASSERT(
		memcmp(ipv6->dst_addr, client6, 16) == 0,
		"the echo reply must reach its own client"
	);
	struct yanet_icmp6_hdr *icmp6 = rte_pktmbuf_mtod_offset(
		mbuf, struct yanet_icmp6_hdr *, out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(icmp6->yanet_icmp6_id),
		ident,
		"the echo reply must restore the client's identifier"
	);
	rte_pktmbuf_free(mbuf);
	return TEST_SUCCESS;
}

// Send the remote's echo reply, which carries the identifier the request
// left with, and check which client it reaches.
static int
nat64s_test_echo_answered(
	struct nat64s_env *env,
	uint16_t translated,
	const uint8_t client6[16],
	uint16_t ident
) {
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_echo_reply(env, translated, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the echo reply must not be dropped");
	return nat64s_test_assert_echo_reply_to(out, client6, ident);
}

// An echo identifier outside the port window is translated like a port:
// the request leaves with an in-window identifier, and the reply, which
// carries that one, finds the session and restores the client's own.
static int
test_echo_identifier_outside_window_round_trips(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 20000000000000ull);
	const uint16_t ident = 7;

	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_echo(env, ident, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the echo request must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the echo request must be translated");
	struct rte_icmp_hdr *icmp4 = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_icmp_hdr *,
		out->transport_header.offset
	);
	uint16_t translated = rte_be_to_cpu_16(icmp4->icmp_ident);
	TEST_ASSERT(
		translated >= 1024 && translated < 4096,
		"an out-of-window identifier must be translated into the window"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_echo_reply(env, translated, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"the echo reply carrying the translated identifier must find "
		"the "
		"session"
	);
	TEST_ASSERT_NOT_NULL(out, "the echo reply must be translated");
	struct yanet_icmp6_hdr *icmp6 = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct yanet_icmp6_hdr *,
		out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		icmp6->icmp6_type,
		129,
		"the reply must become an ICMPv6 echo reply"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(icmp6->yanet_icmp6_id),
		ident,
		"the client's identifier must be restored"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// An error from the remote quoting that request is found through the
	// same session, and the client sees its own identifier in the quote.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_error_quoting_echo(env, translated, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"an error quoting the echo request must not be dropped"
	);
	TEST_ASSERT_NOT_NULL(
		out, "an error quoting the echo request must be translated"
	);
	struct rte_mbuf *mbuf = packet_to_mbuf(out);
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, out->network_header.offset
	);
	TEST_ASSERT(
		memcmp(ipv6->dst_addr, client_addr, 16) == 0,
		"the error must reach the client"
	);
	struct yanet_icmp6_hdr *quoted = rte_pktmbuf_mtod_offset(
		mbuf,
		struct yanet_icmp6_hdr *,
		out->transport_header.offset + sizeof(struct yanet_icmp6_hdr) +
			sizeof(struct rte_ipv6_hdr)
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(quoted->yanet_icmp6_id),
		ident,
		"the quoted identifier must be the client's own"
	);
	rte_pktmbuf_free(mbuf);

	// A client-side error quoting the inbound echo reply, under the
	// client's own identifier, finds the session as well: the remote
	// sees in the quote the identifier it answered under.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_packet_too_big_quoting_echo(env, ident, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped, "an error quoting the echo reply must not be dropped"
	);
	TEST_ASSERT_NOT_NULL(
		out, "an error quoting the echo reply must be translated"
	);
	mbuf = packet_to_mbuf(out);
	struct rte_icmp_hdr *quoted_reply = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_icmp_hdr *,
		out->transport_header.offset + sizeof(struct rte_icmp_hdr) +
			sizeof(struct rte_ipv4_hdr)
	);
	TEST_ASSERT_EQUAL(
		quoted_reply->icmp_type,
		ICMP_ECHOREPLY,
		"the quoted echo reply must leave as an ICMP echo reply"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(quoted_reply->icmp_ident),
		translated,
		"the quoted identifier must be the one the remote answered "
		"under"
	);
	rte_pktmbuf_free(mbuf);
	return TEST_SUCCESS;
}

// Two clients pinging one remote with the same in-window identifier: the
// second is moved to a free identifier on both ports of its session key,
// so the remote's answer, which carries the moved identifier, reaches the
// second client.
static int
test_echo_on_taken_identifier_moves_whole_key(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 20500000000000ull);

	uint8_t first_client[16];
	uint8_t second_client[16];
	nat64s_test_other_client(first_client, 0x61);
	nat64s_test_other_client(second_client, 0x62);

	uint16_t kept = 0;
	TEST_ASSERT_SUCCESS(
		nat64s_test_echo_request(env, first_client, 2000, &kept),
		"the first client's request failed"
	);
	TEST_ASSERT_EQUAL(kept, 2000, "an in-window identifier must be kept");

	uint16_t moved = 0;
	TEST_ASSERT_SUCCESS(
		nat64s_test_echo_request(env, second_client, 2000, &moved),
		"the second client's request failed"
	);
	TEST_ASSERT(
		moved >= 1024 && moved < 4096 && moved != 2000,
		"an identifier another client holds towards this remote must "
		"be "
		"moved inside the window"
	);

	TEST_ASSERT_SUCCESS(
		nat64s_test_echo_answered(env, moved, second_client, 2000),
		"the second client's answer must find the second client"
	);
	TEST_ASSERT_SUCCESS(
		nat64s_test_echo_answered(env, 2000, first_client, 2000),
		"the first client's answer must still find the first client"
	);
	return TEST_SUCCESS;
}

// A moved echo lands on no identifier another echo holds towards the same
// remote: with identifiers 2100 and 2101 taken, a third client's 2100
// moves to 2102, and every answer reaches its own client. The identifiers are
// not those of the scenario before, whose sessions are still alive.
static int
test_echo_probe_skips_identifiers_others_hold(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 20650000000000ull);

	uint8_t first_client[16];
	uint8_t holder_client[16];
	uint8_t moved_client[16];
	nat64s_test_other_client(first_client, 0x71);
	nat64s_test_other_client(holder_client, 0x72);
	nat64s_test_other_client(moved_client, 0x73);

	uint16_t ident = 0;
	TEST_ASSERT_SUCCESS(
		nat64s_test_echo_request(env, first_client, 2100, &ident),
		"the first client's request failed"
	);
	TEST_ASSERT_SUCCESS(
		nat64s_test_echo_request(env, holder_client, 2101, &ident),
		"the holder's request failed"
	);
	TEST_ASSERT_EQUAL(ident, 2101, "an in-window identifier must be kept");

	TEST_ASSERT_SUCCESS(
		nat64s_test_echo_request(env, moved_client, 2100, &ident),
		"the moved client's request failed"
	);
	TEST_ASSERT_EQUAL(
		ident,
		2102,
		"an echo must move past an identifier another echo holds"
	);

	TEST_ASSERT_SUCCESS(
		nat64s_test_echo_answered(env, 2102, moved_client, 2100),
		"the moved client's answer must find the moved client"
	);
	TEST_ASSERT_SUCCESS(
		nat64s_test_echo_answered(env, 2101, holder_client, 2101),
		"the holder's answer must find the holder"
	);
	TEST_ASSERT_SUCCESS(
		nat64s_test_echo_answered(env, 2100, first_client, 2100),
		"the first client's answer must find the first client"
	);
	return TEST_SUCCESS;
}

// An identifier of zero is the client's own like any other.
//
// The request leaves with a translated one, as zero lies outside the
// port window, and the reply comes back under the client's zero.
static int
test_echo_identifier_zero_round_trips(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 20750000000000ull);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_echo(env, 0, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the echo request must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the echo request must be translated");
	struct rte_icmp_hdr *icmp4 = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_icmp_hdr *,
		out->transport_header.offset
	);
	uint16_t translated = rte_be_to_cpu_16(icmp4->icmp_ident);
	TEST_ASSERT(
		translated != 0,
		"an identifier outside the window must be translated"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	return nat64s_test_echo_answered(env, translated, client_addr, 0);
}

// Of ICMP only echoes cross besides errors.
//
// A message of any other type is dropped before it takes a session: the
// remote's answer to the identifier it carried finds nothing.
static int
test_unsupported_icmp_type_takes_no_session(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 20800000000000ull);
	// An identifier inside the window: the answer carries it unchanged.
	const uint16_t ident = 2106;
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_echo(env, ident, &in), "build failed"
	);
	uint8_t *type = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(in), uint8_t *, in->transport_header.offset
	);
	*type = 139; // node information query
	uint64_t refused = env->config->stats[NAT64S_ICMP_UNSUPPORTED];
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		dropped,
		"an ICMP message that is neither an echo nor an error must be "
		"dropped"
	);

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_echo_reply(env, ident, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(
		out, "a message the translation drops must leave no session"
	);
	TEST_ASSERT(
		dropped, "the answer must be dropped for want of a session"
	);
	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_ICMP_UNSUPPORTED],
		refused + 1,
		"the dropped message must be counted"
	);

	// The same from the remote: a type the translation does not carry is
	// dropped although it holds the identifier of a live echo session.
	const uint16_t live_ident = 2107;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_echo(env, live_ident, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the echo request must open its session");
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_echo_reply(env, live_ident, &in), "build failed"
	);
	uint8_t *reply_type = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(in), uint8_t *, in->transport_header.offset
	);
	*reply_type = 13; // timestamp request
	refused = env->config->stats[NAT64S_ICMP_UNSUPPORTED];
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(
		out,
		"an ICMPv4 message that is neither an echo nor an error must "
		"be dropped"
	);
	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_ICMP_UNSUPPORTED],
		refused + 1,
		"the dropped message must be counted"
	);
	return TEST_SUCCESS;
}

// Towards one remote an external tuple belongs to one client: a second
// client arriving with the same in-window source port is moved to a free
// port, and each client's return traffic still reaches its own client.
static int
test_taken_tuple_moves_to_free_port(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 21000000000000ull);

	// The first client keeps its in-window port 2000: that is the tuple
	// the second client derives.
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2000, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the first client's SYN must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));

	uint8_t second_client[16];
	nat64s_test_other_client(second_client, 0x02);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_from(env, second_client, 2000, remote4, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the second client's SYN must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the second client's SYN must be translated");
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	uint16_t moved_port = rte_be_to_cpu_16(tcp->src_port);
	TEST_ASSERT(
		moved_port >= 1024 && moved_port < 4096,
		"the moved port must stay inside the window"
	);
	TEST_ASSERT(
		moved_port != 2000,
		"a port another client holds towards this remote must not be "
		"shared"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, moved_port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the second client's reply must not be dropped");
	TEST_ASSERT_SUCCESS(
		nat64s_test_assert_reply_to(out, second_client, 2000),
		"the second client's reply must reach the second client"
	);

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, 2000, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the first client's reply must not be dropped");
	TEST_ASSERT_SUCCESS(
		nat64s_test_assert_reply_to(out, client_addr, 2000),
		"the first client's reply must still reach the first client"
	);
	return TEST_SUCCESS;
}

// A client moved off a taken port walks the window upwards and past its
// end back to its first port: no port outside the window is ever used.
static int
test_taken_top_port_wraps_to_window_start(struct nat64s_env *env) {
	// Three ports, so the top one has no neighbor inside the window.
	const struct nat64s_env_settings narrow = {
		.port_lo = 2000,
		.port_hi = 2003,
		.timeouts = {.timeout = 300, .tcp_established = 300},
	};
	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &narrow), "reconfigure failed"
	);
	dataplane_ut_set_time_ns(env->ut, 21500000000000ull);

	// The first client holds the window's top port.
	uint8_t top_client[16];
	nat64s_test_other_client(top_client, 0x51);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_from(env, top_client, 2002, remote4, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the first client's SYN must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the first client's SYN must be translated");
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(tcp->src_port),
		2002,
		"an in-window source port must be kept"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// The second client derives the same top port and must move: with no
	// port above it inside the window, the search wraps to the start.
	uint8_t second_client[16];
	nat64s_test_other_client(second_client, 0x52);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_from(env, second_client, 2002, remote4, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the second client's SYN must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the second client's SYN must be translated");
	tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(tcp->src_port),
		2000,
		"a port moved past the window's end must wrap to its start"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, 2000, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the second client's reply must not be dropped");
	TEST_ASSERT_SUCCESS(
		nat64s_test_assert_reply_to(out, second_client, 2002),
		"the wrapped port's reply must reach the second client"
	);

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, 2002, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the first client's reply must not be dropped");
	TEST_ASSERT_SUCCESS(
		nat64s_test_assert_reply_to(out, top_client, 2002),
		"the top port's reply must still reach the first client"
	);

	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &nat64s_suite_settings),
		"restoring the suite's settings failed"
	);
	return TEST_SUCCESS;
}

// The default window ends with the last port a 16-bit field carries.
//
// A client already using port 65535 keeps it, and a second client moved
// off that port wraps to the window's first one.
static int
test_default_window_ends_with_last_port(struct nat64s_env *env) {
	// A window left unset selects the default one.
	const struct nat64s_env_settings full = {
		.timeouts = {.timeout = 300, .tcp_established = 300},
	};
	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &full), "reconfigure failed"
	);
	dataplane_ut_set_time_ns(env->ut, 21600000000000ull);

	// The last port lies inside the window: the client keeps it.
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn(env, 65535, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN from port 65535 must not be dropped");
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(tcp->src_port),
		65535,
		"the last port lies inside the default window and must be kept"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// A second client arriving with the same port, the window's top
	// one, must move: with no port above it inside the window, the
	// search wraps to the first one. That port is free towards the
	// remote here: the only live sessions hold 2000 and 2002.
	uint8_t second_client[16];
	nat64s_test_other_client(second_client, 0x52);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_from(
			env, second_client, 65535, remote4, &in
		),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the second client's SYN must not be dropped");
	tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(tcp->src_port),
		1024,
		"a client moved off the last port must wrap to the first one"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Each answer reaches its own client.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, 1024, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the wrapped port's reply must not be dropped");
	TEST_ASSERT_SUCCESS(
		nat64s_test_assert_reply_to(out, second_client, 65535),
		"the wrapped port's reply must reach the second client"
	);

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, 65535, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the first client's reply must not be dropped");
	TEST_ASSERT_SUCCESS(
		nat64s_test_assert_reply_to(out, client_addr, 65535),
		"the last port's reply must still reach the first client"
	);

	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &nat64s_suite_settings),
		"restoring the suite's settings failed"
	);
	return TEST_SUCCESS;
}

// An IPv6 MTU limit above the largest one the translation carries is
// held down to it.
//
// The limit reaches the config as the largest carried value, so a direct
// caller cannot hand the translation an arithmetic it does not hold.
static int
test_ipv6_mtu_above_carried_is_held_down(struct nat64s_env *env) {
	const struct nat64s_env_settings large = {
		.port_lo = 1024,
		.port_hi = 4096,
		.timeouts = {.timeout = 300, .tcp_established = 300},
		.mtu_ipv6 = 40000,
	};
	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &large), "reconfigure failed"
	);
	TEST_ASSERT_EQUAL(
		env->config->mtu.ipv6,
		NAT64S_MTU_IPV6_MAX,
		"an IPv6 MTU limit above the carried one must be held down"
	);

	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &nat64s_suite_settings),
		"restoring the suite's settings failed"
	);
	return TEST_SUCCESS;
}

// An ICMPv6 error whose source lies under a NAT64 prefix is hairpinned
// traffic like any other packet: it is dropped before the session its
// quote would match is ever looked up.
static int
test_hairpinned_error_dropped(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 22000000000000ull);

	// The quoted session must be alive at this test's timestamp for the
	// drop to be the hairpin rule's doing rather than a missed session.
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn(env, CLIENT_PORT, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the session refresh SYN must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_packet_too_big(env, CLIENT_PORT, &in),
		"build failed"
	);
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(in),
		struct rte_ipv6_hdr *,
		in->network_header.offset
	);
	memcpy(ipv6->src_addr, nat64_prefix, 12);
	memcpy(&ipv6->src_addr[12], remote4, 4);

	uint64_t hairpin_before = env->config->stats[NAT64S_HAIRPIN];
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		dropped,
		"an error sourced from under a NAT64 prefix must be dropped"
	);
	TEST_ASSERT_NULL(out, "a hairpinned error must not be translated");
	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_HAIRPIN],
		hairpin_before + 1,
		"the hairpin counter must count the error"
	);
	return TEST_SUCCESS;
}

// A client-side error whose quote carries a Hop-by-Hop header ahead of
// the TCP header still finds the session and translates: the quoted
// transport header is looked for past the quote's extension headers, not
// right behind its IPv6 header.
static int
test_client_error_quoting_options_translated(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 22200000000000ull);
	TEST_ASSERT_SUCCESS(nat64s_test_warm_session(env), "warm-up failed");

	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_packet_too_big_with_options(
			env, CLIENT_PORT, &in
		),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"an error quoting a packet with extension headers must not be "
		"dropped"
	);
	TEST_ASSERT_NOT_NULL(out, "the error must be translated");
	// The quote leaves without its extension header, as IPv4.
	struct rte_mbuf *mbuf = packet_to_mbuf(out);
	struct rte_tcp_hdr *emb_tcp = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_tcp_hdr *,
		out->transport_header.offset + sizeof(struct rte_icmp_hdr) +
			sizeof(struct rte_ipv4_hdr)
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(emb_tcp->src_port),
		REMOTE_PORT,
		"the quoted source port must stay the remote's"
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(emb_tcp->dst_port),
		env->allocated_port,
		"the quoted destination port must be the allocated one"
	);
	rte_pktmbuf_free(mbuf);
	return TEST_SUCCESS;
}

// An error is translated only when it is addressed to the sender of the
// packet it quotes: one addressed anywhere else is dropped, whatever
// session its quote names.
static int
test_error_addressed_elsewhere_dropped(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 22400000000000ull);
	TEST_ASSERT_SUCCESS(nat64s_test_warm_session(env), "warm-up failed");

	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	size_t dst_off = offsetof(struct rte_ipv6_hdr, dst_addr);

	// A client-side error addressed outside every configured prefix.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_packet_too_big(env, CLIENT_PORT, &in),
		"build failed"
	);
	memcpy(rte_pktmbuf_mtod_offset(
		       packet_to_mbuf(in),
		       uint8_t *,
		       in->network_header.offset + dst_off
	       ),
	       client_addr,
	       16);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		dropped,
		"an error addressed outside the prefixes must be dropped"
	);

	// A client-side error addressed to another remote under the prefix.
	const uint8_t other_remote[4] = {192, 0, 2, 35};
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_packet_too_big(env, CLIENT_PORT, &in),
		"build failed"
	);
	memcpy(rte_pktmbuf_mtod_offset(
		       packet_to_mbuf(in),
		       uint8_t *,
		       in->network_header.offset + dst_off + 12
	       ),
	       other_remote,
	       4);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		dropped, "an error addressed to another remote must be dropped"
	);

	// A remote's error addressed to another address of the pool.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_frag_needed(env, env->allocated_port, &in),
		"build failed"
	);
	struct rte_ipv4_hdr *ipv4 = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(in),
		struct rte_ipv4_hdr *,
		in->network_header.offset
	);
	ipv4->dst_addr = other_pool_addr;
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		dropped,
		"an error addressed to another pool address must be dropped"
	);

	// The same remote's error, addressed to the session's public
	// address, is translated.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_frag_needed(env, env->allocated_port, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"the error addressed to the session must be translated"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));
	return TEST_SUCCESS;
}

// A SYN whose first segment ends inside its TCP header, the rest riding
// in a second segment, is dropped and leaves no session behind. The flag
// byte the module must not read sits in the head segment's tailroom, here
// deliberately holding a SYN.
static int
test_truncated_syn_leaves_no_session(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 23000000000000ull);
	const uint16_t port = 2012;

	struct packet *in = NULL;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	// The TCP header past its two ports moves into the second segment.
	TEST_ASSERT_SUCCESS(
		nat64s_test_split_tcp_header(env, in, 4), "split failed"
	);

	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		dropped,
		"a SYN whose flags lie outside the first segment must be "
		"dropped"
	);
	TEST_ASSERT_NULL(out, "a SYN cut inside its header must not translate");

	// No session survived it: the flow's next packet, a plain ACK, has
	// nothing to find and is refused like any TCP packet that did not
	// open with a SYN.
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		dropped,
		"a session must not survive a SYN whose flags could not be read"
	);
	return TEST_SUCCESS;
}

// A transport header that continues past the first segment is refused
// even when both its ports sit in it and no port is to be rewritten.
//
// The translation rewrites the header's checksum where it expects the
// header, in the first segment: such a packet of a live session would go
// out with the checksum written beside the header, in either direction.
static int
test_split_transport_header_refused(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 23200000000000ull);
	// A port inside the window stays the session's own.
	const uint16_t port = 2108;
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));
	uint64_t malformed = env->config->stats[NAT64S_MALFORMED];

	// The client's ACK.
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(
		nat64s_test_split_tcp_header(env, in, 4), "split failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(
		out, "a client packet whose header is split must not translate"
	);
	TEST_ASSERT(
		dropped, "a client packet whose header is split must be dropped"
	);

	// The remote's ACK, answered under the port the client kept.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, port, &in), "build failed"
	);
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(
		nat64s_test_split_tcp_header(env, in, 4), "split failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(
		out, "a return packet whose header is split must not translate"
	);
	TEST_ASSERT(
		dropped, "a return packet whose header is split must be dropped"
	);
	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_MALFORMED],
		malformed + 2,
		"the refused packets must be counted"
	);

	// Whole packets of the session still translate.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the session must survive the refused packets");
	rte_pktmbuf_free(packet_to_mbuf(out));
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the client's whole packet must still translate");
	rte_pktmbuf_free(packet_to_mbuf(out));
	return TEST_SUCCESS;
}

// The first fragment of a datagram is refused like the later ones.
//
// It carries the transport header a session would key on, but no session
// can serve the fragments that follow it: none is taken for it.
static int
test_first_fragment_refused(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 23400000000000ull);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2109, &in), "build failed");
	// The parser's mark of a fragmented datagram, on a fragment whose
	// offset is zero.
	in->flags |= 1 << PACKET_FLAG_FRAGMENTED;
	TEST_ASSERT_EQUAL(
		in->fragment_offset, 0, "the first fragment's offset is zero"
	);

	uint64_t refused = env->config->stats[NAT64S_FRAGMENTED];
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(out, "the first fragment must not translate");
	TEST_ASSERT(dropped, "the first fragment must be dropped");
	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_FRAGMENTED],
		refused + 1,
		"the refused fragment must be counted"
	);

	// Nothing was recorded for it: the datagram's whole SYN still opens
	// the session.
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2109, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the whole SYN must still open its session");
	rte_pktmbuf_free(packet_to_mbuf(out));
	return TEST_SUCCESS;
}

// A new session keeps the port its own wan entry already holds.
//
// The wan entry is written before the lan entry, and another worker may
// handle the same first packet: in both cases the session meets its own
// entry at the port it derived, and stays on that one port.
static int
test_own_wan_entry_keeps_its_port(struct nat64s_env *env) {
	const uint64_t now = 23600000000000ull;
	dataplane_ut_set_time_ns(env->ut, now);
	// A port inside the window is the session's on both sides.
	const uint16_t port = 2110;

	// The wan entry on its own, as a first packet whose lan insert
	// found no room leaves it.
	struct nat64state_wan_key wan_key = nat64s_test_wan_key(port);
	struct nat64state_value entry = {0};
	entry.cli_port = port;
	memcpy(entry.src6, client_addr, sizeof(entry.src6));
	memcpy(entry.prefix, nat64_prefix, sizeof(entry.prefix));
	fwtable_t *wan_table =
		fwstate_map_nat64_object_wan_table(&env->map->cp_object);
	rwlock_t *lock = NULL;
	TEST_ASSERT(
		fwtable_insert(
			wan_table,
			0,
			now,
			300ull * 1000000000ull,
			&wan_key,
			&entry,
			&lock
		) >= 0,
		"the session's wan entry must be recorded"
	);
	rwlock_write_unlock(lock);
	// The probe is to meet it.
	void *found = NULL;
	lock = NULL;
	bool from_stale = false;
	TEST_ASSERT(
		fwtable_lookup(
			wan_table, now, &wan_key, &found, &lock, &from_stale
		) >= 0,
		"the recorded wan entry must be found"
	);
	if (lock != NULL) {
		rwlock_read_unlock(lock);
	}

	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN must not be dropped");
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(tcp->src_port),
		port,
		"the session must keep the port its own wan entry holds"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// The lan entry was written as well: the client's next packet finds
	// the session.
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"the client's next packet must find the session it completed"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// And the remote's answer to the kept port reaches the client.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the answer to the kept port must be translated");
	return nat64s_test_assert_reply_to(out, client_addr, port);
}

// One client endpoint under two configured prefixes derives one external
// tuple towards one remote.
//
// The session under the second prefix moves off the first one's port
// while that lives, and each answer returns under its own session's
// prefix.
static int
test_second_prefix_moves_off_live_session(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 23700000000000ull);
	// A port inside the window is kept, so both sessions derive it.
	const uint16_t port = 2111;
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;

	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN under the first prefix must translate");
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(tcp->src_port),
		port,
		"the first session must keep its in-window port"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// The same client endpoint towards the same remote.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_to2(env, port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN under the second prefix must translate");
	tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	uint16_t moved = rte_be_to_cpu_16(tcp->src_port);
	TEST_ASSERT(
		moved != port,
		"the second session must move off the first one's port"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Each answer returns under its own session's prefix.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the first session's answer must be translated");
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_ipv6_hdr *,
		out->network_header.offset
	);
	TEST_ASSERT(
		memcmp(ipv6->src_addr, prefixes[0], NAT64S_PREFIX_LEN) == 0,
		"the first session's answer must return under the first prefix"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, moved, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the second session's answer must be translated");
	ipv6 = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_ipv6_hdr *,
		out->network_header.offset
	);
	TEST_ASSERT(
		memcmp(ipv6->src_addr, prefixes[1], NAT64S_PREFIX_LEN) == 0,
		"the second session's answer must return under the second "
		"prefix"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));
	return TEST_SUCCESS;
}

// Two source ports of one client whose derived external ports coincide.
//
// The second flow finds the first one's entry under the port it derived.
// The entry is another client port's, so the flow moves, and each answer
// reaches its own client port.
static int
test_derived_port_collision_between_client_ports_moves(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 23800000000000ull);
	const uint16_t first_port = 435;
	const uint16_t second_port = 514;
	const uint8_t other_remote[4] = {192, 0, 2, 99};
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;

	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn(env, first_port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the first flow's SYN must translate");
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	uint16_t derived = rte_be_to_cpu_16(tcp->src_port);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// The precondition, read off the second flow towards another remote:
	// both client ports derive one external port.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_to(env, second_port, other_remote, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"the second flow's SYN towards another remote must "
		"translate"
	);
	tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	TEST_ASSERT_EQUAL(
		rte_be_to_cpu_16(tcp->src_port),
		derived,
		"the two client ports must derive one external port"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Towards the first flow's remote, the derived port is that flow's.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn(env, second_port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the second flow's SYN must translate");
	tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	uint16_t moved = rte_be_to_cpu_16(tcp->src_port);
	TEST_ASSERT(
		moved != derived,
		"the second flow must move off the first one's derived port"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Each answer reaches its own client port.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, derived, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the first flow's answer must be translated");
	TEST_ASSERT_SUCCESS(
		nat64s_test_assert_reply_to(out, client_addr, first_port),
		"the first flow's answer must reach its own client port"
	);

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, moved, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the second flow's answer must be translated");
	return nat64s_test_assert_reply_to(out, client_addr, second_port);
}

// A packet the translation refuses takes no session and refreshes none.
//
// The translation refuses a Routing header of the deprecated type 0
// itself; one of another type it carries. A SYN with the refused header
// is dropped like any other refused packet, and nothing is recorded for
// it: the flow's next packet finds no session. A refused packet of a
// session that exists refreshes nothing either.
static int
test_refused_translation_takes_no_session(struct nat64s_env *env) {
	const uint64_t sec = 1000000000ull;
	const uint64_t now = 23900000000000ull;
	dataplane_ut_set_time_ns(env->ut, now);
	const uint16_t port = 2115;
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;

	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_with_routing(env, port, 0, &in),
		"build failed"
	);
	TEST_ASSERT_EQUAL(
		in->transport_header.type,
		IPPROTO_TCP,
		"the TCP header must be found past the routing header"
	);
	uint64_t translated = env->config->stats[NAT64S_TRANSLATED_V6_TO_V4];
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(
		out, "a SYN the translation refuses must not be forwarded"
	);
	TEST_ASSERT(dropped, "a SYN the translation refuses must be dropped");
	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_TRANSLATED_V6_TO_V4],
		translated,
		"a refused packet is no translation"
	);

	// Nothing was recorded for it: the flow's next packet, a plain ACK,
	// has nothing to find and is refused like any TCP packet that did
	// not open with a SYN.
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, port, &in), "build failed");
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(
		out,
		"a session must not survive a packet the translation refused"
	);
	TEST_ASSERT(dropped, "the ACK must be dropped for want of a session");

	// The control: the same SYN with a Routing header of another type is
	// carried, so the drop above was the type's.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_with_routing(env, port, 2, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"a SYN with a Routing header of another type must be carried"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// A refused packet of an existing session does not keep it alive:
	// past the general timeout without a translatable packet, the
	// session is gone.
	const uint16_t live_port = 2116;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn(env, live_port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the live flow's SYN must open its session");
	rte_pktmbuf_free(packet_to_mbuf(out));

	dataplane_ut_set_time_ns(env->ut, now + 250 * sec);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_with_routing(env, live_port, 0, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(
		out, "a refused packet of a live session must not be forwarded"
	);

	dataplane_ut_set_time_ns(env->ut, now + 400 * sec);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn(env, live_port, &in), "build failed"
	);
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(out, "a refused packet must not keep a session alive");
	TEST_ASSERT(dropped, "the session must have aged out unrefreshed");
	return TEST_SUCCESS;
}

// A new flow finding every candidate port taken towards its remote gives
// up instead of sharing one: its SYN is dropped, and the sessions holding
// the ports keep answering their own clients.
static int
test_exhausted_window_drops_new_flow(struct nat64s_env *env) {
	// Four ports, so four clients fill the window completely.
	const struct nat64s_env_settings narrow = {
		.port_lo = 2000,
		.port_hi = 2004,
		.timeouts = {.timeout = 300, .tcp_established = 300},
	};
	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &narrow), "reconfigure failed"
	);
	dataplane_ut_set_time_ns(env->ut, 24000000000000ull);

	// Each client's in-window source port is kept, so the four of them
	// hold the whole window towards the test remote.
	uint16_t held[4];
	for (size_t idx = 0; idx < 4; ++idx) {
		uint8_t client6[16];
		nat64s_test_other_client(client6, (uint8_t)(0x21 + idx));
		const uint16_t client_port = (uint16_t)(2000 + idx);

		struct packet *in = NULL;
		struct packet *out = NULL;
		bool dropped = false;
		TEST_ASSERT_SUCCESS(
			nat64s_make_v6_syn_from(
				env, client6, client_port, remote4, &in
			),
			"build failed"
		);
		TEST_ASSERT_SUCCESS(
			nat64s_run(env, in, &out, &dropped), "run failed"
		);
		TEST_ASSERT(
			!dropped, "an in-window client must not be dropped"
		);
		TEST_ASSERT_NOT_NULL(
			out, "an in-window client must be translated"
		);
		struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(out),
			struct rte_tcp_hdr *,
			out->transport_header.offset
		);
		held[idx] = rte_be_to_cpu_16(tcp->src_port);
		TEST_ASSERT_EQUAL(
			held[idx],
			client_port,
			"an in-window source port must be kept"
		);
		rte_pktmbuf_free(packet_to_mbuf(out));
	}

	// A fifth client derives one of the four ports as well: all of them
	// are taken.
	uint8_t fifth_client[16];
	nat64s_test_other_client(fifth_client, 0x31);
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_from(env, fifth_client, 3000, remote4, &in),
		"build failed"
	);
	uint64_t failed_before = env->config->stats[NAT64S_INSERT_FAILED];
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		dropped,
		"a flow with no free port towards its remote must be dropped"
	);
	TEST_ASSERT_NULL(out, "an unplaceable flow must not be translated");
	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_INSERT_FAILED],
		failed_before + 1,
		"the insert-failed counter must count the unplaceable flow"
	);

	for (size_t idx = 0; idx < 4; ++idx) {
		uint8_t client6[16];
		nat64s_test_other_client(client6, (uint8_t)(0x21 + idx));

		TEST_ASSERT_SUCCESS(
			nat64s_make_v4_reply(env, held[idx], &in),
			"build failed"
		);
		TEST_ASSERT_SUCCESS(
			nat64s_run(env, in, &out, &dropped), "run failed"
		);
		TEST_ASSERT(!dropped, "a held port must keep answering");
		TEST_ASSERT_SUCCESS(
			nat64s_test_assert_reply_to(
				out, client6, (uint16_t)(2000 + idx)
			),
			"a held port must keep answering its own client"
		);
	}

	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &nat64s_suite_settings),
		"restoring the suite's settings failed"
	);
	return TEST_SUCCESS;
}

// A full session table refuses new flows without wedging their buckets:
// the insert that finds no room releases its bucket, so the same flow is
// refused again instead of hanging its worker on it.
static int
test_full_table_refuses_without_wedging(struct nat64s_env *env) {
	dataplane_ut_set_time_ns(env->ut, 24500000000000ull);

	// In-window source ports are kept, so every flow takes a port of its
	// own and none is refused for a taken tuple.
	const uint64_t refused_before =
		env->config->stats[NAT64S_INSERT_FAILED];
	uint64_t refused = refused_before;
	uint16_t port = 1024;
	size_t opened = 0;
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;
	while (refused == refused_before && opened < 3000) {
		TEST_ASSERT_SUCCESS(
			nat64s_make_v6_syn(env, port, &in), "build failed"
		);
		TEST_ASSERT_SUCCESS(
			nat64s_run(env, in, &out, &dropped), "run failed"
		);
		refused = env->config->stats[NAT64S_INSERT_FAILED];
		if (refused == refused_before) {
			TEST_ASSERT(
				!dropped,
				"a flow must be opened while the table has room"
			);
			rte_pktmbuf_free(packet_to_mbuf(out));
		}
		++opened;
		++port;
	}
	TEST_ASSERT_EQUAL(
		refused, refused_before + 1, "the session table must fill up"
	);
	// The scenarios before this one keep the key slots of their expired
	// sessions, so a little less than a whole index of flows fits.
	TEST_ASSERT(
		opened >= 900,
		"nearly an index of flows must fit before one is refused: %zu",
		opened
	);
	TEST_ASSERT(
		dropped, "a flow beyond the table's capacity must be refused"
	);

	// The refused flow hashes to the bucket of the failed insert: it is
	// refused again, not stuck on a lock that insert left held.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn(env, port - 1, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_INSERT_FAILED],
		refused_before + 2,
		"the refused flow must be refused again"
	);
	TEST_ASSERT(dropped, "the refused flow must be dropped again");

	// A flow opened before the table filled still translates.
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 1024, &in), "build failed");
	nat64s_test_set_tcp_flags(in, RTE_TCP_ACK_FLAG);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"a flow opened before the table filled must still translate"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));
	return TEST_SUCCESS;
}

// Timeouts left unset reach the dataplane unset, and each resolves to its
// own default there: a TCP session the remote has answered lives for
// hours, one that it has not lives for a minute.
static int
test_default_timeouts_follow_each_knob(struct nat64s_env *env) {
	const struct nat64s_env_settings defaults = {
		.port_lo = 1024,
		.port_hi = 4096,
		.timeouts = {.timeout = 0, .tcp_established = 0},
	};
	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &defaults), "reconfigure failed"
	);

	const uint64_t base = 25000000000000ull;
	const uint64_t sec = 1000000000ull;
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;

	// Flow A is opened and answered with a SYN-ACK; flow B only opens.
	dataplane_ut_set_time_ns(env->ut, base);
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2013, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the established flow's SYN must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the established flow's SYN must translate");
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	uint16_t established_port = rte_be_to_cpu_16(tcp->src_port);
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, established_port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN-ACK must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2014, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped, "the unestablished flow's SYN must not be dropped"
	);
	TEST_ASSERT_NOT_NULL(
		out, "the unestablished flow's SYN must translate"
	);
	tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	uint16_t unestablished_port = rte_be_to_cpu_16(tcp->src_port);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// A minute in, the unestablished flow is gone.
	dataplane_ut_set_time_ns(env->ut, base + 61 * sec);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, unestablished_port, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT_NULL(
		out, "an unestablished flow must expire after a minute"
	);
	TEST_ASSERT(dropped, "an unestablished flow's reply must be dropped");

	// Two minutes in, the established one still answers.
	dataplane_ut_set_time_ns(env->ut, base + 120 * sec);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, established_port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"an established flow must outlive the unestablished default"
	);

	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &nat64s_suite_settings),
		"restoring the suite's settings failed"
	);
	return TEST_SUCCESS;
}

// Move a built packet's destination under the Well-Known Prefix.
static void
nat64s_test_under_well_known_prefix(struct packet *packet) {
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(packet),
		struct rte_ipv6_hdr *,
		packet->network_header.offset
	);
	memcpy(ipv6->dst_addr, well_known_prefixes[0], 12);
}

// Under the Well-Known Prefix only global IPv4 addresses cross (RFC
// 6052 3.1).
//
// A source or destination standing for an address of a block no global
// traffic uses is refused and counted, whatever the configuration
// carries. A global address translates as under any other prefix, and a
// network-specific prefix still carries a documentation address.
static int
test_well_known_prefix_refuses_non_global(struct nat64s_env *env) {
	const struct nat64s_env_settings well_known = {
		.port_lo = 1024,
		.port_hi = 4096,
		.timeouts = {.timeout = 300, .tcp_established = 300},
		.well_known = true,
	};
	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &well_known), "reconfigure failed"
	);
	dataplane_ut_set_time_ns(env->ut, 25600000000000ull);

	const uint8_t private_remote[4] = {10, 1, 2, 3};
	const uint8_t public_remote[4] = {8, 8, 8, 8};
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;

	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_to(env, 2021, private_remote, &in),
		"build failed"
	);
	nat64s_test_under_well_known_prefix(in);
	uint64_t refused = env->config->stats[NAT64S_NON_GLOBAL];
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		dropped,
		"a private address under the Well-Known Prefix must be refused"
	);
	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_NON_GLOBAL],
		refused + 1,
		"the refused destination must be counted"
	);

	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_to(env, 2022, public_remote, &in),
		"build failed"
	);
	nat64s_test_under_well_known_prefix(in);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"a global address under the Well-Known Prefix must translate"
	);
	TEST_ASSERT_NOT_NULL(
		out, "the global destination's SYN must translate"
	);
	struct rte_ipv4_hdr *ipv4 = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_ipv4_hdr *,
		out->network_header.offset
	);
	uint32_t public_addr = 0;
	memcpy(&public_addr, public_remote, sizeof(public_addr));
	TEST_ASSERT_EQUAL(
		ipv4->dst_addr,
		public_addr,
		"the translated destination must be the public remote"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// The anycast addresses of a reserved block are global all the same.
	const uint8_t anycast_remote[4] = {192, 0, 0, 10};
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_to(env, 2025, anycast_remote, &in),
		"build failed"
	);
	nat64s_test_under_well_known_prefix(in);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"a global anycast address of a reserved block must translate"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// A network-specific prefix beside it carries a documentation
	// address as before.
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_to2(env, 2026, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"a documentation address under a network-specific prefix must "
		"translate"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &nat64s_suite_settings),
		"restoring the suite's settings failed"
	);

	// A source is held to the rule although no config carries the
	// Well-Known Prefix.
	uint8_t hidden_source[16];
	memcpy(hidden_source, well_known_prefixes[0], NAT64S_PREFIX_LEN);
	const uint8_t private_source[4] = {10, 9, 8, 7};
	memcpy(hidden_source + NAT64S_PREFIX_LEN,
	       private_source,
	       sizeof(private_source));
	refused = env->config->stats[NAT64S_NON_GLOBAL];
	TEST_ASSERT_SUCCESS(
		nat64s_make_v6_syn_from(env, hidden_source, 2027, remote4, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		dropped,
		"a source around a private address under the Well-Known Prefix "
		"must be refused"
	);
	TEST_ASSERT_EQUAL(
		env->config->stats[NAT64S_NON_GLOBAL],
		refused + 1,
		"the refused source must be counted"
	);
	return TEST_SUCCESS;
}

// The largest timeout a knob represents reaches the session whole.
//
// One configured with it still answers half a second short of it, on
// either knob.
static int
test_largest_timeout_kept_whole(struct nat64s_env *env) {
	const struct nat64s_env_settings largest = {
		.port_lo = 1024,
		.port_hi = 4096,
		.timeouts = {.timeout = 65535, .tcp_established = 65535},
	};
	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &largest), "reconfigure failed"
	);

	const uint64_t base = 26000000000000ull;
	const uint64_t sec = 1000000000ull;
	struct packet *in = NULL;
	struct packet *out = NULL;
	bool dropped = false;

	// Flow A is opened and answered with a SYN-ACK; flow B only opens.
	dataplane_ut_set_time_ns(env->ut, base);
	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2017, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the established flow's SYN must not be dropped");
	TEST_ASSERT_NOT_NULL(out, "the established flow's SYN must translate");
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	uint16_t established_port = rte_be_to_cpu_16(tcp->src_port);
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, established_port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(!dropped, "the SYN-ACK must not be dropped");
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(nat64s_make_v6_syn(env, 2018, &in), "build failed");
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped, "the unestablished flow's SYN must not be dropped"
	);
	TEST_ASSERT_NOT_NULL(
		out, "the unestablished flow's SYN must translate"
	);
	tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(out),
		struct rte_tcp_hdr *,
		out->transport_header.offset
	);
	uint16_t unestablished_port = rte_be_to_cpu_16(tcp->src_port);
	rte_pktmbuf_free(packet_to_mbuf(out));

	// Half a second short of the largest timeout, both still answer.
	dataplane_ut_set_time_ns(env->ut, base + 65534 * sec + sec / 2);
	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, established_port, &in), "build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"an established session must live for the largest timeout"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_make_v4_reply(env, unestablished_port, &in),
		"build failed"
	);
	TEST_ASSERT_SUCCESS(nat64s_run(env, in, &out, &dropped), "run failed");
	TEST_ASSERT(
		!dropped,
		"an unestablished session must live for the largest timeout"
	);
	rte_pktmbuf_free(packet_to_mbuf(out));

	TEST_ASSERT_SUCCESS(
		nat64s_env_reconfigure(env, &nat64s_suite_settings),
		"restoring the suite's settings failed"
	);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	struct nat64s_env env;
	if (nat64s_env_init(&env, &nat64s_suite_settings) != TEST_SUCCESS) {
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
		res = test_external_identity_derives_from_client_endpoint(&env);
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
	if (res == TEST_SUCCESS) {
		res = test_client_ack_keeps_init_lifetime(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_remote_reset_does_not_establish(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_latched_wan_entry_holds_client_refresh(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_latched_lan_entry_holds_return_refresh(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_refused_return_packet_refreshes_nothing(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_return_traffic_keeps_lan_entry(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_echo_identifier_outside_window_round_trips(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_echo_on_taken_identifier_moves_whole_key(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_echo_probe_skips_identifiers_others_hold(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_echo_identifier_zero_round_trips(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_unsupported_icmp_type_takes_no_session(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_taken_tuple_moves_to_free_port(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_taken_top_port_wraps_to_window_start(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_default_window_ends_with_last_port(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_ipv6_mtu_above_carried_is_held_down(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_hairpinned_error_dropped(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_client_error_quoting_options_translated(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_error_addressed_elsewhere_dropped(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_truncated_syn_leaves_no_session(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_split_transport_header_refused(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_first_fragment_refused(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_own_wan_entry_keeps_its_port(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_second_prefix_moves_off_live_session(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_derived_port_collision_between_client_ports_moves(
			&env
		);
	}
	if (res == TEST_SUCCESS) {
		res = test_refused_translation_takes_no_session(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_exhausted_window_drops_new_flow(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_full_table_refuses_without_wedging(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_default_timeouts_follow_each_knob(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_well_known_prefix_refuses_non_global(&env);
	}
	if (res == TEST_SUCCESS) {
		res = test_largest_timeout_kept_whole(&env);
	}

	nat64s_env_fini(&env);
	return (res == TEST_SUCCESS) ? 0 : 1;
}
