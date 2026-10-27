#include "assert.h"
#include "config.h"
#include "lib/dataplane/packet/packet.h"

#include <errno.h>
#include <fcntl.h>
#include <stdlib.h>
#include <string.h>

static size_t refused_device_allocation_size;
static unsigned refused_device_allocations;

void *
__real_realloc(void *ptr, size_t size);

// Refuse only the device-array allocation selected by the current scenario.
void *
__wrap_realloc(void *ptr, size_t size) {
	if (refused_device_allocation_size != 0 &&
	    size == refused_device_allocation_size) {
		++refused_device_allocations;
		errno = ENOMEM;
		return NULL;
	}
	return __real_realloc(ptr, size);
}

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

static const char devices_yaml[] = "dataplane:\n"
				   "  devices:\n"
				   "    - port_name: eth0\n"
				   "      workers:\n"
				   "        - core_id: 1\n"
				   "    - port_name: eth1\n";

// Verifies that failed array growth reclaims previously initialized devices.
static void
test_device_allocation_failure(size_t device_count) {
	refused_device_allocation_size =
		device_count * sizeof(struct dataplane_device_config);
	refused_device_allocations = 0;
	struct dataplane_config *config = NULL;
	int rc = parse_yaml(devices_yaml, &config);
	refused_device_allocation_size = 0;

	assert(refused_device_allocations == 1);
	assert(rc == -1);
	assert(config == NULL);
}

