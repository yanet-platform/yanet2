#include "device.h"
#include "dataplane.h"

#include <pthread.h>
#include <string.h>

#include "common/strutils.h"
#include "dpdk.h"
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

	strtcpy(device->port_name, config->port_name, 80);

	return 0;
}

int
dataplane_device_get_mac(
	struct dataplane_device *device, struct rte_ether_addr *ether_addr
) {
	return dpdk_port_get_mac(device->port_id, ether_addr);
}
