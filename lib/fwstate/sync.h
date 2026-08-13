#pragma once

#include "config.h"
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

// Forward declarations
struct dp_worker;
struct packet;

enum sync_packet_direction {
	// NOLINTBEGIN(readability-identifier-naming)
	SYNC_NONE,
	SYNC_INGRESS,
	SYNC_EGRESS,
	// NOLINTEND(readability-identifier-naming)
};

/**
 * Report whether an emission config can produce a valid sync frame.
 *
 * A zeroed config (all-zero destination MAC or zero multicast port)
 * would send the frame nowhere, so callers skip crafting instead.
 */
static inline bool
fwstate_sync_emit_config_usable(const struct fwstate_sync_emit_config *config) {
	for (size_t idx = 0; idx < sizeof(config->dst_ether.addr); ++idx) {
		if (config->dst_ether.addr[idx] != 0) {
			return config->port_multicast != 0;
		}
	}
	return false;
}

/**
 * Craft a state synchronization packet from the given packet.
 *
 * @param emit_config The emission-side sync addressing
 * @param packet The original packet to extract 5-tuple from
 * @param direction The direction of the sync packet (INGRESS or EGRESS)
 * @param sync_pkt Pre-allocated packet to fill with sync data
 * @return 0 on success, or -1 on failure
 */
int
fwstate_craft_state_sync_packet(
	const struct fwstate_sync_emit_config *emit_config,
	const struct packet *packet,
	const enum sync_packet_direction direction,
	struct packet *sync_pkt
);
