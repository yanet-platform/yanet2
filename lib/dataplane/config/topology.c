#include "topology.h"

#include <string.h>

#include "common/memory.h"
#include "common/memory_address.h"

#include "lib/dataplane/config/zone.h"
#include "lib/logging/log.h"

struct dp_port *
dp_topology_alloc_devices(struct dp_config *dp_config, size_t count) {
	struct dp_port *ports = (struct dp_port *)memory_balloc(
		&dp_config->memory_context, sizeof(struct dp_port) * count
	);
	if (ports == NULL) {
		return NULL;
	}
	memset(ports, 0, sizeof(struct dp_port) * count);

	dp_config->dp_topology.device_count = count;
	SET_OFFSET_OF(&dp_config->dp_topology.devices, ports);

	return ports;
}

int
dp_topology_set_device_rss(
	struct dp_config *dp_config,
	uint32_t device_id,
	const uint8_t *key,
	uint16_t key_len,
	const uint16_t *reta,
	uint16_t reta_size
) {
	// Reject an out-of-contract report before allocating or storing
	// anything, so every consumer of dp_port's RSS fields can trust
	// reta_size and key_len without re-checking them. The 512-RETA and
	// key_len>=4 bounds are otherwise only enforced by the live NIC
	// producer, and a different producer (or a fuzzed/malicious caller)
	// bypassing it would leave the shared config in a state that
	// overflows a consumer's fixed-size stack buffer or under-reads a
	// short key.
	if (reta_size == 0 || reta_size > DP_TOPOLOGY_RSS_RETA_SIZE_MAX ||
	    (reta_size & (reta_size - 1)) != 0 ||
	    key_len < DP_TOPOLOGY_RSS_KEY_LEN_MIN || key == NULL ||
	    reta == NULL) {
		return -1;
	}

	uint64_t device_count = dp_config->dp_topology.device_count;
	if (device_id >= device_count) {
		LOG(ERROR,
		    "device_id %u out of range (device_count %lu)",
		    device_id,
		    device_count);
		return -1;
	}

	struct dp_port *devices = ADDR_OF(&dp_config->dp_topology.devices);
	struct dp_port *device = devices + device_id;

	uint8_t *key_copy =
		(uint8_t *)memory_balloc(&dp_config->memory_context, key_len);
	if (key_copy == NULL) {
		return -1;
	}
	memcpy(key_copy, key, key_len);

	uint16_t *reta_copy = (uint16_t *)memory_balloc(
		&dp_config->memory_context, sizeof(uint16_t) * reta_size
	);
	if (reta_copy == NULL) {
		memory_bfree(&dp_config->memory_context, key_copy, key_len);
		return -1;
	}
	memcpy(reta_copy, reta, sizeof(uint16_t) * reta_size);

	device->rss_key_len = key_len;
	SET_OFFSET_OF(&device->rss_key, key_copy);

	device->rss_reta_size = reta_size;
	SET_OFFSET_OF(&device->rss_reta, reta_copy);

	device->rss_valid = true;

	return 0;
}

int
dp_topology_set_device_worker_count(
	struct dp_config *dp_config, uint32_t device_id, uint64_t worker_count
) {
	uint64_t device_count = dp_config->dp_topology.device_count;

	if (device_id >= device_count) {
		LOG(ERROR,
		    "device_id %u out of range (device_count %lu)",
		    device_id,
		    device_count);
		return -1;
	}

	struct dp_port *devices = ADDR_OF(&dp_config->dp_topology.devices);
	devices[device_id].worker_count = worker_count;

	return 0;
}
