// Runs the Rust vxlan device inside the in-process dataplane harness.
//
// The harness resolves the device constructor from this binary through the
// same dlopen/dlsym loader the dataplane uses, so the test covers the
// builtin link path end to end: the exported Rust constructor, the device
// the Rust control-plane api builds and validates through its C ABI, and
// both Rust handlers running on a real packet front. The port hands every
// packet to the vxlan input, whose pipeline hands it to the vxlan output, so
// one round runs both.

#include <errno.h>
#include <stdlib.h>
#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_udp.h>

#include "api/agent.h"
#include "common/memory_address.h"
#include "common/strutils.h"
#include "common/test_assert.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_device.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/logging/log.h"
#include "lib/rust/cp/yanet-cp-sys/shim/yanet_cp_shim.h"
#include "modules/forward/api/controlplane.h"
#include "yanet_cp.h"

#define VXLAN_UDP_PORT 4789
#define VXLAN_VNI_MAX ((UINT32_C(1) << 24) - 1)
#define VXLAN_HDR_LEN 8
#define ENCAP_LEN                                                              \
	(sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr) +          \
	 sizeof(struct rte_udp_hdr) + VXLAN_HDR_LEN)

static const struct yanet_cp_vxlan_tunnel test_vxlan = {
	.local_mac = {0x02, 0, 0, 0, 0, 0x01},
	.remote_mac = {0x02, 0, 0, 0, 0, 0x02},
	.local_ip = {192, 0, 2, 1},
	.remote_ip = {198, 51, 100, 7},
	.vni = 0x1234,
};

// Creates a vxlan device through the Rust control-plane api.
static void *
vxlan_device_new(
	struct agent *agent,
	const char *name,
	const struct yanet_cp_vxlan_tunnel *tunnel,
	char *err
) {
	struct yanet_cp_pipeline input = {
		.name = "to_vxlan_output", .weight = 1
	};
	struct yanet_cp_pipeline output = {.name = "empty", .weight = 1};
	struct yanet_cp_vxlan_device_request request = {
		.name = name,
		.tunnel = *tunnel,
		.input = &input,
		.input_len = 1,
		.output = &output,
		.output_len = 1,
	};
	void *device = NULL;
	if (yanet_cp_vxlan_device_new(
		    agent, &request, &device, err, YANET_CP_ERROR_LEN
	    ) != YANET_CP_OK) {
		return NULL;
	}
	return device;
}

// A harness with one port and one vxlan device.
struct vxlan_fixture {
	struct dataplane_ut *ut;
	struct agent *agent;
};

// Ethernet/IPv4/UDP frame used as the tunnel payload; returns its length.
static size_t
write_inner_frame(uint8_t *data) {
	static const uint8_t frame[] = {
		0x02, 0,    0,	  0,  0, 0x0a, 0x02, 0,	  0,   0,   0,	  0x0b,
		0x08, 0x00, 0x45, 0,  0, 33,   0,    0,	  0,   0,   64,	  17,
		0,    0,    10,	  0,  0, 1,    10,   0,	  0,   2,   0x30, 0x39,
		0x00, 0x35, 0,	  13, 0, 0,    'h',  'e', 'l', 'l', 'o',
	};
	memcpy(data, frame, sizeof(frame));
	return sizeof(frame);
}

// Creates a forward module instance sending every packet to the vxlan
// device entry of the given direction.
static struct cp_module *
forward_to_vxlan(struct agent *agent, const char *name, uint8_t mode) {
	yanet_error *err = NULL;
	struct cp_module *forward =
		forward_module_config_init(agent, name, &err);
	if (forward == NULL) {
		yanet_error_free(err);
		return NULL;
	}
	struct forward_rule rule;
	memset(&rule, 0, sizeof(rule));
	strtcpy(rule.target, "vx0", sizeof(rule.target));
	strtcpy(rule.counter, name, sizeof(rule.counter));
	rule.mode = mode;
	if (forward_module_config_update(forward, &rule, 1, &err)) {
		yanet_error_free(err);
		return NULL;
	}
	return forward;
}

