#pragma once

#include <stdint.h>
#include <stdio.h>

#define DATAPLANE_MODULE_NAME_LEN 80
#define DATAPLANE_MAX_MODULES 64
#define DATAPLANE_PLUGIN_DIR_LEN 256

struct dataplane_instance_config {
	uint16_t numa_idx;
	// Bytes, the file may add a KiB, MiB, GiB or TiB unit.
	uint64_t dp_memory;
	uint64_t cp_memory;
};

struct dataplane_device_worker_config {
	uint16_t core_id;
	uint16_t instance_id;
	uint16_t rx_queue_len;
	uint16_t tx_queue_len;
	uint32_t num_mbufs;
};

// One (device, queue) pair a worker polls.
//
// The queue's ring and pool sizing travels with the assignment: it is a
// property of the queue, not of the worker.
struct dataplane_assignment_config {
	uint32_t device_id;
	uint32_t queue_id;
	uint16_t rx_queue_len;
	uint16_t tx_queue_len;
	uint32_t num_mbufs;
};

// A worker thread: one core, one instance, one or more assignments.
struct dataplane_worker_config {
	uint16_t core_id;
	uint16_t instance_id;
	uint32_t assignment_count;
	struct dataplane_assignment_config *assignments;
};

struct dataplane_device_config {
	char device_name[80];
	char port_name[80];
	char mac_addr[20];

	uint32_t mtu;
	uint64_t max_lro_packet_size;
	uint64_t rss_hash;

	// The workers declared inside the device; also the device's queue
	// count, with worker i polling queue i. Expanded into the flat
	// top-level worker list (one single-assignment worker per entry) by
	// dataplane_config_init.
	uint32_t worker_count;
	struct dataplane_device_worker_config *workers;
};

struct dataplane_connection_config {
	uint64_t src_device_id;
	uint64_t device_id;
	char src_device[80];
	char dst_device[80];
};

struct dataplane_config {
	char storage[80];
	uint64_t dpdk_memory;
	uint16_t packet_recirc_limit;

	// IOVA mode passed to DPDK EAL via --iova-mode.
	// Empty string means "do not pass the flag" (EAL autodetect).
	// Valid values: "pa" (physical addresses, required for vfio
	// no-iommu / uio in VMs without an IOMMU) or "va" (virtual
	// addresses, required by virtio_user with the vhost-net backend).
	char iova_mode[8];

	uint16_t numa_count;

	uint16_t instance_count;
	struct dataplane_instance_config *instances;

	uint64_t device_count;
	struct dataplane_device_config *devices;
	uint64_t connection_count;
	struct dataplane_connection_config *connections;

	// The flat worker list the nested devices[].workers[] entries expand
	// into, one single-assignment worker per entry in device and entry
	// order; worker i of device d polls queue i of d.
	uint64_t worker_count;
	struct dataplane_worker_config *workers;

	char loglevel[32];

	char plugin_dir[DATAPLANE_PLUGIN_DIR_LEN];
	uint64_t module_count;
	char module_names[DATAPLANE_MAX_MODULES][DATAPLANE_MODULE_NAME_LEN];
};

int
dataplane_config_init(FILE *file, struct dataplane_config **config);

void
dataplane_config_free(struct dataplane_config *config);
