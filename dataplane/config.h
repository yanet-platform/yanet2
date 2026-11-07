#pragma once

#include <stdint.h>
#include <stdio.h>

#define DATAPLANE_MODULE_NAME_LEN 80
#define DATAPLANE_MAX_MODULES 64
#define DATAPLANE_OBJECT_NAME_LEN 80
#define DATAPLANE_MAX_OBJECTS 64
#define DATAPLANE_PLUGIN_DIR_LEN 256

struct dataplane_instance_config {
	uint16_t numa_idx;
	// Bytes, the file may add a KiB, MiB, GiB or TiB unit.
	uint64_t dp_memory;
	uint64_t cp_memory;
};

// Which traffic the NIC delivers to a port queue.
//
// A queue left out of the RSS table is a steering queue: it receives only
// the classes steered to it, through rte_flow rules.
struct dataplane_queue_role {
	// Whether the queue is in the port's RSS table.
	uint8_t rss;

	// The steered classes. Bit n of dscp steers DSCP value n, in IPv4 and
	// IPv6; nd covers ICMPv6 types 133-136; l2_control covers the IEEE
	// 802.1 link-local block 01:80:c2:00:00:0X (STP, LACP, LLDP).
	uint64_t dscp;
	uint8_t arp;
	uint8_t nd;
	uint8_t l2_control;
};

struct dataplane_device_worker_config {
	uint16_t core_id;
	uint16_t instance_id;
	uint16_t rx_queue_len;
	uint16_t tx_queue_len;
	uint32_t num_mbufs;

	struct dataplane_queue_role role;
};

// One declared queue of a device: the ring and pool sizing of its rx/tx
// pair, plus the traffic the NIC delivers to it.
//
// Every queue ends up fully materialized: a field the queue leaves at zero
// takes the device's default_queue value, and one that is still zero takes
// the built-in default. Consumers read the effective numbers directly.
struct dataplane_queue_config {
	uint16_t rx_queue_len;
	uint16_t tx_queue_len;
	uint32_t num_mbufs;

	struct dataplane_queue_role role;
};

// Which YAML shapes declared a device's queues: a parsed device carries
// at most one of these bits.
enum dataplane_queue_declaration {
	dataplane_queue_none = 0,
	dataplane_queue_count = 1 << 0,	 // queue_count: N
	dataplane_queue_list = 1 << 1,	 // queues: [...]
	dataplane_queue_nested = 1 << 2, // nested devices[].workers[] entries
};

// One (device, queue) pair a worker polls, with the pair's rx service:
// how many bursts the pair takes per worker round, always at least one;
// the nested workers[] form keeps its one-burst-per-round meaning.
//
// The device travels by name and is resolved to device_id by
// dataplane_config_init, exactly like a connection's src_device; the
// nested workers[] expansion, which knows the device, fills device_id
// directly and leaves the name empty.
struct dataplane_assignment_config {
	char device[80];
	uint32_t device_id;
	uint32_t queue_id;
	uint8_t weight;
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

	// Sizing a queue of this device takes when the queue itself declares
	// none: the middle step between the queue entry and the built-in
	// defaults. The role member is not user-configurable here; every
	// queue keeps role.rss set until its own declaration clears it.
	struct dataplane_queue_config default_queue;

	// Which shapes declared the queues; see enum
	// dataplane_queue_declaration.
	uint8_t queue_declarations;

	// The device's queues, one per rx/tx pair; the queue id is the index
	// into this array, which maps directly onto the port's RSS/RETA
	// entries. Materialized from the declaring shape by
	// dataplane_config_init, so the fields hold effective values
	// afterwards. NULL when the device declares no queues.
	uint32_t queue_count;
	struct dataplane_queue_config *queues;

	// The workers declared inside the device; the legacy shape, where
	// worker i declares and polls queue i, carrying that queue's sizing
	// and role. Expanded into the queues array and the flat top-level
	// worker list (one single-assignment worker per entry) by
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

	// The flat worker list: written directly by the top-level workers
	// section, and expanded from the nested devices[].workers[] entries
	// (one single-assignment worker per entry, in device and entry order;
	// worker i of device d polls queue i of d) by dataplane_config_init.
	// The two shapes cannot be mixed in one config.
	uint64_t worker_count;
	struct dataplane_worker_config *workers;

	char loglevel[32];

	char plugin_dir[DATAPLANE_PLUGIN_DIR_LEN];
	uint64_t module_count;
	char module_names[DATAPLANE_MAX_MODULES][DATAPLANE_MODULE_NAME_LEN];

	// Object types to load beside the built-in ones, by name.
	//
	// A plugin ships an object type as new_object_<name>; listing the name
	// here makes the dataplane load it, as the module list does for
	// modules. Built-in names may be listed and load once.
	uint64_t object_count;
	char object_names[DATAPLANE_MAX_OBJECTS][DATAPLANE_OBJECT_NAME_LEN];
};

int
dataplane_config_init(FILE *file, struct dataplane_config **config);

void
dataplane_config_free(struct dataplane_config *config);

// Whether a device declares any queue role other than the default: a queue
// out of the RSS table or a steered class.
int
dataplane_device_config_has_roles(const struct dataplane_device_config *device);