// Verifies that successful device growth preserves owned worker declarations.
static void
test_device_allocation_success(void) {
	struct dataplane_config *config = NULL;
	assert(parse_yaml(devices_yaml, &config) == 0);
	assert(config->device_count == 2);
	assert(strcmp(config->devices[0].port_name, "eth0") == 0);
	assert(strcmp(config->devices[1].port_name, "eth1") == 0);
	assert(config->devices[0].worker_count == 1);
	assert(config->devices[0].workers[0].core_id == 1);
	assert(config->devices[1].worker_count == 0);
	assert(config->devices[1].workers == NULL);
	assert(config->worker_count == 1);
	assert(config->workers[0].assignments[0].device_id == 0);
	dataplane_config_free(config);
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

// Verifies that declarations on one core merge into one worker polling
// their queues in declaration order.
static void
test_expand_workers_merges_shared_core(void) {
	const char yaml[] = "dataplane:\n"
			    "  devices:\n"
			    "    - port_name: eth0\n"
			    "      workers:\n"
			    "        - core_id: 1\n"
			    "        - core_id: 5\n"
			    "          rx_queue_len: 128\n"
			    "        - core_id: 5\n"
			    "          tx_queue_len: 64\n"
			    "    - port_name: kni0\n"
			    "      workers:\n"
			    "        - core_id: 5\n"
			    "          num_mbufs: 512\n";

	struct dataplane_config *config = NULL;
	assert(parse_yaml(yaml, &config) == 0);

	assert(config->devices[0].worker_count == 3);
	assert(config->worker_count == 2);
	assert(config->workers[0].core_id == 1);
	assert(config->workers[0].assignment_count == 1);

	const struct dataplane_worker_config *merged = config->workers + 1;
	assert(merged->core_id == 5);
	assert(merged->assignment_count == 3);
	assert(merged->assignments[0].device_id == 0);
	assert(merged->assignments[0].queue_id == 1);
	assert(merged->assignments[0].rx_queue_len == 128);
	assert(merged->assignments[1].device_id == 0);
	assert(merged->assignments[1].queue_id == 2);
	assert(merged->assignments[1].tx_queue_len == 64);
	assert(merged->assignments[2].device_id == 1);
	assert(merged->assignments[2].queue_id == 0);
	assert(merged->assignments[2].num_mbufs == 512);

	dataplane_config_free(config);
}

// Verifies that a core declared under two instances is rejected.
static void
test_expand_workers_rejects_core_instance_conflict(void) {
	const char yaml[] = "dataplane:\n"
			    "  devices:\n"
			    "    - port_name: eth0\n"
			    "      workers:\n"
			    "        - core_id: 3\n"
			    "          instance_id: 0\n"
			    "    - port_name: eth1\n"
			    "      workers:\n"
			    "        - core_id: 3\n"
			    "          instance_id: 1\n";

	struct dataplane_config *config = NULL;
	assert(parse_yaml(yaml, &config) == -1);
	assert(config == NULL);
}

// Verifies that queue roles are parsed per worker entry, with a queue in
// the RSS table and steering nothing by default.
static void
test_queue_roles_parsed(void) {
	const char yaml[] = "dataplane:\n"
			    "  devices:\n"
			    "    - port_name: eth0\n"
			    "      workers:\n"
			    "        - core_id: 1\n"
			    "        - core_id: 5\n"
			    "          rss: false\n"
			    "          steer:\n"
			    "            dscp: [48, 56]\n"
			    "            arp: true\n"
			    "            nd: false\n"
			    "            l2_control: true\n"
			    "    - port_name: virtio_user_kni0\n"
			    "      workers:\n"
			    "        - core_id: 5\n";

	struct dataplane_config *config = NULL;
	assert(parse_yaml(yaml, &config) == 0);

	const struct dataplane_device_config *port = config->devices;
	assert(port->workers[0].role.rss);
	assert(port->workers[0].role.dscp == 0);
	assert(!port->workers[0].role.arp);
	assert(!port->workers[1].role.rss);
	assert(port->workers[1].role.dscp ==
	       ((UINT64_C(1) << 48) | (UINT64_C(1) << 56)));
	assert(port->workers[1].role.arp);
	assert(!port->workers[1].role.nd);
	assert(port->workers[1].role.l2_control);
	assert(dataplane_device_config_has_roles(port));
	assert(!dataplane_device_config_has_roles(config->devices + 1));

	dataplane_config_free(config);
}

// Verifies that queue roles a port cannot carry are rejected.
static void
test_queue_roles_rejected(void) {
	const struct {
		const char *name;
		const char *yaml;
	} cases[] = {
		{"queue 0 out of the RSS table",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      workers:\n"
		 "        - core_id: 5\n"
		 "          rss: false\n"
		 "          steer: {arp: true}\n"
		 "        - core_id: 1\n"},
		{"a core polling an RSS queue and a steering queue",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      workers:\n"
		 "        - core_id: 1\n"
		 "        - core_id: 5\n"
		 "          rss: false\n"
		 "          steer: {arp: true}\n"
		 "    - port_name: eth1\n"
		 "      workers:\n"
		 "        - core_id: 5\n"},
		{"a steering queue with no class",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      workers:\n"
		 "        - core_id: 1\n"
		 "        - core_id: 5\n"
		 "          rss: false\n"},
		{"dscp 0, which is all unmarked traffic",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      workers:\n"
		 "        - core_id: 1\n"
		 "        - core_id: 5\n"
		 "          rss: false\n"
		 "          steer: {dscp: 0}\n"},
		{"nd and a dscp value on different queues",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      workers:\n"
		 "        - core_id: 1\n"
		 "          steer: {nd: true}\n"
		 "        - core_id: 5\n"
		 "          rss: false\n"
		 "          steer: {dscp: 48}\n"},
	};

	for (size_t idx = 0; idx < sizeof(cases) / sizeof(cases[0]); ++idx) {
		fprintf(stderr, "rejects %s:\n", cases[idx].name);
		struct dataplane_config *config = NULL;
		assert(parse_yaml(cases[idx].yaml, &config) == -1);
		assert(config == NULL);
	}
}

// The object list keeps every configured type name in order, as the
// module list does, and refuses a name the loader could not use whole.
static void
test_objects_list(void) {
	const char yaml[] = "dataplane:\n"
			    "  devices:\n"
			    "    - port_name: eth0\n"
			    "  modules:\n"
			    "    - plugin_module\n"
			    "  objects:\n"
			    "    - ring\n"
			    "    - plugin_store\n";
	struct dataplane_config *config = NULL;
	assert(parse_yaml(yaml, &config) == 0);
	assert(config->module_count == 1);
	assert(strcmp(config->module_names[0], "plugin_module") == 0);
	assert(config->object_count == 2);
	assert(strcmp(config->object_names[0], "ring") == 0);
	assert(strcmp(config->object_names[1], "plugin_store") == 0);
	dataplane_config_free(config);

	const char empty[] = "dataplane:\n"
			     "  devices:\n"
			     "    - port_name: eth0\n"
			     "  objects:\n"
			     "    - \"\"\n";
	config = NULL;
	assert(parse_yaml(empty, &config) == -1);
	assert(config == NULL);

	char long_name[DATAPLANE_OBJECT_NAME_LEN + 1];
	memset(long_name, 'a', DATAPLANE_OBJECT_NAME_LEN);
	long_name[DATAPLANE_OBJECT_NAME_LEN] = '\0';
	char overlong[256];
	snprintf(
		overlong,
		sizeof(overlong),
		"dataplane:\n"
		"  devices:\n"
		"    - port_name: eth0\n"
		"  objects:\n"
		"    - %s\n",
		long_name
	);
	config = NULL;
	assert(parse_yaml(overlong, &config) == -1);
	assert(config == NULL);
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
	assert(config->connections[0].device_id == 1);
	assert(config->connections[1].src_device_id == 1);
	assert(config->connections[1].device_id == 0);

	dataplane_config_free(config);

	fclose(f);
}

int
main(int argc, char **argv) {
	(void)argc;
	(void)argv;

	test_valid_config();
	test_device_allocation_failure(1);
	test_device_allocation_failure(2);
	test_device_allocation_success();
	test_objects_list();
	test_numeric_field_maximum_values();
	test_packet_recirc_limit_default_and_bounds();
	test_numeric_field_ranges();
	test_resolve_connections_unknown_device();
	test_resolve_connections_duplicate_device();
	test_resolve_connections_empty_device_name();
	test_expand_workers_merges_shared_core();
	test_expand_workers_rejects_core_instance_conflict();
	test_queue_roles_parsed();
	test_queue_roles_rejected();

	return 0;
}
