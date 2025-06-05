#pragma once

#include <stdint.h>
#include <stdio.h>

struct dataplane_instance_config {
	uint16_t numa_idx;
	uint64_t dp_memory;
	uint64_t cp_memory;
};

struct dataplane_device_worker_config {
	uint16_t core_id;
	uint16_t instance_id;
	uint16_t rx_queue_len;
	uint16_t tx_queue_len;
};

struct dataplane_device_config {
	char port_name[80];
	char mac_addr[20];

	uint32_t mtu;
	uint64_t max_lro_packet_size;
	uint64_t rss_hash;

	uint32_t worker_count;
	struct dataplane_device_worker_config *workers;
};

struct dataplane_connection_config {
	uint64_t src_device_id;
	uint64_t dst_device_id;
};

struct dataplane_config {
	char storage[80];
	uint64_t dpdk_memory;

	uint16_t numa_count;

	uint16_t instance_count;
	struct dataplane_instance_config *instances;

	uint64_t device_count;
	struct dataplane_device_config *devices;
	uint64_t connection_count;
	struct dataplane_connection_config *connections;
	char loglevel[32];
};

int
dataplane_config_init(FILE *file, struct dataplane_config **config);

void
dataplane_config_free(struct dataplane_config *config);
