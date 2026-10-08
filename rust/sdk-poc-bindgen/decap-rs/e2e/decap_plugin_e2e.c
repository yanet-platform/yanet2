// End-to-end check of the Rust decap plugin inside the dataplane harness.
//
// Runs the same frames through a real dataplane_ut pipeline twice: once
// with the built-in C decap module and once with the plugin directory that
// holds the Rust libdecap_dp.so, which the loader prefers over the
// built-in. The configuration is published by the C control-plane API
// into agent arenas, so the Rust module reads a real shared-memory image.
// Exit status 0 means every frame produced identical results.

#include <stdio.h>
#include <string.h>

#include <rte_mbuf.h>

#include "api/agent.h"
#include "common/memory_address.h"
#include "common/test_assert.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "modules/decap/api/controlplane.h"

// The built-in C handler, linked from the decap module archive.
void
decap_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

#define MAX_FRAME 256
#define FRAME_COUNT 6

// Result of one frame: verdict, final bytes and descriptor metadata.
struct frame_result {
	int output;
	uint16_t len;
	uint8_t data[MAX_FRAME];
	uint32_t flow_label;
	uint16_t network_type;
	uint16_t transport_type;
	uint16_t transport_offset;
};

static size_t
put_ether(uint8_t *f, uint16_t type) {
	memset(f, 0x02, 12);
	f[12] = type >> 8;
	f[13] = type & 0xff;
	return 14;
}

static size_t
put_ipv4(uint8_t *f, uint8_t proto, const uint8_t dst[4], uint16_t frag,
	 uint16_t payload) {
	uint16_t total = 20 + payload;
	uint8_t header[20] = {
		0x45, 0, total >> 8, total & 0xff, 0x12, 0x34, frag >> 8,
		frag & 0xff, 64, proto, 0, 0, 198, 51, 100, 1,
	};
	memcpy(header + 16, dst, 4);
	memcpy(f, header, 20);
	return 20;
}

static size_t
put_ipv6(uint8_t *f, uint8_t next, uint8_t dst_prefix_last,
	 uint32_t flow_label, uint16_t payload) {
	uint32_t vtc_flow = 0x60000000u | flow_label;
	uint8_t header[40] = {
		vtc_flow >> 24, (vtc_flow >> 16) & 0xff, (vtc_flow >> 8) & 0xff,
		vtc_flow & 0xff, payload >> 8, payload & 0xff, next, 64,
		0x20, 0x01, 0x0d, 0xb8, 0xff, 0xff,
	};
	header[23] = 1;
	header[24] = 0x20;
	header[25] = 0x01;
	header[26] = 0x0d;
	header[27] = dst_prefix_last;
	header[39] = 1;
	memcpy(f, header, 40);
	return 40;
}

static size_t
put_tcp(uint8_t *f) {
	memset(f, 0, 20);
	f[1] = 0x39;
	f[3] = 0x50;
	f[12] = 0x50;
	return 20;
}

// Builds frame idx; returns its length.
static size_t
build_frame(int idx, uint8_t *f) {
	static const uint8_t in4[4] = {10, 1, 2, 3};
	static const uint8_t out4[4] = {203, 0, 113, 9};
	static const uint8_t inner_dst[4] = {192, 0, 2, 1};
	size_t n = 0;
	switch (idx) {
	case 0: // IP-in-IP to a configured prefix.
	case 1: // IP-in-IP to a foreign address.
	case 2: // Outer fragment to a configured prefix.
		n += put_ether(f, 0x0800);
		n += put_ipv4(f + n, 4, idx == 1 ? out4 : in4,
			      idx == 2 ? 0x2000 : 0, 40);
		n += put_ipv4(f + n, 6, inner_dst, 0, 20);
		n += put_tcp(f + n);
		return n;
	case 3: // GRE with key to a configured prefix.
		n += put_ether(f, 0x0800);
		n += put_ipv4(f + n, 47, in4, 0, 8 + 40);
		{
			uint8_t gre[8] = {0x20, 0, 0x08, 0, 0, 0, 0, 7};
			memcpy(f + n, gre, 8);
			n += 8;
		}
		n += put_ipv4(f + n, 6, inner_dst, 0, 20);
		n += put_tcp(f + n);
		return n;
	case 4: // IPv4 in IPv6 to a configured prefix.
	case 5: // The same to a foreign IPv6 address.
		n += put_ether(f, 0x86dd);
		n += put_ipv6(f + n, 4, idx == 4 ? 0xb8 : 0xb9, 0xabcde, 40);
		n += put_ipv4(f + n, 6, inner_dst, 0, 20);
		n += put_tcp(f + n);
		return n;
	}
	return 0;
}