// Registers a function and a single-function pipeline running one forward
// module instance, both named after the instance.
static int
add_forward_pipeline(struct agent *agent, const char *name) {
	yanet_error *err = NULL;
	const char *chain_types[] = {"forward"};
	const char *chain_names[] = {name};
	struct cp_chain_config *chain =
		cp_chain_config_create(name, 1, chain_types, chain_names);
	TEST_ASSERT_NOT_NULL(chain, "cp_chain_config_create failed");
	struct cp_function_config *function =
		cp_function_config_create(name, 1);
	TEST_ASSERT_NOT_NULL(function, "cp_function_config_create failed");
	TEST_ASSERT_SUCCESS(
		cp_function_config_set_chain(function, 0, chain, 1),
		"cp_function_config_set_chain failed"
	);
	struct cp_function_config *functions[] = {function};
	TEST_ASSERT_SUCCESS(
		agent_update_functions(agent, 1, functions, &err),
		"function update failed: %s",
		err ? yanet_error_format(err) : "?"
	);
	cp_function_config_free(function);

	struct cp_pipeline_config *pipeline =
		cp_pipeline_config_create(name, 1);
	TEST_ASSERT_NOT_NULL(pipeline, "pipeline allocation failed");
	TEST_ASSERT_SUCCESS(
		cp_pipeline_config_set_function(pipeline, 0, name),
		"pipeline setup failed"
	);
	struct cp_pipeline_config *pipelines[] = {pipeline};
	TEST_ASSERT_SUCCESS(
		agent_update_pipelines(agent, 1, pipelines, &err),
		"pipeline update failed: %s",
		err ? yanet_error_format(err) : "?"
	);
	cp_pipeline_config_free(pipeline);
	return TEST_SUCCESS;
}

