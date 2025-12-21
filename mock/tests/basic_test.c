#include "../mock.h"
#include "api/agent.h"
#include "api/config.h"

#include "common/test_assert.h"
#include "controlplane/config/cp_device.h"
#include "controlplane/config/cp_function.h"
#include "controlplane/config/cp_module.h"
#include "controlplane/config/cp_pipeline.h"
#include "dataplane/packet/packet.h"
#include "devices/plain/api/controlplane.h"
#include "logging/log.h"
#include "mock/config.h"
#include "mock/packet.h"
#include "my_module/controlplane.h"
#include "utils/packet.h"
#include <assert.h>
#include <netinet/in.h>
#include <stdio.h>
#include <time.h>

#include "lib/controlplane/config/econtext.h"

////////////////////////////////////////////////////////////////////////////////

const char *module_name = "my_module";

////////////////////////////////////////////////////////////////////////////////

static int
setup_cp(struct agent *agent, struct cp_module *cp_module) {
	// setup modules

	LOG(INFO, "update modules...");

	int res = agent_update_modules(agent, 1, &cp_module);
	TEST_ASSERT_EQUAL(res, 0, "failed to update cp modules");

	// setup chain config
	const char *module_type = "balancer";
	struct cp_chain_config *chain_config =
		cp_chain_config_create("ch0", 1, &module_type, &module_name);
	TEST_ASSERT_NOT_NULL(chain_config, "failed to create chain config");

	// setup function config

	struct cp_function_config *function_config =
		cp_function_config_create("f0", 1);
	TEST_ASSERT_NOT_NULL(
		function_config, "failed to create function config"
	);
	cp_function_config_set_chain(function_config, 0, chain_config, 1);

	// update functions in controlplane

	LOG(INFO, "update functions...");

	res = agent_update_functions(agent, 1, &function_config);
	TEST_ASSERT_EQUAL(res, 0, "failed to update functions in controlplane");

	// setup pipelines

	struct cp_pipeline_config *pipeline_config =
		cp_pipeline_config_create("p0", 1);
	TEST_ASSERT_NOT_NULL(
		pipeline_config, "failed to create pipeline config"
	);
	cp_pipeline_config_set_function(pipeline_config, 0, "f0");

	// update pipelines in controlplane

	LOG(INFO, "update pipelines...");

	res = agent_update_pipelines(agent, 1, &pipeline_config);
	TEST_ASSERT_EQUAL(res, 0, "failed to update pipelines in controlplane");

	struct cp_pipeline_config *dummy_pipeline_config =
		cp_pipeline_config_create("dummy", 0);
	TEST_ASSERT_NOT_NULL(
		dummy_pipeline_config, "failed to create dummy pipeline config"
	);

	res = agent_update_pipelines(agent, 1, &dummy_pipeline_config);
	TEST_ASSERT_EQUAL(res, 0, "failed to update pipelines in controlplane");

	struct cp_device_plain_config *device_config =
		cp_device_plain_config_create("01:00.0", 1, 1);
	TEST_ASSERT_NOT_NULL(device_config, "failed to create device config");

	res = cp_device_plain_config_set_input_pipeline(
		device_config, 0, "p0", 1
	);
	TEST_ASSERT_EQUAL(res, 0, "failed to set input pipeline for device");

	res = cp_device_plain_config_set_output_pipeline(
		device_config, 0, "dummy", 1
	);
	TEST_ASSERT_EQUAL(res, 0, "failed to set output pipeline for device");

	struct cp_device *cp_device =
		cp_device_plain_create(agent, device_config);
	TEST_ASSERT_NOT_NULL(cp_device, "failed to create plain cp device");

	LOG(INFO, "update devices...");
	res = agent_update_devices(agent, 1, &cp_device);
	TEST_ASSERT_EQUAL(res, 0, "failed to update devices in controlplane");

	// free allocated memory

	// dont free chain config because it will be freed by
	// `cp_function_config_free`
	cp_function_config_free(function_config);
	cp_pipeline_config_free(pipeline_config);
	cp_pipeline_config_free(dummy_pipeline_config);
	cp_device_plain_config_free(device_config);

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

static uint8_t
packet_data_chsum(struct packet_data *data) {
	uint8_t x = 0;
	for (size_t i = 0; i < data->size; ++i) {
		x ^= data->data[i];
	}
	return x;
}

////////////////////////////////////////////////////////////////////////////////

static int
send_packet(struct yanet_mock *mock) {
	struct packet_list packets;
	packet_list_init(&packets);
	struct packet packet;
	const uint8_t src_ip[] = {10, 12, 13, 1};
	const uint8_t dst_ip[] = {10, 12, 13, 1};
	int res = fill_packet(
		&packet, src_ip, dst_ip, 1000, 80, IPPROTO_UDP, IPPROTO_IP, 0
	);

	struct packet_data init_packet_data = packet_data(&packet);
	uint8_t init_chsum = packet_data_chsum(&init_packet_data);

	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet");
	packet.tx_device_id = 0;
	packet.rx_device_id = 0;
	packet_list_add(&packets, &packet);
	struct packet_handle_result result;
	yanet_mock_handle_packets(mock, &packets, 0, &result);
	TEST_ASSERT_EQUAL(
		result.output_packets.count, 1, "no packets in output"
	);
	TEST_ASSERT_EQUAL(
		result.drop_packets.count, 0, "there are some dropped packets"
	);
	struct packet *p = result.output_packets.first;
	TEST_ASSERT_EQUAL(p, &packet, "returned packet is not which were sent");

	struct packet_data result_packet_data = packet_data(&packet);
	uint8_t result_chsum = packet_data_chsum(&result_packet_data);

	TEST_ASSERT_EQUAL(
		init_chsum,
		result_chsum,
		"initial and result packets checksum mismatch"
	);

	free_packet(&packet);

	return TEST_SUCCESS;
}

int
main() {
	log_enable_name("debug");

	// make config

	struct yanet_mock_config config;
	config.cp_memory = 1 << 27;
	config.dp_memory = 1 << 20;
	config.device_count = 1;
	config.devices[0].id = 0;
	config.worker_count = 1;
	memset(config.devices[0].name, 0, sizeof(config.devices[0].name));
	strcpy(config.devices[0].name, "01:00.0");

	LOG(INFO, "initialize mock...");

	struct yanet_mock mock;
	int res = yanet_mock_init(&mock, &config, NULL);
	TEST_ASSERT_EQUAL(res, 0, "failed to init mock");

	struct yanet_shm *shm = yanet_mock_shm(&mock);
	TEST_ASSERT_NOT_NULL(shm, "invalid shm");

	LOG(INFO, "attach agent...");

	struct agent *agent = agent_attach(shm, 0, "agent", 1 << 20);
	TEST_ASSERT_NOT_NULL(agent, "failed to attach agent: agent is null");

	LOG(INFO, "init module...");

	struct my_module_config *my_module =
		my_module_config_create(agent, module_name);
	TEST_ASSERT_NOT_NULL(my_module, "failed to create module config");
	my_module->packet_counter = 0;

	LOG(INFO, "setup controlplane...");

	res = setup_cp(agent, &my_module->cp_module);
	TEST_ASSERT_SUCCESS(res, "failed to setup cp");

	// Set current time
	struct timespec current_time = {123, 321};
	yanet_mock_set_current_time(&mock, &current_time);

	LOG(INFO, "send packet...");
	res = send_packet(&mock);
	TEST_ASSERT_SUCCESS(res, "failed to send packet");

	LOG(INFO,
	    "packets passed throw my module: %lu",
	    my_module->packet_counter);

	uint64_t last_packet_timestamp = my_module->last_packet_timestamp;
	LOG(INFO,
	    "last packet timestamp: sec=%lu, nsec=%lu",
	    last_packet_timestamp / (uint64_t)1e9,
	    last_packet_timestamp % (uint64_t)1e9);

	TEST_ASSERT_EQUAL(
		my_module->packet_counter,
		1,
		"my module packet counter not updated"
	);

	TEST_ASSERT_EQUAL(
		my_module->last_packet_timestamp,
		current_time.tv_sec * (uint64_t)1000 * 1000 * 1000 +
			current_time.tv_nsec,
		"incorrect current time"
	);

	LOG(INFO, "success");

	my_module_config_free(my_module);

	yanet_mock_free(&mock);
	return 0;
}