static int
setup_pipeline(struct agent *agent) {
	yanet_error *err = NULL;
	struct cp_module *decap =
		decap_module_config_new(agent, "unwrap", &err);
	TEST_ASSERT_NOT_NULL(decap, "decap_module_config_new failed");
	uint8_t from4[4] = {10, 0, 0, 0};
	uint8_t to4[4] = {10, 255, 255, 255};
	TEST_ASSERT_SUCCESS(
		decap_module_config_add_prefix_v4(decap, from4, to4),
		"decap v4 prefix setup failed"
	);
	uint8_t from6[16] = {0x20, 0x01, 0x0d, 0xb8};
	uint8_t to6[16];
	memset(to6, 0xff, sizeof(to6));
	memcpy(to6, from6, 4);
	TEST_ASSERT_SUCCESS(
		decap_module_config_add_prefix_v6(decap, from6, to6),
		"decap v6 prefix setup failed"
	);
	struct cp_module *modules[] = {decap};
	TEST_ASSERT_SUCCESS(
		agent_update_modules(agent, 1, modules, &err),
		"module update failed"
	);

	const char *chain_types[] = {"decap"};
	const char *chain_names[] = {"unwrap"};
	struct cp_chain_config *chain =
		cp_chain_config_create("chain", 1, chain_types, chain_names);
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
		agent_update_functions(agent, 1, functions, &err),
		"function update failed"
	);
	cp_function_config_free(function);

	struct cp_pipeline_config *input = cp_pipeline_config_create("input", 1);
	TEST_ASSERT_NOT_NULL(input, "input pipeline allocation failed");
	TEST_ASSERT_SUCCESS(
		cp_pipeline_config_set_function(input, 0, "function"),
		"input pipeline setup failed"
	);
	struct cp_pipeline_config *output =
		cp_pipeline_config_create("output", 0);
	TEST_ASSERT_NOT_NULL(output, "output pipeline allocation failed");
	struct cp_pipeline_config *pipelines[] = {input, output};
	TEST_ASSERT_SUCCESS(
		agent_update_pipelines(agent, 2, pipelines, &err),
		"pipeline update failed"
	);
	cp_pipeline_config_free(input);
	cp_pipeline_config_free(output);

	struct cp_device_plain_config *device_config =
		cp_device_plain_config_new("dev0", 1, 1, &err);
	TEST_ASSERT_NOT_NULL(device_config, "device config failed");
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
		cp_device_plain_new(agent, device_config, &err);
	cp_device_plain_config_free(device_config);
	TEST_ASSERT_NOT_NULL(device, "cp_device_plain_new failed");
	struct cp_device *devices[] = {device};
	TEST_ASSERT_SUCCESS(
		agent_update_devices(agent, 1, devices, &err),
		"device update failed"
	);
	yanet_error *device_err = NULL;
	cp_device_plain_free(device, &device_err);
	yanet_error_free(device_err);
	return TEST_SUCCESS;
}