// Builds the topology: port0 input hands every packet to the vxlan input,
// whose pipeline hands it to the vxlan output; both outputs run an empty
// pipeline, so a surviving packet leaves the round as output.
static int
fixture_setup(struct vxlan_fixture *fixture) {
	static const char *port_names[] = {"port0"};
	static const char *module_names[] = {"forward"};
	// The initial generation gives every port a plain device.
	static const char *devs_to_load[] = {"plain", "vxlan"};
	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = module_names,
		.module_count = 1,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 2,
	};
	fixture->ut = dataplane_ut_new(&cfg);
	TEST_ASSERT_NOT_NULL(fixture->ut, "dataplane_ut_new returned NULL");

	yanet_error *err = NULL;
	struct agent *agent = agent_attach(
		dataplane_ut_shm(fixture->ut), 0, "vxlan-test", 8u << 20, &err
	);
	fixture->agent = agent;
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_module *modules[] = {
		forward_to_vxlan(agent, "to_vxlan_input", FORWARD_MODE_IN),
		forward_to_vxlan(agent, "to_vxlan_output", FORWARD_MODE_OUT),
	};
	TEST_ASSERT(
		modules[0] != NULL && modules[1] != NULL,
		"forward module setup failed"
	);
	TEST_ASSERT_SUCCESS(
		agent_update_modules(agent, 2, modules, &err),
		"module update failed: %s",
		err ? yanet_error_format(err) : "?"
	);
	TEST_ASSERT_SUCCESS(
		add_forward_pipeline(agent, "to_vxlan_input"),
		"port input pipeline setup failed"
	);
	TEST_ASSERT_SUCCESS(
		add_forward_pipeline(agent, "to_vxlan_output"),
		"vxlan input pipeline setup failed"
	);
	struct cp_pipeline_config *empty =
		cp_pipeline_config_create("empty", 0);
	TEST_ASSERT_NOT_NULL(empty, "empty pipeline allocation failed");
	struct cp_pipeline_config *pipelines[] = {empty};
	TEST_ASSERT_SUCCESS(
		agent_update_pipelines(agent, 1, pipelines, &err),
		"pipeline update failed: %s",
		err ? yanet_error_format(err) : "?"
	);
	cp_pipeline_config_free(empty);

	struct cp_device_plain_config *port_config =
		cp_device_plain_config_new("port0", 1, 1, &err);
	TEST_ASSERT_NOT_NULL(port_config, "cp_device_plain_config_new failed");
	TEST_ASSERT_SUCCESS(
		cp_device_plain_config_set_input_pipeline(
			port_config, 0, "to_vxlan_input", 1
		),
		"port input pipeline binding failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_plain_config_set_output_pipeline(
			port_config, 0, "empty", 1
		),
		"port output pipeline binding failed"
	);
	struct cp_device *port = cp_device_plain_new(agent, port_config, &err);
	cp_device_plain_config_free(port_config);
	TEST_ASSERT_NOT_NULL(port, "cp_device_plain_new failed");

	char cp_err[YANET_CP_ERROR_LEN];
	struct cp_device *vxlan =
		vxlan_device_new(agent, "vx0", &test_vxlan, cp_err);
	TEST_ASSERT_NOT_NULL(vxlan, "vxlan device creation failed: %s", cp_err);

	struct cp_device *devices[] = {port, vxlan};
	TEST_ASSERT_SUCCESS(
		agent_update_devices(agent, 2, devices, &err),
		"device update failed: %s",
		err ? yanet_error_format(err) : "?"
	);
	// Both stay dangling until the agent detaches; the EAGAIN error chains
	// must be freed.
	yanet_error *port_err = NULL;
	cp_device_plain_free(port, &port_err);
	yanet_error_free(port_err);
	TEST_ASSERT_EQUAL(
		yanet_cp_vxlan_device_free(vxlan, cp_err, sizeof(cp_err)),
		YANET_CP_STILL_REFERENCED,
		"a published device must stay alive"
	);

	return TEST_SUCCESS;
}

static void
fixture_teardown(struct vxlan_fixture *fixture) {
	agent_detach(fixture->agent);
	dataplane_ut_free(fixture->ut);
}

// Allocates a packet holding the inner frame, VXLAN-encapsulated by the
// remote endpoint with the given VNI when vni is not negative.
static struct packet *
make_packet(struct dataplane_ut *ut, int64_t vni) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(ut);
	if (mbuf == NULL) {
		return NULL;
	}
	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;

	uint8_t inner[128];
	size_t inner_len = write_inner_frame(inner);
	size_t outer_len = vni < 0 ? 0 : ENCAP_LEN;
	uint8_t *data =
		(uint8_t *)rte_pktmbuf_append(mbuf, outer_len + inner_len);
	if (data == NULL) {
		rte_pktmbuf_free(mbuf);
		return NULL;
	}
	memset(data, 0, outer_len);
	memcpy(data + outer_len, inner, inner_len);

	if (vni >= 0) {
		struct rte_ether_hdr *ether = (struct rte_ether_hdr *)data;
		memcpy(ether->dst_addr.addr_bytes, test_vxlan.local_mac, 6);
		memcpy(ether->src_addr.addr_bytes, test_vxlan.remote_mac, 6);
		ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
		struct rte_ipv4_hdr *ipv4 = (struct rte_ipv4_hdr *)(ether + 1);
		ipv4->version_ihl = RTE_IPV4_VHL_DEF;
		ipv4->time_to_live = 64;
		ipv4->next_proto_id = IPPROTO_UDP;
		ipv4->total_length = rte_cpu_to_be_16(
			outer_len - sizeof(struct rte_ether_hdr) + inner_len
		);
		memcpy(&ipv4->src_addr, test_vxlan.remote_ip, 4);
		memcpy(&ipv4->dst_addr, test_vxlan.local_ip, 4);
		ipv4->hdr_checksum = rte_ipv4_cksum(ipv4);
		struct rte_udp_hdr *udp = (struct rte_udp_hdr *)(ipv4 + 1);
		udp->src_port = rte_cpu_to_be_16(50000);
		udp->dst_port = rte_cpu_to_be_16(VXLAN_UDP_PORT);
		udp->dgram_len = rte_cpu_to_be_16(
			sizeof(struct rte_udp_hdr) + VXLAN_HDR_LEN + inner_len
		);
		uint8_t *vxlan = (uint8_t *)(udp + 1);
		vxlan[0] = 0x08;
		vxlan[4] = (uint8_t)(vni >> 16);
		vxlan[5] = (uint8_t)(vni >> 8);
		vxlan[6] = (uint8_t)vni;
	}

	if (parse_packet(packet)) {
		rte_pktmbuf_free(mbuf);
		return NULL;
	}
	return packet;
}

