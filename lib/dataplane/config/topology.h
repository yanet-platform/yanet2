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

	// Redirection table (RETA): rss_reta[slot] is the rx-queue id the NIC
	// maps that slot to.
	//
	// The table size is queried at runtime and also varies by driver.
	uint16_t rss_reta_size;
	uint16_t *rss_reta;

	// Count of workers bound to this device, across every instance -- not
	// to be confused with dp_config->worker_count, the instance-wide
	// total across every device.
	uint64_t worker_count;
};

struct dp_topology {
	uint64_t device_count;
	struct dp_port *devices;
};

// Largest RETA size any NIC driver reports today.
//
// This mirrors DPDK's RTE_ETH_RSS_RETA_SIZE_512, which both the live NIC
// producer (dataplane/dpdk.c) and the tcpproxy consumer size their fixed
// buffers against. topology.c is DPDK-free, so the value is duplicated here
// as a plain constant rather than pulled in via rte_ethdev.h — keep it in
// sync with RTE_ETH_RSS_RETA_SIZE_512 if that ever changes.
#define DP_TOPOLOGY_RSS_RETA_SIZE_MAX 512

// Shortest RSS key dp_topology_set_device_rss accepts.
//
// common/thash.h's thash_toeplitz seeds its 32-bit window from the first
// four key bytes unconditionally, so any key shorter than that is unusable
// by every consumer. There is no meaningful upper bound to enforce here:
// the key buffer is sized to key_len exactly and thash reads exactly
// key_len bytes.
#define DP_TOPOLOGY_RSS_KEY_LEN_MIN 4

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
// Enforces the contract every consumer of dp_port's RSS fields relies on:
// reta_size must be in (0, DP_TOPOLOGY_RSS_RETA_SIZE_MAX] and key_len must
// be at least DP_TOPOLOGY_RSS_KEY_LEN_MIN. A call outside that contract is
// rejected outright — nothing is allocated or stored, and rss_valid stays
// false — so the device falls back to its non-RSS-aware path instead of an
// out-of-contract report reaching a consumer. An out-of-range device_id is
// rejected the same way.
//
// Assumes a single call per (instance, device) at device start — a
// re-invocation for the same device would leak the previously allocated
// key and reta arrays instead of freeing them.
//
// On success the device's rss_valid flag is set. Returns non-zero on
// rejection or allocation failure, leaving the device's RSS state untouched.
int
dp_topology_set_device_rss(
	struct dp_config *dp_config,
	uint32_t device_id,
	const uint8_t *key,
	uint16_t key_len,
	const uint16_t *reta,
	uint16_t reta_size
);

// Set the count of workers bound to device_id, across every instance, so
// dp_config_device_worker_count can answer in O(1).
//
// Must be the device-wide count, not a single instance's tally: queue ids
// are handed out per device across every instance, so a device spanning
// more than one instance would otherwise be undercounted.
//
// Returns non-zero if device_id is out of range for dp_topology, leaving
// the count untouched.
int
dp_topology_set_device_worker_count(
	struct dp_config *dp_config, uint32_t device_id, uint64_t worker_count
);
