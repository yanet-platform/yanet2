#include "device.h"
#include "dataplane.h"

#include <pthread.h>
#include <stdlib.h>
#include <string.h>

#include "common/strutils.h"
#include "dpdk.h"
#include "flow.h"
#include "lib/dataplane/config/topology.h"
#include "lib/logging/log.h"

// Maximum RSS key / RETA sizes we are willing to stage on the stack while
// querying the NIC. RTE_ETH_RSS_RETA_SIZE_512 (the largest table any driver
// reports today) and 64 bytes (generous even for wide symmetric keys) both
// comfortably cover mlx5 and i40e.
#define DATAPLANE_RSS_KEY_BUF_LEN 64
#define DATAPLANE_RSS_RETA_BUF_LEN 512

static void
dataplane_device_start_query_rss(
	struct dataplane *dataplane, struct dataplane_device *device
) {
	uint8_t key_buf[DATAPLANE_RSS_KEY_BUF_LEN];
	uint16_t key_len;
	uint16_t reta_buf[DATAPLANE_RSS_RETA_BUF_LEN];
	uint16_t reta_size;

	if (dpdk_port_get_rss_key(
		    device->port_id, key_buf, sizeof(key_buf), &key_len
	    )) {
		return;
	}
	if (dpdk_port_get_rss_reta(
		    device->port_id,
		    reta_buf,
		    DATAPLANE_RSS_RETA_BUF_LEN,
		    &reta_size
	    )) {
		return;
	}

	for (uint32_t instance_idx = 0;
	     instance_idx < dataplane->instance_count;
	     ++instance_idx) {
		struct dataplane_instance *instance =
			dataplane->instances + instance_idx;
		if (dp_topology_set_device_rss(
			    instance->dp_config,
			    device->device_id,
			    key_buf,
			    key_len,
			    reta_buf,
			    reta_size
		    )) {
			LOG(WARN,
			    "failed to store RSS state for device id=%u "
			    "instance=%u",
			    device->device_id,
			    instance_idx);
		}
	}
}

// Apply the queue roles to a started port: the RSS table over the RSS
// queues only, then the rte_flow rules for the steered classes.
//
// The table is rewritten only when the port hashes and some queue is left
// out of it. Without RSS nothing is hashed: unmatched traffic lands on
// queue 0, which validation keeps an RSS queue. A rule the driver rejects
// fails the start, as does a driver with no flow API.
static int
dataplane_device_steer(struct dataplane_device *device) {
	uint16_t rss_queues[device->queue_count];
	uint16_t rss_queue_count = 0;
	for (uint32_t idx = 0; idx < device->queue_count; ++idx) {
		if (device->queue_roles[idx].rss) {
			rss_queues[rss_queue_count++] = (uint16_t)idx;
		}
	}
	if (device->rss_hash != 0 && rss_queue_count < device->queue_count &&
	    dpdk_port_set_rss_reta(
		    device->port_id, rss_queues, rss_queue_count
	    ) != 0) {
		LOG(ERROR,
		    "device %s: failed to keep RSS off the steering queues",
		    device->port_name);
		return -1;
	}

	int rc = dataplane_flow_steer(
		device->port_id,
		device->rss_hash,
		device->queue_roles,
		(uint16_t)device->queue_count
	);
	if (rc != 0) {
		LOG(ERROR,
		    "device %s: failed to install the steering rules",
		    device->port_name);
		return -1;
	}
	LOG(INFO,
	    "device %s: queue roles applied, %u of %u queues are RSS "
	    "queues",
	    device->port_name,
	    rss_queue_count,
	    device->queue_count);
	return 0;
}

int
dataplane_device_start(
	struct dataplane *dataplane, struct dataplane_device *device
) {
	LOG(INFO,
	    "start dataplane device id=%u with %u queues",
	    device->device_id,
	    device->queue_count);
	int rc = dpdk_port_start(device->port_id);
	if (rc) {
		LOG(ERROR,
		    "failed to start dataplane device id=%u port=%s: error=%d",
		    device->device_id,
		    device->port_name,
		    rc);
		return -1;
	}

	if (device->queue_roles != NULL &&
	    dataplane_device_steer(device) != 0) {
		return -1;
	}

	// Query the NIC's RSS hash key and redirection table for later use by
	// RSS-aware worker-affinity lookups. This is a best-effort query: no
	// RSS state (unsupported driver, RSS disabled, un-negotiated virtio
	// feature bits) leaves the device's rss_valid flag clear and callers
	// fall back to their non-RSS-aware path.
	dataplane_device_start_query_rss(dataplane, device);

	return 0;
}

int
dataplane_device_init(
	struct dataplane *dataplane,
	struct dataplane_device *device,
	uint32_t device_id,
	struct dataplane_device_config *config
) {
	(void)dataplane;

	device->device_id = device_id;
	device->queue_count = config->worker_count;
	device->rss_hash = config->rss_hash;
	device->queue_roles = NULL;
	strtcpy(device->port_name, config->port_name, 80);

	if (dataplane_device_config_has_roles(config)) {
		device->queue_roles = (struct dataplane_queue_role *)malloc(
			sizeof(struct dataplane_queue_role) *
			config->worker_count
		);
		if (device->queue_roles == NULL) {
			return -1;
		}
		for (uint32_t idx = 0; idx < config->worker_count; ++idx) {
			device->queue_roles[idx] = config->workers[idx].role;
		}
	}
	device->rss_queue_count = 0;
	for (uint32_t idx = 0; idx < device->queue_count; ++idx) {
		device->rss_queue_count +=
			!dataplane_device_queue_is_steering(device, idx);
	}

	if (dpdk_port_init(
		    config->port_name,
		    &device->port_id,
		    config->rss_hash,
		    config->worker_count,
		    config->worker_count,
		    config->mtu,
		    config->max_lro_packet_size
	    )) {

		LOG(ERROR, "failed to init dpdk port %s", config->port_name);
		return -1;
	}

	return 0;
}

int
dataplane_device_get_mac(
	struct dataplane_device *device, struct rte_ether_addr *ether_addr
) {
	return dpdk_port_get_mac(device->port_id, ether_addr);
}