// Runs one round with a single packet and reports where it ended.
static void
run_one(struct dataplane_ut *ut,
	struct packet *packet,
	struct dataplane_ut_round_result *result) {
	struct packet_list input;
	packet_list_init(&input);
	packet_list_add(&input, packet);
	dataplane_ut_run(ut, 0, &input, result);
}

// Checks that a frame is the inner frame encapsulated with the test config.
static int
check_encapsulated(struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint8_t inner[128];
	size_t inner_len = write_inner_frame(inner);
	TEST_ASSERT_EQUAL(
		(long)rte_pktmbuf_pkt_len(mbuf),
		(long)(ENCAP_LEN + inner_len),
		"output must be the inner frame plus the outer headers"
	);
	uint8_t *data = rte_pktmbuf_mtod(mbuf, uint8_t *);

	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)data;
	TEST_ASSERT(
		memcmp(ether->dst_addr.addr_bytes, test_vxlan.remote_mac, 6) ==
			0,
		"outer destination MAC must be the remote endpoint"
	);
	TEST_ASSERT(
		memcmp(ether->src_addr.addr_bytes, test_vxlan.local_mac, 6) ==
			0,
		"outer source MAC must be the local endpoint"
	);
	struct rte_ipv4_hdr *ipv4 = (struct rte_ipv4_hdr *)(ether + 1);
	TEST_ASSERT(
		memcmp(&ipv4->src_addr, test_vxlan.local_ip, 4) == 0 &&
			memcmp(&ipv4->dst_addr, test_vxlan.remote_ip, 4) == 0,
		"outer addresses must run from the local to the remote endpoint"
	);
	TEST_ASSERT_EQUAL(
		(long)rte_raw_cksum(ipv4, sizeof(*ipv4)),
		0xffffL,
		"outer IPv4 checksum must verify"
	);
	struct rte_udp_hdr *udp = (struct rte_udp_hdr *)(ipv4 + 1);
	TEST_ASSERT_EQUAL(
		(long)rte_be_to_cpu_16(udp->dst_port),
		(long)VXLAN_UDP_PORT,
		"outer UDP destination must be the VXLAN port"
	);
	TEST_ASSERT(
		rte_be_to_cpu_16(udp->src_port) >= 49152,
		"outer UDP source must be in the dynamic range"
	);
	uint8_t *vxlan = (uint8_t *)(udp + 1);
	uint32_t vni = (uint32_t)vxlan[4] << 16 | (uint32_t)vxlan[5] << 8 |
		       (uint32_t)vxlan[6];
	TEST_ASSERT_EQUAL(
		(long)vni, (long)test_vxlan.vni, "VNI must be the device VNI"
	);
	TEST_ASSERT(
		memcmp(data + ENCAP_LEN, inner, inner_len) == 0,
		"inner frame must be carried unchanged"
	);
	return TEST_SUCCESS;
}

