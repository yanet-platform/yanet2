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

	// The nested entry declares eth0's single queue; the entry sets no
	// sizing, so the queue materializes at the built-in defaults.
	assert(config->devices[0].queue_count == 1);
	assert(config->devices[0].queues != NULL);
	assert(config->devices[0].queues[0].rx_queue_len == 4096);
	assert(config->devices[0].queues[0].tx_queue_len == 4096);
	assert(config->devices[0].queues[0].num_mbufs == 16384);
	assert(config->devices[0].queues[0].role.rss);
	assert(config->devices[1].queue_count == 0);
	assert(config->devices[1].queues == NULL);
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

	// The nested entry's values become the materialized queue's.
	assert(config->devices[0].queue_count == 1);
	assert(config->devices[0].queues[0].rx_queue_len == UINT16_MAX);
	assert(config->devices[0].queues[0].tx_queue_len == UINT16_MAX);
	assert(config->devices[0].queues[0].num_mbufs == UINT32_MAX);
	assert(config->devices[0].queues[0].role.rss);
	assert(config->worker_count == 1);
	assert(config->workers[0].core_id == UINT16_MAX);
	assert(config->workers[0].assignment_count == 1);
	assert(config->workers[0].assignments[0].device_id == 0);
	assert(config->workers[0].assignments[0].queue_id == 0);

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
		{"dataplane:\n  devices:\n    - queue_count: %s\n", "65536"},
		{"dataplane:\n  devices:\n    - queues:\n        - "
		 "rx_queue_len: %s\n",
		 "65536"},
		{"dataplane:\n  devices:\n    - queues:\n        - "
		 "tx_queue_len: %s\n",
		 "65536"},
		{"dataplane:\n  devices:\n    - queues:\n        - num_mbufs: "
		 "%s\n",
		 "4294967296"},
		{"dataplane:\n  devices:\n    - default_queue:\n        "
		 "num_mbufs: %s\n",
		 "4294967296"},
		{"dataplane:\n  devices:\n    - queues:\n        - steer: "
		 "{dscp: %s}\n",
		 "64"},
		{"dataplane:\n  workers:\n    - core_id: %s\n", "65536"},
		{"dataplane:\n  workers:\n    - instance_id: %s\n", "65536"},
		{"dataplane:\n  workers:\n    - assignments:\n        - queue: "
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
			    "        - core_id: 6\n";

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

	// The nested entries' roles become their queues' roles.
	assert(port->queue_count == 2);
	assert(port->queues[0].role.rss);
	assert(port->queues[1].role.dscp ==
	       ((UINT64_C(1) << 48) | (UINT64_C(1) << 56)));
	assert(port->queues[1].role.arp);
	assert(port->queues[1].role.l2_control);

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

// The queue_count form declares N queues of the device defaults, bound by
// the top-level workers section.
static void
test_queue_count_form(void) {
	const char yaml[] = "dataplane:\n"
			    "  devices:\n"
			    "    - port_name: eth0\n"
			    "      queue_count: 2\n"
			    "  workers:\n"
			    "    - core_id: 3\n"
			    "      instance_id: 1\n"
			    "      assignments:\n"
			    "        - {device: eth0, queue: 0}\n"
			    "        - {device: eth0, queue: 1}\n";

	struct dataplane_config *config = NULL;
	assert(parse_yaml(yaml, &config) == 0);

	const struct dataplane_device_config *device = config->devices;
	assert(device->queue_count == 2);
	assert(device->queue_declarations == dataplane_queue_count);
	assert(device->worker_count == 0);
	for (uint32_t idx = 0; idx < device->queue_count; ++idx) {
		assert(device->queues[idx].rx_queue_len == 4096);
		assert(device->queues[idx].tx_queue_len == 4096);
		assert(device->queues[idx].num_mbufs == 16384);
		assert(device->queues[idx].role.rss);
	}

	// A single worker holds both queues.
	assert(config->worker_count == 1);
	assert(config->workers[0].core_id == 3);
	assert(config->workers[0].instance_id == 1);
	assert(config->workers[0].assignment_count == 2);
	assert(config->workers[0].assignments[0].device_id == 0);
	assert(config->workers[0].assignments[0].queue_id == 0);
	assert(config->workers[0].assignments[1].device_id == 0);
	assert(config->workers[0].assignments[1].queue_id == 1);

	dataplane_config_free(config);
}

// The queues list declares each queue: a field left unset takes the
// device's default_queue, and one still unset takes the built-in number.
// Roles are queue declarations here, a steering queue keeps a dedicated
// worker, and one worker may hold assignments of several devices.
static void
test_queues_list_form(void) {
	const char yaml[] = "dataplane:\n"
			    "  devices:\n"
			    "    - port_name: eth0\n"
			    "      default_queue:\n"
			    "        rx_queue_len: 8192\n"
			    "        num_mbufs: 32768\n"
			    "      queues:\n"
			    "        - {}\n"
			    "        - rx_queue_len: 1024\n"
			    "        - rss: false\n"
			    "          steer: {dscp: [48], nd: true}\n"
			    "    - port_name: eth1\n"
			    "      queues:\n"
			    "        - rx_queue_len: 0\n"
			    "  workers:\n"
			    "    - core_id: 3\n"
			    "      assignments:\n"
			    "        - {device: eth0, queue: 2}\n"
			    "    - core_id: 4\n"
			    "      assignments:\n"
			    "        - {device: eth0, queue: 0}\n"
			    "        - {device: eth0, queue: 1}\n"
			    "        - {device: eth1, queue: 0}\n";

	struct dataplane_config *config = NULL;
	assert(parse_yaml(yaml, &config) == 0);

	const struct dataplane_device_config *eth0 = config->devices;
	const struct dataplane_device_config *eth1 = config->devices + 1;
	assert(eth0->queue_count == 3);
	assert(eth0->queue_declarations == dataplane_queue_list);

	// An empty entry takes the device defaults, with the built-ins
	// filling what the device default leaves unset.
	assert(eth0->queues[0].rx_queue_len == 8192);
	assert(eth0->queues[0].tx_queue_len == 4096);
	assert(eth0->queues[0].num_mbufs == 32768);

	// The entry's own value beats the device default.
	assert(eth0->queues[1].rx_queue_len == 1024);
	assert(eth0->queues[1].tx_queue_len == 4096);
	assert(eth0->queues[1].num_mbufs == 32768);

	// The steering queue keeps its steered classes.
	assert(!eth0->queues[2].role.rss);
	assert(eth0->queues[2].role.dscp == (UINT64_C(1) << 48));
	assert(eth0->queues[2].role.nd);
	assert(!eth0->queues[2].role.arp);
	assert(!eth0->queues[2].role.l2_control);
	assert(dataplane_device_config_has_roles(eth0));

	// An explicit zero counts as unset and falls back the same way.
	assert(eth1->queues[0].rx_queue_len == 4096);
	assert(eth1->queues[0].tx_queue_len == 4096);
	assert(eth1->queues[0].num_mbufs == 16384);
	assert(!dataplane_device_config_has_roles(eth1));

	// One worker dedicates itself to the steering queue; another holds
	// rx assignments of two devices.
	assert(config->worker_count == 2);
	assert(config->workers[0].assignment_count == 1);
	assert(config->workers[0].assignments[0].device_id == 0);
	assert(config->workers[0].assignments[0].queue_id == 2);
	assert(config->workers[1].assignment_count == 3);
	assert(config->workers[1].assignments[0].device_id == 0);
	assert(config->workers[1].assignments[0].queue_id == 0);
	assert(config->workers[1].assignments[1].device_id == 0);
	assert(config->workers[1].assignments[1].queue_id == 1);
	assert(config->workers[1].assignments[2].device_id == 1);
	assert(config->workers[1].assignments[2].queue_id == 0);

	dataplane_config_free(config);
}

// The nested devices[].workers[] shape and the explicit queues plus
// top-level workers shape describe the same configuration.
static void
test_worker_forms_equivalent(void) {
	const char nested[] = "dataplane:\n"
			      "  devices:\n"
			      "    - port_name: eth0\n"
			      "      workers:\n"
			      "        - core_id: 3\n"
			      "          instance_id: 1\n"
			      "          rx_queue_len: 1024\n"
			      "        - core_id: 4\n"
			      "          rx_queue_len: 2048\n"
			      "          rss: false\n"
			      "          steer: {arp: true}\n";
	const char flat[] = "dataplane:\n"
			    "  devices:\n"
			    "    - port_name: eth0\n"
			    "      queues:\n"
			    "        - rx_queue_len: 1024\n"
			    "        - rx_queue_len: 2048\n"
			    "          rss: false\n"
			    "          steer: {arp: true}\n"
			    "  workers:\n"
			    "    - core_id: 3\n"
			    "      instance_id: 1\n"
			    "      assignments:\n"
			    "        - {device: eth0, queue: 0}\n"
			    "    - core_id: 4\n"
			    "      assignments:\n"
			    "        - {device: eth0, queue: 1}\n";

	struct dataplane_config *nested_config = NULL;
	struct dataplane_config *flat_config = NULL;
	assert(parse_yaml(nested, &nested_config) == 0);
	assert(parse_yaml(flat, &flat_config) == 0);

	const struct dataplane_device_config *nested_device =
		nested_config->devices;
	const struct dataplane_device_config *flat_device =
		flat_config->devices;
	assert(nested_device->queue_count == flat_device->queue_count);
	for (uint32_t idx = 0; idx < flat_device->queue_count; ++idx) {
		const struct dataplane_queue_config *nested_queue =
			nested_device->queues + idx;
		const struct dataplane_queue_config *flat_queue =
			flat_device->queues + idx;
		assert(nested_queue->rx_queue_len == flat_queue->rx_queue_len);
		assert(nested_queue->tx_queue_len == flat_queue->tx_queue_len);
		assert(nested_queue->num_mbufs == flat_queue->num_mbufs);
		assert(nested_queue->role.rss == flat_queue->role.rss);
		assert(nested_queue->role.dscp == flat_queue->role.dscp);
		assert(nested_queue->role.arp == flat_queue->role.arp);
	}

	assert(nested_config->worker_count == flat_config->worker_count);
	for (uint64_t idx = 0; idx < flat_config->worker_count; ++idx) {
		const struct dataplane_worker_config *nested_worker =
			nested_config->workers + idx;
		const struct dataplane_worker_config *flat_worker =
			flat_config->workers + idx;
		assert(nested_worker->core_id == flat_worker->core_id);
		assert(nested_worker->instance_id == flat_worker->instance_id);
		assert(nested_worker->assignment_count ==
		       flat_worker->assignment_count);
		for (uint32_t asgn_idx = 0;
		     asgn_idx < flat_worker->assignment_count;
		     ++asgn_idx) {
			assert(nested_worker->assignments[asgn_idx].device_id ==
			       flat_worker->assignments[asgn_idx].device_id);
			assert(nested_worker->assignments[asgn_idx].queue_id ==
			       flat_worker->assignments[asgn_idx].queue_id);
		}
	}

	dataplane_config_free(nested_config);
	dataplane_config_free(flat_config);
}

// Verifies that a device may declare its queues through exactly one shape.
static void
test_queue_declarations_rejected(void) {
	const struct {
		const char *name;
		const char *yaml;
	} cases[] = {
		{"queue_count beside queues",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queue_count: 2\n"
		 "      queues:\n"
		 "        - {}\n"
		 "        - {}\n"
		 "  workers:\n"
		 "    - assignments:\n"
		 "        - {device: eth0, queue: 0}\n"
		 "        - {device: eth0, queue: 1}\n"},
		{"queues beside nested workers",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queues:\n"
		 "        - {}\n"
		 "        - {}\n"
		 "      workers:\n"
		 "        - core_id: 3\n"
		 "        - core_id: 4\n"},
		{"an empty queues list",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queues: []\n"},
		{"a role on the device defaults",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queue_count: 1\n"
		 "      default_queue:\n"
		 "        rss: false\n"
		 "  workers:\n"
		 "    - assignments:\n"
		 "        - {device: eth0, queue: 0}\n"},
		{"workers declared at both levels",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      workers:\n"
		 "        - core_id: 3\n"
		 "    - port_name: eth1\n"
		 "      queue_count: 1\n"
		 "  workers:\n"
		 "    - core_id: 4\n"
		 "      assignments:\n"
		 "        - {device: eth1, queue: 0}\n"},
		{"a ring device with two queues",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: net_ring_:0:a:b\n"
		 "      queue_count: 2\n"
		 "  workers:\n"
		 "    - assignments:\n"
		 "        - {device: net_ring_:0:a:b, queue: 0}\n"
		 "        - {device: net_ring_:0:a:b, queue: 1}\n"},
	};

	for (size_t idx = 0; idx < sizeof(cases) / sizeof(cases[0]); ++idx) {
		fprintf(stderr, "rejects %s:\n", cases[idx].name);
		struct dataplane_config *config = NULL;
		assert(parse_yaml(cases[idx].yaml, &config) == -1);
		assert(config == NULL);
	}
}

// Verifies the assignment contract: names resolve, every declared queue is
// assigned exactly once, and a worker keeps at least one assignment.
static void
test_assignments_rejected(void) {
	const struct {
		const char *name;
		const char *yaml;
	} cases[] = {
		{"an assignment naming an unknown device",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queue_count: 1\n"
		 "  workers:\n"
		 "    - assignments:\n"
		 "        - {device: does_not_exist, queue: 0}\n"},
		{"an assignment naming an ambiguous device",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queue_count: 1\n"
		 "    - port_name: eth0\n"
		 "      queue_count: 1\n"
		 "  workers:\n"
		 "    - assignments:\n"
		 "        - {device: eth0, queue: 0}\n"},
		{"a queue index out of range",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queue_count: 1\n"
		 "  workers:\n"
		 "    - assignments:\n"
		 "        - {device: eth0, queue: 1}\n"},
		{"a queue assigned to two workers",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queue_count: 1\n"
		 "  workers:\n"
		 "    - core_id: 3\n"
		 "      assignments:\n"
		 "        - {device: eth0, queue: 0}\n"
		 "    - core_id: 4\n"
		 "      assignments:\n"
		 "        - {device: eth0, queue: 0}\n"},
		{"a queue assigned twice within one worker",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queue_count: 1\n"
		 "  workers:\n"
		 "    - assignments:\n"
		 "        - {device: eth0, queue: 0}\n"
		 "        - {device: eth0, queue: 0}\n"},
		{"a queue left unassigned",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queue_count: 2\n"
		 "  workers:\n"
		 "    - assignments:\n"
		 "        - {device: eth0, queue: 1}\n"},
		{"an assignment naming no device",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queue_count: 1\n"
		 "  workers:\n"
		 "    - assignments:\n"
		 "        - {queue: 0}\n"},
		{"an rx queue after a steering queue in one worker",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queues:\n"
		 "        - {}\n"
		 "        - rss: false\n"
		 "          steer: {arp: true}\n"
		 "  workers:\n"
		 "    - assignments:\n"
		 "        - {device: eth0, queue: 1}\n"
		 "        - {device: eth0, queue: 0}\n"},
		{"a steering queue after an rx queue in one worker",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queues:\n"
		 "        - {}\n"
		 "        - rss: false\n"
		 "          steer: {arp: true}\n"
		 "  workers:\n"
		 "    - assignments:\n"
		 "        - {device: eth0, queue: 0}\n"
		 "        - {device: eth0, queue: 1}\n"},
		{"a worker without assignments",
		 "dataplane:\n"
		 "  devices:\n"
		 "    - port_name: eth0\n"
		 "      queue_count: 1\n"
		 "  workers:\n"
		 "    - core_id: 3\n"
		 "    - core_id: 4\n"
		 "      assignments:\n"
		 "        - {device: eth0, queue: 0}\n"},
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

	// Both devices declare one nested worker each, so one queue each:
	// the entry's sizing with the unset num_mbufs at the built-in
	// default, polled by one single-assignment flat worker each.
	assert(config->devices[0].queue_count == 1);
	assert(config->devices[0].queues[0].rx_queue_len == 1024);
	assert(config->devices[0].queues[0].tx_queue_len == 512);
	assert(config->devices[0].queues[0].num_mbufs == 16384);
	assert(config->devices[1].queue_count == 1);
	assert(config->devices[1].queues[0].rx_queue_len == 128);
	assert(config->devices[1].queues[0].tx_queue_len == 4096);
	assert(config->worker_count == 2);
	assert(config->workers[0].assignment_count == 1);
	assert(config->workers[0].assignments[0].device_id == 0);
	assert(config->workers[0].assignments[0].queue_id == 0);
	assert(config->workers[1].assignments[0].device_id == 1);
	assert(config->workers[1].assignments[0].queue_id == 0);

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
	test_queue_roles_parsed();
	test_queue_roles_rejected();
	test_queue_count_form();
	test_queues_list_form();
	test_worker_forms_equivalent();
	test_queue_declarations_rejected();
	test_assignments_rejected();

	return 0;
}