static void
record(struct packet *packet, int output, struct frame_result *result) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	result->output = output;
	result->len = rte_pktmbuf_data_len(mbuf);
	memcpy(result->data,
	       rte_pktmbuf_mtod(mbuf, uint8_t *),
	       result->len < MAX_FRAME ? result->len : MAX_FRAME);
	result->flow_label = packet->flow_label;
	result->network_type = packet->network_header.type;
	result->transport_type = packet->transport_header.type;
	result->transport_offset = packet->transport_header.offset;
}

// Runs every frame through the decap pipeline; plugin_dir selects the
// module implementation.
static int
run(const char *plugin_dir, struct frame_result *results) {
	const char *port_names[] = {"dev0"};
	const char *module_names[] = {"decap"};
	const char *devs_to_load[] = {"plain"};
	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = module_names,
		.module_count = 1,
		.plugin_dir = plugin_dir,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};
	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	TEST_ASSERT_NOT_NULL(ut, "dataplane_ut_new returned NULL");
	struct yanet_shm *shm = dataplane_ut_shm(ut);
	yanet_error *err = NULL;
	struct agent *agent = agent_attach(shm, 0, "e2e", 8u << 20, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");
	TEST_ASSERT_SUCCESS(setup_pipeline(agent), "pipeline setup failed");

	struct dp_config *dp_config = yanet_shm_dp_config(shm, 0);
	struct dp_module *dp_modules = ADDR_OF(&dp_config->dp_modules);
	printf("%s: decap handler at %p (built-in C handler at %p)\n",
	       plugin_dir ? "plugin" : "built-in",
	       (void *)dp_modules[0].handler,
	       (void *)decap_handle_packets);

	for (int idx = 0; idx < FRAME_COUNT; ++idx) {
		uint8_t frame[MAX_FRAME];
		size_t len = build_frame(idx, frame);
		struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(ut);
		TEST_ASSERT_NOT_NULL(mbuf, "mbuf allocation failed");
		struct packet *packet = mbuf_to_packet(mbuf);
		memset(packet, 0, sizeof(*packet));
		packet->mbuf = mbuf;
		memcpy(rte_pktmbuf_append(mbuf, len), frame, len);
		TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");

		struct packet_list input;
		packet_list_init(&input);
		packet_list_add(&input, packet);
		struct dataplane_ut_round_result result;
		dataplane_ut_run(ut, 0, &input, &result);
		struct packet *out = packet_list_first(&result.output);
		struct packet *drop = packet_list_first(&result.drop);
		TEST_ASSERT(
			(out == NULL) != (drop == NULL),
			"frame %d must leave exactly once",
			idx
		);
		record(out ? out : drop, out != NULL, &results[idx]);
		dataplane_ut_round_result_free(&result);
	}

	agent_detach(agent);
	dataplane_ut_free(ut);
	return TEST_SUCCESS;
}

int
main(int argc, char **argv) {
	if (argc != 2) {
		fprintf(stderr, "usage: %s <plugin-dir>\n", argv[0]);
		return 2;
	}
	static struct frame_result c_results[FRAME_COUNT];
	static struct frame_result rust_results[FRAME_COUNT];
	TEST_ASSERT_SUCCESS(run(NULL, c_results), "built-in run failed");
	TEST_ASSERT_SUCCESS(run(argv[1], rust_results), "plugin run failed");

	int failures = 0;
	for (int idx = 0; idx < FRAME_COUNT; ++idx) {
		struct frame_result *c = &c_results[idx];
		struct frame_result *r = &rust_results[idx];
		int same = c->output == r->output && c->len == r->len &&
			   memcmp(c->data, r->data, c->len) == 0 &&
			   c->flow_label == r->flow_label &&
			   c->network_type == r->network_type &&
			   c->transport_type == r->transport_type &&
			   c->transport_offset == r->transport_offset;
		printf("frame %d: %s len %u flow_label %#x: %s\n",
		       idx,
		       r->output ? "output" : "drop",
		       r->len,
		       r->flow_label,
		       same ? "same as C" : "DIFFERENT");
		failures += !same;
	}
	return failures == 0 ? 0 : 1;
}