// Verifies that the loader resolved the Rust constructor and registered
// the device type with both handlers.
static int
run_vxlan_device_loaded_test(void) {
	struct vxlan_fixture fixture;
	TEST_ASSERT_SUCCESS(fixture_setup(&fixture), "fixture setup failed");

	struct dp_config *dp_config =
		yanet_shm_dp_config(dataplane_ut_shm(fixture.ut), 0);
	struct dp_device *dp_devices = ADDR_OF(&dp_config->dp_devices);
	struct dp_device *vxlan = NULL;
	for (uint64_t idx = 0; idx < dp_config->device_count; ++idx) {
		if (strcmp(dp_devices[idx].name, "vxlan") == 0) {
			vxlan = dp_devices + idx;
		}
	}
	TEST_ASSERT_NOT_NULL(vxlan, "the vxlan device type must be loaded");
	TEST_ASSERT(
		vxlan->input_handler != NULL && vxlan->output_handler != NULL &&
			vxlan->commit_handler != NULL,
		"the Rust constructor must provide every handler"
	);

	fixture_teardown(&fixture);
	return TEST_SUCCESS;
}

// Verifies that a tunnel packet with the device VNI is decapsulated on
// input and the inner frame encapsulated again on output.
static int
run_vxlan_decap_reencap_test(void) {
	struct vxlan_fixture fixture;
	TEST_ASSERT_SUCCESS(fixture_setup(&fixture), "fixture setup failed");

	struct packet *packet = make_packet(fixture.ut, test_vxlan.vni);
	TEST_ASSERT_NOT_NULL(packet, "packet allocation failed");
	struct dataplane_ut_round_result result;
	run_one(fixture.ut, packet, &result);

	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&result.drop), 0L, "nothing drops"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&result.output),
		1L,
		"the packet reaches the output"
	);
	int rc = check_encapsulated(result.output.first);

	dataplane_ut_round_result_free(&result);
	fixture_teardown(&fixture);
	return rc;
}

// Verifies that a tunnel packet with another VNI is dropped on input.
static int
run_vxlan_vni_mismatch_drop_test(void) {
	struct vxlan_fixture fixture;
	TEST_ASSERT_SUCCESS(fixture_setup(&fixture), "fixture setup failed");

	struct packet *packet = make_packet(fixture.ut, test_vxlan.vni + 1);
	TEST_ASSERT_NOT_NULL(packet, "packet allocation failed");
	struct dataplane_ut_round_result result;
	run_one(fixture.ut, packet, &result);

	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&result.output),
		0L,
		"the packet must not reach the output"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&result.drop), 1L, "the packet drops"
	);
	TEST_ASSERT_EQUAL(
		(long)rte_pktmbuf_pkt_len(packet_to_mbuf(result.drop.first)),
		(long)(ENCAP_LEN + 47),
		"a dropped tunnel packet keeps its outer headers"
	);

	dataplane_ut_round_result_free(&result);
	fixture_teardown(&fixture);
	return TEST_SUCCESS;
}

// Verifies that non-tunnel traffic passes input unchanged and is
// encapsulated on output.
static int
run_vxlan_plain_frame_encap_test(void) {
	struct vxlan_fixture fixture;
	TEST_ASSERT_SUCCESS(fixture_setup(&fixture), "fixture setup failed");

	struct packet *packet = make_packet(fixture.ut, -1);
	TEST_ASSERT_NOT_NULL(packet, "packet allocation failed");
	struct dataplane_ut_round_result result;
	run_one(fixture.ut, packet, &result);

	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&result.drop), 0L, "nothing drops"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&result.output),
		1L,
		"the packet reaches the output"
	);
	int rc = check_encapsulated(result.output.first);

	dataplane_ut_round_result_free(&result);
	fixture_teardown(&fixture);
	return rc;
}

