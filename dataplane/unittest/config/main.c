#include "assert.h"
#include "config.h"
#include "lib/dataplane/packet/packet.h"

#include <fcntl.h>
#include <string.h>

static void
check_instance(
	struct dataplane_instance_config *config,
	uint16_t numa_idx,
	uint64_t dp_memory,
	uint64_t cp_memory
) {
	assert(config->numa_idx == numa_idx);
	assert(config->dp_memory == dp_memory);
	assert(config->cp_memory == cp_memory);
}

static int
parse_yaml(const char *yaml, struct dataplane_config **config) {
	FILE *f = fmemopen((void *)yaml, strlen(yaml), "r");
	assert(f != NULL);

	int rc = dataplane_config_init(f, config);
	fclose(f);
	return rc;
}

struct numeric_field {
	const char *yaml_format;
	const char *overflow;
};

static void
test_numeric_field_rejects_invalid_values(const struct numeric_field *field) {
	const char *invalid_values[] = {
		"-1", "\" -1\"", "\"1\\0junk\"", field->overflow, "not-a-number"
	};
	char yaml[512];

	for (size_t value_idx = 0;
	     value_idx < sizeof(invalid_values) / sizeof(invalid_values[0]);
	     ++value_idx) {
		int length = snprintf(
			yaml,
			sizeof(yaml),
			field->yaml_format,
			invalid_values[value_idx]
		);
		assert(length >= 0 && (size_t)length < sizeof(yaml));

		struct dataplane_config *config = NULL;
		int rc = parse_yaml(yaml, &config);
		assert(rc == -1);
		assert(config == NULL);
	}
}

static void
test_numeric_field_maximum_values(void) {
	const char yaml[] = "dataplane:\n"
			    "  dpdk_memory: 18446744073709551615\n"
			    "  packet_recirc_limit: 256\n"
			    "  instances:\n"
			    "    - numa_id: 65535\n"
			    "      dp_memory: 18446744073709551615\n"
			    "      cp_memory: 18446744073709551615\n"
			    "  devices:\n"
			    "    - mtu: 4294967295\n"
			    "      max_lro_packet_size: 18446744073709551615\n"
			    "      rss_hash: 18446744073709551615\n"
			    "      workers:\n"
			    "        - core_id: 65535\n"
			    "          instance_id: 65535\n"
			    "          rx_queue_len: 65535\n"
			    "          tx_queue_len: 65535\n"
			    "          num_mbufs: 4294967295\n";

	struct dataplane_config *config = NULL;
	int rc = parse_yaml(yaml, &config);
	assert(rc == 0);

	assert(config->dpdk_memory == UINT64_MAX);
	assert(config->packet_recirc_limit == PACKET_RECIRC_LIMIT_MAX);
	assert(config->instance_count == 1);
	assert(config->instances[0].numa_idx == UINT16_MAX);
	assert(config->instances[0].dp_memory == UINT64_MAX);
	assert(config->instances[0].cp_memory == UINT64_MAX);
	assert(config->device_count == 1);
	assert(config->devices[0].mtu == UINT32_MAX);
	assert(config->devices[0].max_lro_packet_size == UINT64_MAX);
	assert(config->devices[0].rss_hash == UINT64_MAX);
	assert(config->devices[0].worker_count == 1);
	assert(config->devices[0].workers[0].core_id == UINT16_MAX);
	assert(config->devices[0].workers[0].instance_id == UINT16_MAX);
	assert(config->devices[0].workers[0].rx_queue_len == UINT16_MAX);
	assert(config->devices[0].workers[0].tx_queue_len == UINT16_MAX);
	assert(config->devices[0].workers[0].num_mbufs == UINT32_MAX);

	dataplane_config_free(config);
}

static void
test_packet_recirc_limit_default_and_bounds(void) {
	const char *yamls[] = {
		"dataplane: {}\n",
		"dataplane:\n  packet_recirc_limit: 4\n",
		"dataplane:\n  packet_recirc_limit: 256\n",
		"dataplane:\n  packet_recirc_limit: 37\n",
	};
	const uint16_t expected[] = {
		PACKET_RECIRC_LIMIT_DEFAULT,
		PACKET_RECIRC_LIMIT_MIN,
		PACKET_RECIRC_LIMIT_MAX,
		37,
	};

	for (size_t idx = 0; idx < sizeof(yamls) / sizeof(yamls[0]); ++idx) {
		struct dataplane_config *config = NULL;
		assert(parse_yaml(yamls[idx], &config) == 0);
		assert(config->packet_recirc_limit == expected[idx]);
		dataplane_config_free(config);
	}

	const char *invalid[] = {
		"dataplane:\n  packet_recirc_limit: 3\n",
		"dataplane:\n  packet_recirc_limit: 257\n",
	};
	for (size_t idx = 0; idx < sizeof(invalid) / sizeof(invalid[0]);
	     ++idx) {
		struct dataplane_config *config = NULL;
		assert(parse_yaml(invalid[idx], &config) == -1);
		assert(config == NULL);
	}
}

