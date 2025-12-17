#pragma once

#include "config.h"
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
 * Craft a state synchronization packet from the given packet.
 *
 * @param sync_config The firewall state sync configuration
 * @param packet The original packet to extract 5-tuple from
 * @param direction The direction of the sync packet (INGRESS or EGRESS)
 * @param sync_pkt Pre-allocated packet to fill with sync data
 * @return 0 on success, or -1 on failure
 */
int
fwstate_craft_state_sync_packet(
	const struct fwstate_sync_config *sync_config,
	const struct packet *packet,
	const enum sync_packet_direction direction,
	struct packet *sync_pkt
);