// Verifies that the Rust api refuses a VNI wider than 24 bits.
static int
run_vxlan_api_rejects_wide_vni_test(void) {
	struct vxlan_fixture fixture;
	TEST_ASSERT_SUCCESS(fixture_setup(&fixture), "fixture setup failed");

	struct yanet_cp_vxlan_tunnel wide = test_vxlan;
	wide.vni = VXLAN_VNI_MAX + 1;
	char err[YANET_CP_ERROR_LEN];
	void *device = vxlan_device_new(fixture.agent, "vx1", &wide, err);
	TEST_ASSERT(device == NULL, "a 25-bit VNI must be refused");
	TEST_ASSERT_STR_CONTAINS(err, "vni", "error names the field");

	fixture_teardown(&fixture);
	return TEST_SUCCESS;
}

// Verifies that the Rust api reads back the tunnel of a published device.
static int
run_vxlan_api_shows_published_tunnel_test(void) {
	struct vxlan_fixture fixture;
	TEST_ASSERT_SUCCESS(fixture_setup(&fixture), "fixture setup failed");

	struct yanet_cp_vxlan_tunnel shown;
	memset(&shown, 0, sizeof(shown));
	char err[YANET_CP_ERROR_LEN];
	TEST_ASSERT_EQUAL(
		yanet_cp_vxlan_device_show(
			fixture.agent, "vx0", &shown, err, sizeof(err)
		),
		YANET_CP_OK,
		"show failed: %s",
		err
	);
	TEST_ASSERT(
		memcmp(&shown, &test_vxlan, sizeof(shown)) == 0,
		"show must return the created tunnel"
	);
	TEST_ASSERT_EQUAL(
		yanet_cp_vxlan_device_show(
			fixture.agent, "nope", &shown, err, sizeof(err)
		),
		YANET_CP_NOT_FOUND,
		"an unknown name is not found"
	);
	uint8_t bytes[sizeof(shown)];
	TEST_ASSERT_EQUAL(
		yanet_cp_shim_device_read(
			fixture.agent,
			"vxlan",
			"vx0",
			0,
			0,
			bytes,
			sizeof(bytes)
		),
		2,
		"a read for another configuration layout is refused"
	);

	fixture_teardown(&fixture);
	return TEST_SUCCESS;
}

// Verifies that a vxlan device cannot be created for another configuration
// layout: the C device init without a layout expects the zero layout of a C
// device, which the Rust device type was not loaded with.
static int
run_vxlan_layout_mismatch_refused_test(void) {
	struct vxlan_fixture fixture;
	TEST_ASSERT_SUCCESS(fixture_setup(&fixture), "fixture setup failed");

	yanet_error *err = NULL;
	struct cp_device_config config;
	memset(&config, 0, sizeof(config));
	TEST_ASSERT_SUCCESS(
		cp_device_config_init(&config, "vxlan", "vx1", 0, 0, &err),
		"device config init failed"
	);
	struct cp_device *device =
		cp_device_new(&fixture.agent->memory_context);
	TEST_ASSERT_NOT_NULL(device, "device allocation failed");
	int rc = cp_device_init(device, fixture.agent, &config, &err);
	cp_device_config_fini(&config);
	TEST_ASSERT_EQUAL(rc, -1, "a C init of a vxlan device must be refused");
	char *message = yanet_error_format(err);
	TEST_ASSERT_STR_CONTAINS(
		message, "configuration layout", "the error names the layout"
	);
	free(message);
	yanet_error_free(err);
	memory_bfree(
		&fixture.agent->memory_context, device, sizeof(struct cp_device)
	);

	fixture_teardown(&fixture);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("error");

	int failed = 0;
	failed |= run_vxlan_device_loaded_test();
	failed |= run_vxlan_decap_reencap_test();
	failed |= run_vxlan_vni_mismatch_drop_test();
	failed |= run_vxlan_plain_frame_encap_test();
	failed |= run_vxlan_api_rejects_wide_vni_test();
	failed |= run_vxlan_api_shows_published_tunnel_test();
	failed |= run_vxlan_layout_mismatch_refused_test();
	return failed == TEST_SUCCESS ? 0 : 1;
}