static void
test_numeric_field_ranges(void) {
	const struct numeric_field fields[] = {
		{"dataplane:\n  dpdk_memory: %s\n", "18446744073709551616"},
		{"dataplane:\n  instances:\n    - numa_id: %s\n", "65536"},
		{"dataplane:\n  instances:\n    - dp_memory: %s\n",
		 "18446744073709551616"},
		{"dataplane:\n  instances:\n    - cp_memory: %s\n",
		 "18446744073709551616"},
		{"dataplane:\n  devices:\n    - mtu: %s\n", "4294967296"},
		{"dataplane:\n  devices:\n    - max_lro_packet_size: %s\n",
		 "18446744073709551616"},
		{"dataplane:\n  devices:\n    - rss_hash: %s\n",
		 "18446744073709551616"},
		{"dataplane:\n  devices:\n    - workers:\n        - core_id: "
		 "%s\n",
		 "65536"},
		{"dataplane:\n  devices:\n    - workers:\n        - "
		 "instance_id: %s\n",
		 "65536"},
		{"dataplane:\n  devices:\n    - workers:\n        - "
		 "rx_queue_len: %s\n",
		 "65536"},
		{"dataplane:\n  devices:\n    - workers:\n        - "
		 "tx_queue_len: %s\n",
		 "65536"},
		{"dataplane:\n  devices:\n    - workers:\n        - num_mbufs: "
		 "%s\n",
		 "4294967296"},
	};

	for (size_t field_idx = 0;
	     field_idx < sizeof(fields) / sizeof(fields[0]);
	     ++field_idx) {
		test_numeric_field_rejects_invalid_values(fields + field_idx);
	}
}

static void
test_resolve_connections_unknown_device(void) {
	const char yaml[] = "dataplane:\n"
			    "  devices:\n"
			    "    - port_name: eth0\n"
			    "  connections:\n"
			    "    - src_device: eth0\n"
			    "      dst_device: does_not_exist\n";

	FILE *f = fmemopen((void *)yaml, strlen(yaml), "r");
	assert(f != NULL);

	struct dataplane_config *config = NULL;
	int rc = dataplane_config_init(f, &config);
	assert(rc == -1);
	assert(config == NULL);

	fclose(f);
}

static void
test_resolve_connections_duplicate_device(void) {
	const char yaml[] = "dataplane:\n"
			    "  devices:\n"
			    "    - port_name: eth0\n"
			    "    - port_name: eth0\n"
			    "  connections:\n"
			    "    - src_device: eth0\n"
			    "      dst_device: eth0\n";

	FILE *f = fmemopen((void *)yaml, strlen(yaml), "r");
	assert(f != NULL);

	struct dataplane_config *config = NULL;
	int rc = dataplane_config_init(f, &config);
	assert(rc == -1);
	assert(config == NULL);

	fclose(f);
}

static void
test_resolve_connections_empty_device_name(void) {
	const char yaml[] = "dataplane:\n"
			    "  devices:\n"
			    "    - port_name: eth0\n"
			    "  connections:\n"
			    "    - src_device: eth0\n"
			    "      dst_device: \"\"\n";

	FILE *f = fmemopen((void *)yaml, strlen(yaml), "r");
	assert(f != NULL);

	struct dataplane_config *config = NULL;
	int rc = dataplane_config_init(f, &config);
	assert(rc == -1);
	assert(config == NULL);

	fclose(f);
}

static void
test_valid_config(void) {
	FILE *f = fopen(CONFIG_PATH, "r");
	assert(f != NULL);

	struct dataplane_config *config = NULL;
	int rc = dataplane_config_init(f, &config);
	assert(rc == 0);

	assert(config->packet_recirc_limit == 37);
	assert(config->instance_count == 3);
	check_instance(config->instances, 0, 1024, 2048);
	check_instance(config->instances + 1, 1, 512, 128);
	check_instance(config->instances + 2, 0, 123, 124);

	assert(config->connection_count == 2);
	assert(config->connections[0].src_device_id == 0);
	assert(config->connections[0].dst_device_id == 1);
	assert(config->connections[1].src_device_id == 1);
	assert(config->connections[1].dst_device_id == 0);

	dataplane_config_free(config);

	fclose(f);
}

int
main(int argc, char **argv) {
	(void)argc;
	(void)argv;

	test_valid_config();
	test_numeric_field_maximum_values();
	test_packet_recirc_limit_default_and_bounds();
	test_numeric_field_ranges();
	test_resolve_connections_unknown_device();
	test_resolve_connections_duplicate_device();
	test_resolve_connections_empty_device_name();

	return 0;
}
