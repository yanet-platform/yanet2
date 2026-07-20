#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

struct dp_port {
	uint16_t port_id;
	char device_name[80];

	// RSS introspection state for this device, queried once at device
	// start.
	//
	// False means the device has no usable RSS state (query failed, RSS
	// disabled, or the driver does not support it) — callers must fall
	// back to a non-RSS-aware path in that case.
	bool rss_valid;

	// Raw RSS hash key, exactly as reported by the NIC driver.
	//
	// Length and layout are driver-specific (mlx5 and i40e differ), so
	// callers must not assume a fixed size.
	uint16_t rss_key_len;
	uint8_t *rss_key;

	// Redirection table: rss_reta[slot] is the rx-queue id the NIC maps
	// RETA slot to.
	//
	// The table size is queried at runtime and also varies by driver.
	uint16_t rss_reta_size;
	uint16_t *rss_reta;
};

struct dp_topology {
	uint64_t device_count;
	struct dp_port *devices;
};

struct dp_config;

// Allocate the dp_topology.devices array of count slots and wire it into
// dp_config. Returns NULL on out-of-memory.
//
// The slots are zero-initialised, so a device whose RSS state is never
// filled reads back as the not-valid sentinel: rss_valid false and the
// key and reta offset pointers NULL. The caller only needs to fill the
// entries it has RSS state for.
struct dp_port *
dp_topology_alloc_devices(struct dp_config *dp_config, size_t count);

// Store the RSS hash key and redirection table for device_id in dp_topology,
// copying key and reta into fresh shared-memory blocks owned by
// dp_config->memory_context.
//
// On success the device's rss_valid flag is set. Returns non-zero on
// allocation failure, leaving the device's RSS state untouched.
int
dp_topology_set_device_rss(
	struct dp_config *dp_config,
	uint32_t device_id,
	const uint8_t *key,
	uint16_t key_len,
	const uint16_t *reta,
	uint16_t reta_size
);
